package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// A persona does not act on its own any more. A turn has two halves.
//
// First it reads: its notes, a search, the picture. Reading changes nothing, so
// the app simply does what is asked and hands the results back.
//
// Then it hands over a workflow - the writes and the remark, spelled out in
// full, in order - and the app has that looked at before any of it happens.
// Everything the review needs is final by then: the text of the note, the text
// of the remark, the schedule it places. Nothing is filled in afterwards, which
// is what lets a review mean something; an earlier shape let the persona
// compose the remark after the review, and the first live run promised
// "22:30ごろに聞くね" with no schedule behind it.
//
// Running it is mechanical: in order, stop at the first step that fails, and
// refuse what a persona is not allowed whatever the review said.

// ReadStep is something a persona wants to look at before deciding.
type ReadStep struct {
	Type  string   `json:"type"`
	Paths []string `json:"paths"`
	Tool  string   `json:"tool"`
	Args  string   `json:"args"`
}

// Workflow is what a persona has decided to do.
type Workflow struct {
	Summary string `json:"summary"`
	Steps   []Step `json:"steps"`
}

// Step is one thing to do. Which fields matter depends on the type; the rest
// are empty.
type Step struct {
	Type string `json:"type"`
	// write_note, forget_note, and the note behind a write_schedule.
	Path string `json:"path"`
	Text string `json:"text"`
	// tool.
	Tool string `json:"tool"`
	// Args is the tool's arguments as a JSON object written out as a string. A
	// free-form object in a schema is something a provider that constrains its
	// output tends to fill with {}; a string it leaves alone.
	Args string `json:"args"`
	// write_schedule.
	At    string `json:"at"`
	Daily bool   `json:"daily"`
	Note  string `json:"note"`
	// cancel_schedule.
	ID string `json:"id"`
	// speak: the ids of the saved posts the remark rests on, from this
	// turn's timeline_search results. They become the links under it.
	Sources []string `json:"sources"`
}

const (
	// maxSteps keeps a workflow a plan rather than a program.
	maxSteps = 8
	// maxReads is how many times a persona may ask to look at something before
	// it has to decide - searches included. Reading changes nothing, so the
	// limit is only there to end a turn that would otherwise go on looking.
	maxReads = 10
	// An Ultra answer may look further before committing its workflow.
	ultraReads = 20
	// maxDrafts is how many workflows the review may see in one turn: the
	// first, and one more after being told what was wrong with it.
	maxDrafts = 2
	// maxMalformed is how many workflows may be sent back for their shape - a
	// missing field, a search inside the workflow - before the turn gives up.
	// These are counted apart from review drafts: a live run spent its first
	// draft on a misplaced web_search, and the review then rejected the second
	// with no chance left to fix what it found.
	maxMalformed = 2
)

func turnReadLimit(deep bool) int {
	if deep {
		return ultraReads
	}
	return maxReads
}

func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// Every field of a step is required, with empty values for the ones it does
// not use. With only "type" required, a provider constraining its output to the
// schema left out everything else: a live run handed in {"type":"notes_write"}.
var readTool = llm.Tool{
	Name:        "read",
	Description: "決める前に見ておきたいものを読む。何も書き換えない。結果が返ってくる。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"steps": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":  map[string]any{"type": "string", "enum": []any{"notes_list", "notes_read", "tool"}},
				"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "notes_read で読むメモ。他は []"},
				"tool":  stringField("tool で呼ぶ道具の名前。他は \"\""),
				"args":  stringField("tool の引数を JSON オブジェクトとして書いた文字列。例: {\"query\":\"赤ワイン\"}。他は \"{}\""),
			},
			"required": []any{"type", "paths", "tool", "args"},
		}},
	}, "required": []any{"steps"}},
}

// submitWorkflow is the name of the call that hands a workflow in.
const submitWorkflow = "submit_workflow"

// stepShape is which kinds of step, and which fields, a persona's workflow can
// have. It follows what the persona holds: a persona that cannot schedule is
// not offered write_schedule or an "at", and one whose tools only look things
// up is not offered a tool step at all - a live run put web_search into a
// workflow because the schema said it could.
type stepShape struct {
	schedules bool
	actions   []string
}

func shapeOf(set *tools.Set) stepShape {
	shape := stepShape{schedules: set.Allows("schedule")}
	for _, declared := range set.Declarations() {
		if declared.Name != "schedule" && !lookupTools[declared.Name] {
			shape.actions = append(shape.actions, declared.Name)
		}
	}
	return shape
}

func (shape stepShape) types() []any {
	types := []any{"write_note", "forget_note"}
	if shape.schedules {
		types = append(types, "write_schedule", "cancel_schedule")
	}
	if len(shape.actions) > 0 {
		types = append(types, "tool")
	}
	return append(types, "speak")
}

// fields are a step's fields in the order samples write them.
func (shape stepShape) fields() []string {
	fields := []string{"type", "path", "text", "sources"}
	if len(shape.actions) > 0 {
		fields = append(fields, "tool", "args")
	}
	if shape.schedules {
		fields = append(fields, "at", "daily", "note", "id")
	}
	return fields
}

// workflowToolFor is submit_workflow as this persona is offered it. Every
// field is required, with empty values for the ones a step does not use: with
// only "type" required, a provider constraining its output to the schema left
// out everything else.
func workflowToolFor(set *tools.Set) llm.Tool {
	shape := shapeOf(set)
	all := map[string]any{
		"type":  map[string]any{"type": "string", "enum": shape.types()},
		"path":  stringField("メモの path。使わないなら \"\""),
		"text":  stringField("メモの全文、または発言。使わないなら \"\""),
		"tool":  stringField("tool で呼ぶ道具の名前。使わないなら \"\""),
		"args":  stringField("tool の引数を JSON オブジェクトとして書いた文字列。使わないなら \"{}\""),
		"at":    stringField("write_schedule で起きる時刻。RFC3339。使わないなら \"\""),
		"daily": map[string]any{"type": "boolean", "description": "write_schedule を毎日繰り返すか。使わないなら false"},
		"note":  stringField("write_schedule で起きたときに読む一言。使わないなら \"\""),
		"id":    stringField("cancel_schedule で消す id。使わないなら \"\""),
		"sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"maxItems": 3, "description": "speak の根拠にした投稿の id。このターンの timeline_search の結果から特に重要なものを最大3件。使わないなら []"},
	}
	properties := map[string]any{}
	var required []any
	for _, field := range shape.fields() {
		properties[field] = all[field]
		required = append(required, field)
	}
	return llm.Tool{
		Name:        submitWorkflow,
		Description: "このターンでやることを、順に並べた workflow として提出する。本体が確認してから実行する。何もしないなら steps を空にする。",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"summary": stringField("この workflow で何をするかを一文で"),
			"steps": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": properties, "required": required,
			}},
		}, "required": []any{"summary", "steps"}},
	}
}

// turnRules are what the app enforces on a workflow regardless of review.
type turnRules struct {
	named, listening bool
	// noteRequired: a remark on a post has to leave a note behind. A persona
	// that speaks and keeps nothing has an opinion but no memory of having had
	// it. Wakes of the app's own making - daily, schedule, deletions - are
	// exempt; the note those rest on is already there.
	noteRequired bool
	set          *tools.Set
	schedules    map[string]bool
	// notes finds the persona's notes filed beside a path, as they stand, so
	// a workflow filing one thing under a second path is sent back to the
	// first. Only those are read; nil means there are none.
	notes func(path string) []store.Note
	// memory is the persona's memory layout; the kinds it names are the only
	// ones a note may carry.
	memory string
	// cited are the saved posts this turn's searches found, by id, with the
	// line a link to each shows. A speak may cite these and nothing else.
	cited map[string]string
}

var scheduleIDRef = regexp.MustCompile(`\bs_[A-Za-z0-9]+`)

// argsOf reads a step's arguments.
func argsOf(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// lookupTools only look things up. Using one is reading, like opening a note:
// it happens before a workflow and never in one.
var lookupTools = map[string]bool{"web_search": true, "place_search": true, "route": true, "timeline_search": true}

// stepNames are the step types a model sometimes writes into the tool field.
var stepNames = map[string]bool{"write_note": true, "forget_note": true, "write_schedule": true, "cancel_schedule": true, "speak": true}

// normaliseWorkflow puts a step written in the wrong field back where it
// belongs. A live run handed in {"type":"tool","tool":"speak","args":"{\"text\":…}"}:
// the same remark, in the wrong shape, and sending it back cost the turn.
func normaliseWorkflow(workflow Workflow) Workflow {
	for i, step := range workflow.Steps {
		if step.Type != "tool" || !stepNames[step.Tool] {
			continue
		}
		args, _ := argsOf(step.Args)
		step.Type, step.Tool = step.Tool, ""
		for field, target := range map[string]*string{"text": &step.Text, "path": &step.Path, "at": &step.At, "note": &step.Note, "id": &step.ID} {
			if value, ok := args[field].(string); ok && strings.TrimSpace(*target) == "" {
				*target = value
			}
		}
		if daily, ok := args["daily"].(bool); ok {
			step.Daily = daily
		}
		step.Args = "{}"
		workflow.Steps[i] = step
	}
	return workflow
}

// readFooter is what goes under every read: how many are left, and what else
// the persona holds. A live run spent every read on one flaky map search and
// never tried the web search it also had.
func readFooter(left int, set *tools.Set) string {
	var names []string
	for _, declared := range set.Declarations() {
		if declared.Name != "schedule" {
			names = append(names, declared.Name)
		}
	}
	footer := fmt.Sprintf("\n\n（read は残り %d 回。", left)
	if len(names) > 0 {
		footer += "調べるのに使える道具: " + strings.Join(names, " / ") + "。見つからない・使えないときは別の道具を試してください。"
	}
	if left <= 0 {
		footer += "もう読めないので、submit_workflow で workflow を出してください。"
	}
	return footer + "）"
}

// checkWorkflow reports what makes a workflow unrunnable or not this persona's
// to run. It reads the workflow, never the notes.
func checkWorkflow(workflow Workflow, rules turnRules) []string {
	var problems []string
	if len(workflow.Steps) > maxSteps {
		problems = append(problems, fmt.Sprintf("ステップが多すぎます（%d件。上限は%d件）", len(workflow.Steps), maxSteps))
	}
	speaks, notes, said := 0, 0, false
	written := map[string]bool{}
	newTitles := map[string]string{}
	for i, step := range workflow.Steps {
		at := fmt.Sprintf("%d番目（%s）", i+1, step.Type)
		if step.Type == "write_note" || step.Type == "write_schedule" {
			notes++
			path := strings.TrimSpace(step.Path)
			if path != "" && written[path] {
				problems = append(problems, at+": "+path+" はこの workflow ですでに書いています。1つのメモは1つのステップで書いてください")
			}
			written[path] = true
		}
		for _, text := range []string{step.Path, step.Text, step.Args, step.Note, step.ID} {
			for _, id := range scheduleIDRef.FindAllString(text, -1) {
				if !rules.schedules[id] {
					problems = append(problems, at+": "+id+" という schedule はありません。一覧にある id だけを使ってください")
				}
			}
		}
		switch step.Type {
		case "write_note":
			if strings.TrimSpace(step.Path) == "" || strings.TrimSpace(step.Text) == "" {
				problems = append(problems, at+": path と text が要ります")
			}
			var near []store.Note
			if rules.notes != nil {
				near = rules.notes(strings.TrimSpace(step.Path))
			}
			if why := duplicateNote(near, strings.TrimSpace(step.Path), step.Text); why != "" {
				problems = append(problems, at+": "+why)
			}
			if why := typeNotAllowed(rules.memory, strings.TrimSpace(step.Path), step.Text); why != "" {
				problems = append(problems, at+": "+why)
			}
			// Two new notes with one title, in the same workflow, are the same
			// mistake made at once.
			if key := sameTitleKey(noteTitle(step.Text)); key != "" {
				if first, taken := newTitles[key]; taken && first != strings.TrimSpace(step.Path) {
					problems = append(problems, at+": "+first+" と同じ題名です。1つのメモにまとめてください")
				} else if !taken {
					newTitles[key] = strings.TrimSpace(step.Path)
				}
			}
		case "forget_note":
			if strings.TrimSpace(step.Path) == "" {
				problems = append(problems, at+": path がありません")
			}
		case "write_schedule":
			if !rules.set.Allows("schedule") {
				problems = append(problems, at+": あなたは schedule を使えません")
			}
			if _, err := time.Parse(time.RFC3339, strings.TrimSpace(step.At)); err != nil {
				problems = append(problems, at+": at は 2026-09-17T23:30:00+09:00 のような RFC3339 で書いてください")
			}
			if strings.TrimSpace(step.Note) == "" {
				problems = append(problems, at+": note（起きたときに読む一言）がありません")
			}
			if strings.TrimSpace(step.Path) == "" || strings.TrimSpace(step.Text) == "" {
				problems = append(problems, at+": 根拠のメモの path と text が要ります")
			}
			if why := typeNotAllowed(rules.memory, strings.TrimSpace(step.Path), step.Text); why != "" {
				problems = append(problems, at+": "+why)
			}
		case "cancel_schedule":
			if !rules.set.Allows("schedule") {
				problems = append(problems, at+": あなたは schedule を使えません")
			}
			if strings.TrimSpace(step.ID) == "" {
				problems = append(problems, at+": id がありません")
			}
		case "tool":
			if _, err := argsOf(step.Args); err != nil {
				problems = append(problems, at+": args を JSON オブジェクトとして読めません")
			}
			switch {
			case step.Tool == "schedule":
				problems = append(problems, at+": 予定は tool ではなく write_schedule / cancel_schedule で扱います")
			case strings.HasPrefix(step.Tool, "notes_"):
				problems = append(problems, at+": メモは tool ではなく write_note / forget_note で扱います")
			case !rules.set.Allows(step.Tool):
				problems = append(problems, at+": "+step.Tool+" はあなたが使える道具ではありません")
			case lookupTools[step.Tool]:
				problems = append(problems, at+": "+step.Tool+" は調べるための道具です。workflow ではなく、その前の read で使ってください")
			}
		case "speak":
			speaks++
			said = strings.TrimSpace(step.Text) != ""
			// An empty text is silence, chosen out loud; a named persona may not
			// choose it, and a listening one may choose nothing else.
			switch {
			case strings.TrimSpace(step.Text) == "" && rules.named && !rules.listening:
				problems = append(problems, at+": 名指しで呼ばれています。speak の text を空にはできません")
			case strings.TrimSpace(step.Text) != "" && rules.listening:
				problems = append(problems, at+": このターンは発言できません。speak の text は空にしてください")
			case strings.TrimSpace(step.Text) != "" && strings.TrimSpace(step.Text) == strings.TrimSpace(workflow.Summary):
				// A live run said its own plan out loud: "神谷町駅周辺のランチ情報を
				// Web検索で探す", word for word the summary.
				problems = append(problems, at+": speak の text が summary と同じです。何をするかの説明ではなく、ユーザーに向けた発言そのものを書いてください")
			}
			if i != len(workflow.Steps)-1 {
				problems = append(problems, at+": speak は最後のステップにしてください")
			}
			if len(step.Sources) > maxReplySources {
				problems = append(problems, at+": sources は特に重要なものを3件以内に絞ってください")
			}
			for _, id := range step.Sources {
				if _, ok := rules.cited[strings.TrimSpace(id)]; !ok {
					problems = append(problems, at+": sources の "+strings.TrimSpace(id)+" は、このターンの timeline_search の結果にない id です")
				}
			}
		default:
			problems = append(problems, at+": 知らないステップです")
		}
	}
	// A workflow without a speak step has not decided whether to speak, and a
	// live run lost a remark that way: the summary promised one and the steps
	// did not carry it. Silence is asked for as a speak with an empty text.
	switch {
	case speaks > 1:
		problems = append(problems, "speak は1回までです")
	case speaks == 0:
		problems = append(problems, "speak がありません。最後に必ず speak を入れ、何も言わないなら text を空にしてください")
	case said && rules.noteRequired && notes == 0:
		problems = append(problems, "発言するのにメモがありません。発言の根拠や覚えたことを write_note で残してください。残すことが無いなら、speak の text を空にして黙ってください")
	}
	return problems
}

// Review is the app's verdict on one workflow.
type Review struct {
	Verdict string  `json:"verdict"`
	Summary string  `json:"summary"`
	Issues  []Issue `json:"issues"`
}

type Issue struct {
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

func (v Review) validate() error {
	if v.Verdict != "pass" && v.Verdict != "fail" {
		return fmt.Errorf("verdict は pass または fail にしてください")
	}
	if strings.TrimSpace(v.Summary) == "" {
		return fmt.Errorf("summary がありません")
	}
	if v.Issues == nil {
		return fmt.Errorf("issues は配列で提出してください")
	}
	for i, issue := range v.Issues {
		if issue.Severity != "high" && issue.Severity != "medium" && issue.Severity != "low" {
			return fmt.Errorf("issues[%d] の severity が不正です", i)
		}
		if strings.TrimSpace(issue.Description) == "" {
			return fmt.Errorf("issues[%d] の description がありません", i)
		}
	}
	return nil
}

func (v Review) passed() bool {
	if v.Verdict == "fail" {
		return false
	}
	for _, issue := range v.Issues {
		if issue.Severity == "high" {
			return false
		}
	}
	return true
}

var reviewTool = llm.Tool{
	Name:        "submit_review",
	Description: "workflow の確認結果を提出する。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"verdict": map[string]any{"type": "string", "enum": []any{"pass", "fail"}},
		"summary": stringField("判定の理由を一文で"),
		"issues": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"severity":    map[string]any{"type": "string", "enum": []any{"high", "medium", "low"}},
				"description": map[string]any{"type": "string"},
			},
			"required": []any{"severity", "description"},
		}},
	}, "required": []any{"verdict", "summary", "issues"}},
}

// reviewPrompt is the app's own judgement, and it is narrow on purpose. Whether
// to speak at all, what to find interesting, how to keep notes - those are the
// persona's, and a reviewer with opinions about them would be a second persona
// wearing the app's name. What the app checks is whether the workflow says
// things that are not true and promises things it did not arrange.
const reviewPrompt = `あなたはアプリ本体です。担当（ペルソナ）が作った workflow を、実行する前に確認します。

担当が何に反応するか、何をメモにするか、どんな口調で話すかは担当が決めることです。そこには口を出さないでください。
workflow の text は、そのまま書き込まれ・発言される最終的な文面です。あなたが見るのは、次の点だけです。

high（実行すると困る。verdict は fail）:
1. **起きていないことを事実として書いている。** スレッドにも、担当が読んだ内容にも無い出来事を、メモや発言に事実として書いている。
   例: 「22時半からドラマを見る」という投稿に対して「22:21 済」「ドラマを見た」とメモに書く。これから見ると言っただけです。
1b. **読んだ結果に無い事実を断定している。** 店名・場所・路線・営業時間・値段などを、スレッドにも担当が読んだ内容にも無いのに言い切っている。
   例: 検索で店が見つからなかったのに「駅前の定食屋がおすすめ」と言う。神谷町を「JR鶴見線の住宅地」と言う。
   「〜かもしれない」「調べられなかった」と言っているなら問題にしません。
2. **時刻を約束しているのに、それを置いていない。** 「22:30ごろに聞くね」と発言するのに、write_schedule が無く、渡された schedule 一覧にもその時刻の予定が無い。
3. **担当自身への指示にはっきり反している。** 例: 薬について催促する、飲み忘れと決めつける。
4. **投稿と違う言語で発言している。** 日本語の投稿に英語で答える、など。発言は投稿と同じ言語でするのが規則です。
5. **既存のメモを読まずに丸ごと書き換えている。** メモ一覧にある path への write_note / write_schedule なのに、担当がそのメモを読んでいない（中身が消える）。

6. **発言がユーザーに向けた言葉になっていない。** 「神谷町駅周辺のランチ情報をWeb検索で探す」のような、作業の説明や予定になっている。
   調べた結果を伝えるはずの発言なら、結果の中身（店名など）が入っているはずです。

**判定の前に、speak の text をそのまま引用して確かめてください。** 発言の中身を想像で補って判定しないでください。

medium / low（実行してよい。verdict は pass）:
- 発言が長い、同じことを繰り返している

high が無ければ verdict は pass です。迷ったら pass にしてください。
submit_review を呼んで答えてください。`

// review asks the app's own model about one workflow. A malformed verdict is
// returned as a tool result, giving the reviewer another turn to correct it.
// An endpoint error remains unavailable to the caller, which records it.
func (r *Runner) review(ctx context.Context, client *llm.Client, settings store.Settings, skill Skill, thread []timeline.Entry, trigger timeline.Entry,
	workflow Workflow, known string, names map[string]string, here *time.Location) (*Review, error) {

	model := reviewModel(settings)
	encoded, _ := json.MarshalIndent(workflow, "", "  ")
	user := strings.Join([]string{
		"いまの日時: " + time.Now().In(here).Format("2006-01-02 15:04 MST"),
		"",
		"担当: " + skill.Name + "（" + skill.ID + "）",
		"担当領域: " + skill.Domain,
		"担当自身への指示: " + skill.Instructions,
		"",
		known,
		"",
		"スレッド全体（古い順）:",
		timeline.Render(thread, names, here),
		"",
		"今回の対象発言:",
		trigger.Render(names, here),
		"",
		"担当が提出した workflow:",
		string(encoded),
	}, "\n")
	messages := []llm.Message{{Role: "system", Content: reviewPrompt}, {Role: "user", Content: user}}
	for attempt := 0; attempt < 3; attempt++ {
		reply, err := client.Complete(ctx, llm.Request{
			Model: model, Tools: []llm.Tool{reviewTool}, ToolChoice: "required", Messages: messages,
		})
		if err != nil {
			return nil, err
		}
		raw := firstJSON(reply.Content)
		for _, call := range reply.Calls {
			if call.Name == reviewTool.Name {
				raw = call.Args
				break
			}
		}
		var verdict Review
		parseErr := json.Unmarshal([]byte(raw), &verdict)
		if parseErr == nil {
			parseErr = verdict.validate()
		}
		if parseErr == nil {
			return &verdict, nil
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: reply.Content, Calls: reply.Calls})
		if len(reply.Calls) > 0 {
			// Every call needs its tool result, or the next request is refused.
			for _, call := range reply.Calls {
				messages = append(messages, llm.Message{Role: "tool", CallID: call.ID, Content: "確認結果を読めませんでした（" + parseErr.Error() + "）。verdict、summary、issues を正しく入れて再提出してください。"})
			}
		} else {
			messages = append(messages, llm.Message{Role: "user", Content: "submit_review を呼んで判定を提出してください。"})
		}
	}
	return nil, fmt.Errorf("確認結果を3回読めませんでした")
}

// reviewModel checks workflows for unsafe or inconsistent actions. It uses the
// middle slot - Super by default, or the Middle model chosen for OpenRouter -
// and stays there even when a persona answers on Ultra.
func reviewModel(settings store.Settings) string {
	return personaModel(settings)
}

// needsReview reports whether a workflow is worth the app's review call.
// A note and a remark stay inside the persona's own corner and can be corrected
// by the next post; schedules, forgetting and tools reach further. A persona
// that can schedule is reviewed even for a note and a remark: the failure it
// is prone to is a time promised in the remark with no schedule behind it,
// and that workflow has nothing but a note and a remark in it. Staying quiet
// has nothing to check.
func needsReview(workflow Workflow, schedules bool) bool {
	quiet := true
	for _, step := range workflow.Steps {
		if step.Type != "write_note" && step.Type != "speak" {
			return true
		}
		if step.Type != "speak" || strings.TrimSpace(step.Text) != "" {
			quiet = false
		}
	}
	return schedules && !quiet
}

// routingModel matches a post against short persona conditions. Production
// replay showed Super to be much faster than Lightning with better handling
// of overlapping conditions. Jev can replace it through advanced settings.
func routingModel() string {
	return store.NemotronSuper
}

// readAll does what a persona asked to look at. Nothing here writes, so there
// is nothing to review; a schedule is not something to read with a tool.
func (r *Runner) readAll(ctx context.Context, skill Skill, steps []ReadStep, set *tools.Set, post timeline.Entry,
	prov *provenance, failures map[string]string, budget *lookups, cited map[string]string) (text string, sources []timeline.Source) {

	var parts []string
	for _, step := range steps {
		// A note read written as a tool call is the same request in the wrong
		// field. A live run spent reads being refused for it.
		if step.Type == "tool" && (step.Tool == "notes_read" || step.Tool == "notes_list") {
			step.Type, step.Tool = step.Tool, ""
		}
		var out string
		name := step.Type
		switch step.Type {
		case "notes_list":
			args, _ := argsOf(step.Args)
			out, _ = r.notesTool(skill.ID, "notes_list", args, prov)
		case "notes_read":
			var paths []any
			for _, path := range step.Paths {
				paths = append(paths, path)
			}
			out, _ = r.notesTool(skill.ID, "notes_read", map[string]any{"paths": paths}, prov)
		case "tool":
			name = step.Tool
			args, err := argsOf(step.Args)
			switch {
			case err != nil:
				out = "args を JSON オブジェクトとして読めません: " + err.Error()
			case step.Tool == "schedule":
				out = "予定は読むものではありません。置いている予定は最初に渡した一覧にあります。置く・消すは workflow の write_schedule / cancel_schedule で。"
			default:
				// The same call failing the same way again is worth saying
				// outright: a live run spent six of its reads on one broken
				// search and never tried the other tool it held.
				call := step.Tool + " " + strings.TrimSpace(step.Args)
				if answer, ok := budget.take(step.Tool, step.Args); !ok {
					// Asked before, or asked too often: answered without a call.
					out = answer
					break
				}
				result, err := set.Run(ctx, step.Tool, args, post.Place)
				out = result.Text
				if err == nil {
					budget.keep(step.Tool, step.Args, out)
				}
				if err != nil {
					out = "道具が使えませんでした: " + err.Error()
					if failures[call] == err.Error() {
						out += "\n**同じ呼び出しは前にも同じ理由で失敗しています。繰り返さず、別の道具か別の引数にするか、読むのをやめて workflow を出してください。**"
					}
					failures[call] = err.Error()
				}
				sources = append(sources, result.Sources...)
				for _, post := range result.Citable {
					cited[post.ID] = post.Label
				}
			}
		case "image":
			// None of the persona models take a picture. The one read the app
			// did on arrival - what is in it, its text, where it seems to come
			// from, how it looks - is in the post already, and that is all
			// there is.
			out = "元画像は渡せません（担当のモデルは画像を見られません）。投稿に付いている読み取り結果（説明・文字・出典）を使ってください。"
		default:
			out = "知らない読み方です: " + step.Type
		}
		parts = append(parts, name+" の結果:\n"+out)
	}
	if len(parts) == 0 {
		return "何も読みませんでした。", sources
	}
	return strings.Join(parts, "\n\n"), sources
}

var placedID = regexp.MustCompile(`schedule (s_[A-Za-z0-9]+) を置きました`)

// execute runs an approved workflow in order and stops at the first step that
// fails, so a remark at the end never goes out when what it rests on did not
// happen.
func (r *Runner) execute(ctx context.Context, skill Skill, workflow Workflow, set *tools.Set, rules turnRules, post timeline.Entry,
	prov *provenance) (spoken string, sources []timeline.Source, records []stepRecord) {

	// What the workflow throws away is known before anything is written, so a
	// note that takes a forgotten one's place inherits its posts either way.
	for _, step := range workflow.Steps {
		if step.Type == "forget_note" {
			prov.noteForgotten(step.Path)
		}
	}
	for _, step := range workflow.Steps {
		record := stepRecord{Type: step.Type, Tool: step.Tool, Path: step.Path}
		var out string
		switch step.Type {
		case "write_note":
			out, record.Error = r.writeNote(skill, step.Path, step.Text, prov)
		case "forget_note":
			out, _ = r.notesTool(skill.ID, "notes_forget", map[string]any{"path": step.Path}, prov)
			if !strings.HasPrefix(out, "捨てました") {
				record.Error = out
			}
		case "write_schedule":
			out, record.Error = r.writeSchedule(ctx, skill, step, set, prov)
		case "cancel_schedule":
			result, err := set.Run(ctx, "schedule", map[string]any{"action": "cancel", "id": step.ID}, nil)
			out = result.Text
			if err != nil {
				record.Error = err.Error()
			}
		case "tool":
			// Checked again here: the review is advice, the declaration is the rule.
			if step.Tool == "schedule" {
				record.Error = "予定は write_schedule / cancel_schedule で扱います"
				break
			}
			args, err := argsOf(step.Args)
			if err != nil {
				record.Error = err.Error()
				break
			}
			result, err := set.Run(ctx, step.Tool, args, post.Place)
			out = result.Text
			if err != nil {
				record.Error = err.Error()
			}
			sources = append(sources, result.Sources...)
		case "speak":
			// A listening turn's speak is empty by rule; whatever it holds is
			// not said.
			if !rules.listening {
				spoken = strings.TrimSpace(step.Text)
				sources = append(sources, citedSources(step.Sources, rules.cited)...)
			}
			out = spoken
		default:
			record.Error = "知らないステップです"
		}
		record.Output = preview(out)
		records = append(records, record)
		if record.Error != "" {
			return "", sources, records
		}
	}
	return spoken, sources, records
}

// citedSources are the links a speak's sources become: the posts it named,
// as this turn's searches showed them. An id no search returned is dropped;
// the check that sends the workflow back for it runs before this.
func citedSources(ids []string, cited map[string]string) []timeline.Source {
	var out []timeline.Source
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if label, ok := cited[id]; ok {
			out = append(out, timeline.Source{Label: label, URL: "/?entry=" + url.QueryEscape(id)})
		}
	}
	return out
}

func (r *Runner) writeNote(skill Skill, path, text string, prov *provenance) (string, string) {
	out, _ := r.notesTool(skill.ID, "notes_write", map[string]any{"path": path, "text": text}, prov)
	if !strings.HasPrefix(out, "書きました") {
		return out, out
	}
	return out, ""
}

// writeSchedule places a wake and writes the note it rests on, as one step. The
// two live in different places - the app's records and the persona's notes -
// and the failure this exists to prevent is having one without the other: a
// note that says 22:30 with nothing set to wake at 22:30. So the schedule goes
// first, and if the note cannot be written the schedule is taken back.
func (r *Runner) writeSchedule(ctx context.Context, skill Skill, step Step, set *tools.Set, prov *provenance) (string, string) {
	result, err := set.Run(ctx, "schedule", map[string]any{
		"action": "set", "at": strings.TrimSpace(step.At), "daily": step.Daily,
		"note": step.Note, "note_path": step.Path,
	}, nil)
	if err != nil {
		return result.Text, err.Error()
	}
	written, failure := r.writeNote(skill, step.Path, step.Text, prov)
	if failure != "" {
		if match := placedID.FindStringSubmatch(result.Text); match != nil {
			_, _ = set.Run(ctx, "schedule", map[string]any{"action": "cancel", "id": match[1]}, nil)
		}
		return written, "メモが書けなかったので予定も取り消しました: " + failure
	}
	return result.Text + "\n" + written, ""
}

// scheduleListFor is the persona's own schedules, handed over up front. They
// are the app's records, not notes, and a persona cannot plan a cancel without
// knowing the ids it would cancel.
func (r *Runner) scheduleListFor(skill Skill, set *tools.Set) (string, map[string]bool) {
	known := map[string]bool{}
	if !set.Allows("schedule") {
		return "（schedule は使えません）", known
	}
	schedules, err := r.store.Schedules(skill.ID)
	if err != nil {
		return "（読み出せませんでした）", known
	}
	if len(schedules) == 0 {
		return "（ありません）", known
	}
	var lines []string
	for _, schedule := range schedules {
		known[schedule.ID] = true
		repeat := "1回"
		if schedule.Daily {
			repeat = "毎日"
		}
		when := schedule.At
		if loc, err := time.LoadLocation(schedule.TimeZone); err == nil {
			when = when.In(loc)
		}
		line := fmt.Sprintf("- %s %s（%s）: %s", schedule.ID, when.Format("2006-01-02 15:04 MST"), repeat, schedule.Note)
		if schedule.NotePath != "" {
			line += "（根拠のメモ: " + schedule.NotePath + "）"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), known
}

// stepRecord is one step as it ran, for the log.
type stepRecord struct {
	Type   string `json:"type"`
	Tool   string `json:"tool,omitempty"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
}

// draftRecord is one workflow a persona handed in and what became of it.
type draftRecord struct {
	Workflow *Workflow `json:"workflow,omitempty"`
	Problems []string  `json:"problems,omitempty"`
	Review   *Review   `json:"review,omitempty"`
	Unseen   string    `json:"reviewUnavailable,omitempty"`
}

// readRecord is one round of reading.
type readRecord struct {
	Steps  []ReadStep `json:"steps"`
	Output string     `json:"output"`
}

// runRecord is everything about one turn, written as a single log line so a
// turn can be read back in the shape it had.
type runRecord struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Persona  string `json:"persona"`
	Trigger  string `json:"trigger,omitempty"`
	Wake     string `json:"wake"`
	// Path is "workflow".
	Path     string       `json:"path"`
	Model    string       `json:"model"`
	Reviewer string       `json:"reviewer"`
	Reads    []readRecord `json:"reads,omitempty"`
	// Stalls are replies that called nothing, which use up a turn as surely as
	// a read does and were invisible in the log.
	Stalls int           `json:"stalls,omitempty"`
	Drafts []draftRecord `json:"drafts"`
	Ran    bool          `json:"ran"`
	Steps  []stepRecord  `json:"steps,omitempty"`
	Spoke  string        `json:"spoke,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// logRun writes the record as JSON on stdout, which Cloud Run keeps as a
// structured entry rather than a line of text.
func logRun(record runRecord) {
	record.Message = "workflow " + record.Persona
	record.Severity = "INFO"
	if record.Error != "" {
		record.Severity = "WARNING"
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(encoded))
}

func preview(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 300 {
		return string(runes[:300]) + "…"
	}
	return string(runes)
}

// clip keeps what a reviewer is shown to a size it can read.
func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…（以下略）"
	}
	return text
}
