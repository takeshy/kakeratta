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

// A turn reads, drafts an Answer, then assembles a state-changing plan one
// phase at a time. Review sees the side-effect phases only. Actions execute
// before the answer is published; a failure never publishes a promise whose
// action did not succeed.

// ReadStep is something a persona wants to look at before deciding.
type ReadStep struct {
	Type  string   `json:"type"`
	Paths []string `json:"paths"`
	Tool  string   `json:"tool"`
	Args  string   `json:"args"`
}

// Research offers structured arguments. Keep the internal/persisted string
// form and accept legacy callers so saved asks and workflows still resume.
func (s *ReadStep) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type  string          `json:"type"`
		Paths []string        `json:"paths"`
		Tool  string          `json:"tool"`
		Args  json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	args := strings.TrimSpace(string(wire.Args))
	switch {
	case args == "" || args == "null":
		args = ""
	case strings.HasPrefix(args, `"`):
		if err := json.Unmarshal(wire.Args, &args); err != nil {
			return err
		}
	case strings.HasPrefix(args, "{"):
		// Give structured calls a stable internal representation; existing
		// string arguments retain their historical representation.
		var object map[string]any
		if err := json.Unmarshal(wire.Args, &object); err != nil {
			return err
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return err
		}
		args = string(encoded)
	default:
		return fmt.Errorf("read の args は JSON オブジェクトで指定してください。")
	}
	*s = ReadStep{Type: wire.Type, Paths: wire.Paths, Tool: wire.Tool, Args: args}
	return nil
}

// operationBatch is a Workflow flattened into the order it runs in:
// schedules, then actions. It is the host's own adapter to the checks
// and the executor, never a shape a model is shown.
type operationBatch struct {
	Steps []operation
}

// operation is one thing to do. Which fields matter depends on the type; the rest
// are empty.
type operation struct {
	Type string `json:"type"`
	// tool.
	Tool string `json:"tool"`
	// Args is the tool's arguments as a JSON object written out as a string. A
	// free-form object in a schema is something a provider that constrains its
	// output tends to fill with {}; a string it leaves alone.
	Args string `json:"args"`
	// write_schedule.
	At            string `json:"at"`
	Daily         bool   `json:"daily"`
	Notify        *bool  `json:"notify,omitempty"`
	UserRequested bool   `json:"user_requested"`
	Note          string `json:"note"`
	// cancel_schedule.
	ID string `json:"id"`
}

const (
	// maxSteps keeps a workflow a plan rather than a program.
	maxSteps = 8
	// maxReads is how many times a persona may ask to look at something before
	// it has to decide - searches included. Reading changes nothing, so the
	// limit is only there to end a turn that would otherwise go on looking.
	maxReads = 10
	// Super research for deep/AlwaysUltra turns may look further.
	ultraReads = 20
	// maxSearches is how many of those reads may be web searches.
	maxSearches = 2
	// Deep/AlwaysUltra research may also search a little more.
	ultraSearches = 4
	// maxDrafts is how many workflows the review may see in one turn: the first,
	// and one more after the blamed phases were regenerated.
	maxDrafts = 2
	// maxMalformed is how many proposals a phase may have sent back for their
	// shape - a missing field, an unknown path - before the turn gives up.
	// These are counted apart from review drafts, so a shape slip does not use
	// up the chance to fix what the review found.
	maxMalformed = 2
	// maxPhaseRounds bounds one phase's model replies whatever they hold: every
	// read, and a few sent-back proposals and stalls besides.
	maxPhaseRounds = maxReads + 2*maxMalformed + 3
)

const (
	// readsRefused answers a read asked for after the budget was spent, and
	// readsLastNote closes the footer of the read that spent it. Both tell the
	// persona to submit; a phase also takes them as the cue to stop offering
	// read, since a model that has been told twice may still ask again.
	readsRefused  = "これ以上は読めません。現在のフェーズの結果を提出してください。"
	readsLastNote = "もう読めないので、現在のフェーズの結果を提出してください。"
)

func turnReadLimit(deep bool) int {
	if deep {
		return ultraReads
	}
	return maxReads
}

func turnSearchLimit(deep bool) int {
	if deep {
		return ultraSearches
	}
	return maxSearches
}

func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// Every field of a step is required, with empty values for the ones it does
// not use. With only "type" required, a provider constraining its output to the
// schema left out everything else: a live run handed in {"type":"notes_write"}.
// records reads the persona's own kakera records; see readRecords.
var readTool = llm.Tool{
	Name:        "read",
	Description: "決める前に見ておきたいものを読む。何も書き換えない。結果が返ってくる。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"steps": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{"type": "string", "enum": []any{"records", "tool"}},
				"tool": stringField("tool で呼ぶ道具の名前。records では \"\""),
				"args": stringField("引数を JSON オブジェクトとして書いた文字列。tool なら例: {\"query\":\"赤ワイン\"}。records なら {\"tag\":\"…\",\"query\":\"…\",\"before\":\"2026-09\"} のうち要るもの、または {\"ids\":[\"記録の[ ]のID\"]} で投稿そのもの（10件まで）"),
			},
			"required": []any{"type", "tool", "args"},
		}},
	}, "required": []any{"steps"}},
}

// stepShape is which kinds of change a persona's plan can hold, following what
// the persona holds: a persona that cannot schedule is offered no schedule
// fields, and one whose tools only look things up has no action phase at all.
type stepShape struct {
	schedules bool
	actions   []string
}

func shapeOf(set *tools.Set) stepShape {
	shape := stepShape{schedules: set.Allows("schedule")}
	for _, declared := range set.Declarations() {
		if declared.Name != "schedule" && !set.IsReadOnly(declared.Name) {
			shape.actions = append(shape.actions, declared.Name)
		}
	}
	return shape
}

// turnRules are what the app enforces on a workflow regardless of review.
type turnRules struct {
	named, listening bool
	set              *tools.Set
	// schedules contains all visible IDs; the value is whether this persona owns it.
	schedules map[string]bool
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

// readFooter is what goes under every read: how many are left, and what else
// the persona holds. A live run spent every read on one flaky map search and
// never tried the web search it also had.
func readFooter(left int, set *tools.Set) string {
	var names []string
	for _, declared := range set.Declarations() {
		if set.IsReadOnly(declared.Name) {
			names = append(names, declared.Name)
		}
	}
	footer := fmt.Sprintf("\n\n（read は残り %d 回。", left)
	if len(names) > 0 {
		footer += "調べるのに使える道具: " + strings.Join(names, " / ") + "。見つからない・使えないときは別の道具を試してください。"
	}
	if left <= 0 {
		footer += readsLastNote
	}
	return footer + "）"
}

// checkBatch reports what makes a plan unrunnable or not this persona's to
// run. It reads the plan, never the notes.
func checkBatch(workflow operationBatch, rules turnRules) []string {
	var problems []string
	if len(workflow.Steps) > maxSteps {
		problems = append(problems, fmt.Sprintf("ステップが多すぎます（%d件。上限は%d件）", len(workflow.Steps), maxSteps))
	}
	for i, step := range workflow.Steps {
		at := fmt.Sprintf("%d番目（%s）", i+1, step.Type)
		for _, text := range []string{step.Args, step.Note, step.ID} {
			for _, id := range scheduleIDRef.FindAllString(text, -1) {
				if _, exists := rules.schedules[id]; !exists {
					problems = append(problems, at+": "+id+" という schedule はありません。一覧にある id だけを使ってください")
				}
			}
		}
		switch step.Type {
		case "write_schedule":
			if !rules.set.Allows("schedule") {
				problems = append(problems, at+": あなたは schedule を使えません")
			}
			if _, err := time.Parse(time.RFC3339, strings.TrimSpace(step.At)); err != nil {
				problems = append(problems, at+": at は 2026-09-17T23:30:00+09:00 のような RFC3339 で書いてください")
			}
			if strings.TrimSpace(step.Note) == "" {
				problems = append(problems, at+": note（起きたときに実行する目的）がありません")
			}
		case "update_schedule":
			if !rules.set.Allows("schedule") || !rules.schedules[strings.TrimSpace(step.ID)] {
				problems = append(problems, at+": 自分の schedule だけを更新できます")
			}
			if step.At == "" && step.Notify == nil {
				problems = append(problems, at+": at または notify が必要です")
			}
			if step.At != "" {
				if parsed, err := time.Parse(time.RFC3339, step.At); err != nil || !parsed.After(time.Now()) {
					problems = append(problems, at+": at は未来のRFC3339時刻で指定してください")
				}
			}
		case "cancel_schedule":
			if !rules.set.Allows("schedule") {
				problems = append(problems, at+": あなたは schedule を使えません")
			}
			if !rules.schedules[strings.TrimSpace(step.ID)] {
				problems = append(problems, at+": 自分が置いた schedule の id だけを取り消せます")
			}
		case "tool":
			if _, err := argsOf(step.Args); err != nil {
				problems = append(problems, at+": args を JSON オブジェクトとして読めません")
			}
			switch {
			case step.Tool == "schedule":
				problems = append(problems, at+": 予定は tool ではなく write_schedule / update_schedule / cancel_schedule で扱います")
			case strings.HasPrefix(step.Tool, "notes_") || step.Tool == "records":
				problems = append(problems, at+": 記憶は本体がメモ係を通して保存します。Action では扱いません")
			case !rules.set.Allows(step.Tool):
				problems = append(problems, at+": "+step.Tool+" はあなたが使える道具ではありません")
			case rules.set.IsReadOnly(step.Tool):
				problems = append(problems, at+": "+step.Tool+" は調べるための道具です。Action ではなく read で使ってください")
			}

		default:
			problems = append(problems, at+": 知らないステップです")
		}
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
	Phase       string `json:"phase,omitempty"`
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
		switch issue.Phase {
		case "schedule", "action", "both":
		default:
			return fmt.Errorf("issues[%d] の phase は schedule / action / both で指定してください", i)
		}
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
	Description: "実行計画の確認結果を提出する。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"verdict": map[string]any{"type": "string", "enum": []any{"pass", "fail"}},
		"summary": stringField("判定の理由を一文で"),
		"issues": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"phase":       map[string]any{"type": "string", "enum": []string{"schedule", "action", "both"}},
				"severity":    map[string]any{"type": "string", "enum": []any{"high", "medium", "low"}},
				"description": map[string]any{"type": "string"},
			},
			"required": []any{"phase", "severity", "description"},
		}},
	}, "required": []any{"verdict", "summary", "issues"}},
}

// reviewPrompt is the app's own judgement, and it is narrow on purpose. Whether
// to speak at all, what to find interesting, how to keep notes - those are the
// persona's, and a reviewer with opinions about them would be a second persona
// wearing the app's name. What the app checks is whether the workflow says
// things that are not true and promises things it did not arrange.
const reviewPrompt = `あなたはアプリ本体です。実行計画のScheduleとActionのみを実行前に確認します。
Answerの品質・長さ・調査結果や記憶の更新はレビューしません。
次の場合だけhighとしてfailにする:
Push通知のON/OFFや予定の時刻変更がユーザーの依頼と矛盾する。notify:falseはPushのみ停止し、予定実行は続く。updateは既存予定の時刻・通知だけを変える。
- Answerの約束と予定の日時、作成・取消・変更が一致しない。
- user_requested:true の予定に、ユーザーによる時刻・繰り返しを伴う実行依頼または既存のユーザー依頼予定の裏付けがない。
- 複数の予定やActionが矛盾し、または相互依存を満たさない。
- ユーザーの依頼・担当の権限とActionの対象・引数が一致しない。
- 状態変更を完了したというAnswerに操作または既存状態の裏付けがない。
外部調査の出典が内部readにないという理由でAnswerを却下しない。
問題にはphaseをschedule/action/bothのどれかで指定する。Answerは再生成しない。
highがなければpass。submit_reviewで提出する。`

// review asks the app's own model about one assembled plan. A malformed verdict is
// returned as a tool result, giving the reviewer another turn to correct it.
// An endpoint error remains unavailable to the caller, which records it.
func (r *Runner) review(ctx context.Context, client *llm.Client, settings store.Settings, skill Skill, thread []timeline.Entry, trigger timeline.Entry,
	answer Answer, plan Workflow, known string, names map[string]string, here *time.Location) (*Review, error) {

	model := reviewModel(settings)
	encoded, _ := json.MarshalIndent(struct {
		Schedules []ScheduleOperation `json:"schedules"`
		Actions   []ActionOperation   `json:"actions"`
	}{plan.Schedules, plan.Actions}, "", "  ")
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
		"完成した Answer（調査の出典は本文中のリンクも参照。これはメモへの保存指示ではありません）:",
		answer.Text,
		"担当が提出した状態変更計画（schedules / actions）:",
		string(encoded),
	}, "\n")
	system := reviewPrompt
	messages := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	for attempt := 0; attempt < 3; attempt++ {
		reply, err := client.Complete(ctx, llm.Request{
			Purpose: "review", Attempt: attempt + 1, Model: model, Tools: []llm.Tool{reviewTool}, ToolChoice: "required", Messages: messages,
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

// reviewModel checks plans for unsafe or inconsistent actions. It uses the
// middle slot - Super by default, or the Middle model chosen for OpenRouter -
// and stays there even when a persona answers on Ultra.
func reviewModel(settings store.Settings) string {
	return personaModel(settings)
}

// routingModel matches a post against short persona conditions. Production
// replay showed Super to be much faster than Lightning with better handling
// of overlapping conditions. Jev can replace it through advanced settings.
func routingModel() string {
	return store.NemotronSuper
}

// readAll does what a persona asked to look at. Nothing here writes, so there
// is nothing to review; schedules_list reads schedules without changing them.
func (r *Runner) readAll(ctx context.Context, skill Skill, steps []ReadStep, set *tools.Set, post timeline.Entry,
	zone *time.Location, failures map[string]string, budget *lookups, cited map[string]string, referenced map[string]bool) (text string, sources []timeline.Source) {

	var parts []string
	for _, step := range steps {
		// A records read written as a tool call is the same request in the
		// wrong field. A live run spent reads being refused for it.
		if step.Type == "tool" && step.Tool == "records" {
			step.Type, step.Tool = step.Tool, ""
		}
		var out string
		name := step.Type
		switch step.Type {
		case "records", "entries":
			args, err := argsOf(step.Args)
			if err != nil {
				out = "args を JSON オブジェクトとして読めません: " + err.Error()
				break
			}
			if step.Type == "entries" && len(entryIDs(args)) == 0 {
				out = "entries には投稿IDを ids で指定してください。"
				break
			}
			if !skill.Remembers() && len(entryIDs(args)) == 0 {
				out = "この担当は記憶を使いません。"
				break
			}
			var ids []string
			out, ids = r.readRecords(ctx, skill.ID, args, zone)
			// What was read is in use; its dates move once the turn is done.
			for _, id := range ids {
				if referenced != nil {
					referenced[id] = true
				}
			}
			// A post read by its record's id may be cited like one a search found.
			if len(entryIDs(args)) > 0 {
				for _, id := range ids {
					if entry, ok := r.store.Entry(id); ok && !entry.Deleted {
						cited[entry.ID] = tools.CiteLabel(entry, zone)
					}
				}
			}
		case "tool":
			name = step.Tool
			args, err := argsOf(step.Args)
			switch {
			case err != nil:
				out = "args を JSON オブジェクトとして読めません: " + err.Error()
			case step.Tool == "schedule":
				out = "自分の予定は最初に渡した一覧にあります。他の担当の予定は閲覧専用の schedules_list で確認してください。作成・更新・取消は後の Schedule フェーズで扱います。"
			case !set.IsReadOnly(step.Tool):
				out = "この道具は状態を変えるため read では実行できません。後の Action フェーズで扱います。"
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
						out += "\n**同じ呼び出しは前にも同じ理由で失敗しています。繰り返さず、別の道具か別の引数にするか、読むのをやめて現在のフェーズの結果を提出してください。**"
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
func (r *Runner) execute(ctx context.Context, skill Skill, workflow operationBatch, set *tools.Set, rules turnRules, post timeline.Entry,
) (sources []timeline.Source, records []stepRecord) {

	for _, step := range workflow.Steps {
		record := stepRecord{Type: step.Type, Tool: step.Tool}
		var out string
		switch step.Type {
		case "write_schedule":
			result, err := set.Run(ctx, "schedule", map[string]any{
				"action": "set", "at": strings.TrimSpace(step.At), "notify": notificationValue(step.Notify), "daily": step.Daily, "user_requested": step.UserRequested, "note": step.Note,
			}, nil)
			out = result.Text
			if err != nil {
				record.Error = err.Error()
			}
		case "update_schedule":
			args := map[string]any{"action": "update", "id": step.ID, "at": step.At}
			if step.Notify != nil {
				args["notify"] = *step.Notify
			}
			result, err := set.Run(ctx, "schedule", args, nil)
			out = result.Text
			if err != nil {
				record.Error = err.Error()
			}
		case "cancel_schedule":
			result, err := set.Run(ctx, "schedule", map[string]any{"action": "cancel", "id": step.ID}, nil)
			out = result.Text
			if err != nil {
				record.Error = err.Error()
			}
		case "tool":
			// Checked again here: the review is advice, the declaration is the rule.
			if step.Tool == "schedule" {
				record.Error = "予定は write_schedule / update_schedule / cancel_schedule で扱います"
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

		default:
			record.Error = "知らないステップです"
		}
		record.Output = preview(out)
		records = append(records, record)
		if record.Error != "" {
			return sources, records
		}
	}
	return sources, records
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

// scheduleListFor hands over only the persona's own schedules. Other owners
// remain in the validation map, but their schedules are read through schedules_list.
func (r *Runner) scheduleListFor(skill Skill, set *tools.Set, names map[string]string) (string, map[string]bool) {
	known := map[string]bool{}
	if !set.Allows("schedule") {
		return "（schedule は使えません）", known
	}
	schedules, err := r.store.Schedules("")
	if err != nil {
		return "（読み出せませんでした）", known
	}

	var lines []string
	for _, schedule := range schedules {
		known[schedule.ID] = schedule.Skill == skill.ID
		if schedule.Skill != skill.ID {
			continue
		}
		repeat := "1回"
		if schedule.PersonEvent != nil && schedule.PersonEvent.Annual {
			repeat = "毎年"
		}
		if schedule.Daily {
			repeat = "毎日"
		}
		when := schedule.At
		if loc, err := time.LoadLocation(schedule.TimeZone); err == nil {
			when = when.In(loc)
		}
		owner := schedule.Skill
		if name := names[schedule.Skill]; name != "" {
			owner = fmt.Sprintf("%s（%s）", name, schedule.Skill)
		}
		line := fmt.Sprintf("- %s %s（%s・担当: %s）: %s", schedule.ID, when.Format("2006-01-02 15:04 MST"), repeat, owner, schedule.Note)
		line += fmt.Sprintf("（user_requested:%t, notify:%t）", schedule.UserRequested, schedule.NotificationsEnabled())
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "（ありません）", known
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
	Path     string `json:"path"`
	Model    string `json:"model"`
	Reviewer string `json:"reviewer"`
	Deep     *bool  `json:"deep,omitempty"`
	// Planner is "external" when an external agent planned the turn, or
	// "external→builtin" when it fell through, with the reason in Fallback.
	Planner  string `json:"planner,omitempty"`
	Fallback string `json:"fallback,omitempty"`
	// Asked is the ask a turn ended with, to be carried on once it is over.
	Asked string       `json:"asked,omitempty"`
	Reads []readRecord `json:"reads,omitempty"`
	// Stalls are replies that called nothing, which use up a turn as surely as
	// a read does and were invisible in the log.
	Stalls  int            `json:"stalls,omitempty"`
	Answer  *Answer        `json:"answer,omitempty"`
	Phases  []phaseRecord  `json:"phases,omitempty"`
	Reviews []reviewRecord `json:"reviews,omitempty"`
	Plan    *Workflow      `json:"plan,omitempty"`
	Ran     bool           `json:"ran"`
	Steps   []stepRecord   `json:"steps,omitempty"`
	Spoke   string         `json:"spoke,omitempty"`
	Error   string         `json:"error,omitempty"`
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

func notificationValue(value *bool) bool { return value == nil || *value }
