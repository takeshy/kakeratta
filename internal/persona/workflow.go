package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
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
}

const (
	// maxSteps keeps a workflow a plan rather than a program.
	maxSteps = 8
	// maxReads is how many times a persona may ask to look at something before
	// it has to decide.
	maxReads = 3
	// maxDrafts is how many workflows a persona may hand in for one turn: the
	// first, and one more after being told what was wrong with it.
	maxDrafts = 2
)

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
				"type":  map[string]any{"type": "string", "enum": []any{"notes_list", "notes_read", "tool", "image"}},
				"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "notes_read で読むメモ。他は []"},
				"tool":  stringField("tool で呼ぶ道具の名前。他は \"\""),
				"args":  stringField("tool の引数を JSON オブジェクトとして書いた文字列。例: {\"query\":\"赤ワイン\"}。他は \"{}\""),
			},
			"required": []any{"type", "paths", "tool", "args"},
		}},
	}, "required": []any{"steps"}},
}

var workflowTool = llm.Tool{
	Name:        "submit_workflow",
	Description: "このターンでやることを、順に並べた workflow として提出する。本体が確認してから実行する。何もしないなら steps を空にする。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"summary": stringField("この workflow で何をするかを一文で"),
		"steps": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":  map[string]any{"type": "string", "enum": []any{"write_note", "forget_note", "write_schedule", "cancel_schedule", "tool", "speak"}},
				"path":  stringField("write_note / forget_note / write_schedule のメモの path。他は \"\""),
				"text":  stringField("write_note / write_schedule のメモの全文、または speak の発言。他は \"\""),
				"tool":  stringField("tool で呼ぶ道具の名前。他は \"\""),
				"args":  stringField("tool の引数を JSON オブジェクトとして書いた文字列。他は \"{}\""),
				"at":    stringField("write_schedule で起きる時刻。RFC3339（2026-09-17T23:30:00+09:00）。他は \"\""),
				"daily": map[string]any{"type": "boolean", "description": "write_schedule を毎日繰り返すか。他は false"},
				"note":  stringField("write_schedule で起きたときに読む、何のためかの一言。他は \"\""),
				"id":    stringField("cancel_schedule で消す schedule の id。他は \"\""),
			},
			"required": []any{"type", "path", "text", "tool", "args", "at", "daily", "note", "id"},
		}},
	}, "required": []any{"summary", "steps"}},
}

// workflowGuide is how a persona is told to work.
func workflowGuide(set *tools.Set) string {
	var listed []string
	for _, declared := range set.Declarations() {
		if declared.Name == "schedule" {
			continue
		}
		line := "- `" + declared.Name + "` — " + declared.Description
		if properties, ok := declared.Parameters["properties"].(map[string]any); ok && len(properties) > 0 {
			var names []string
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			var args []string
			for _, name := range names {
				described := name
				if property, ok := properties[name].(map[string]any); ok {
					if text, ok := property["description"].(string); ok && text != "" {
						described += "（" + text + "）"
					}
				}
				args = append(args, described)
			}
			line += "\n  args: " + strings.Join(args, " / ")
		}
		listed = append(listed, line)
	}
	toolList := "（ありません）"
	if len(listed) > 0 {
		toolList = strings.Join(listed, "\n")
	}
	schedules := "（あなたは schedule を使えません。write_schedule / cancel_schedule は入れられません）"
	if set.Allows("schedule") {
		schedules = `- ` + "`write_schedule`" + ` — **予定と、その根拠のメモを一緒に作る。** at に起きる時刻、daily に毎日繰り返すか、note に起きたとき読む一言、
  path と text に根拠のメモ（全文）。予定だけ・メモだけにはなりません。メモが書けなければ予定も取り消されます。
  30日以内の時刻だけ置けます。起きたときに、済んでいるか確かめて、まだなら軽くひとことです。
- ` + "`cancel_schedule`" + ` — id の予定を消す。id は下に渡す一覧にあるものだけ`
	}
	return `## このターンの進め方

**1. 読む（必要なら）。** read で、メモの一覧・メモの中身・検索などを頼みます。何も書き換えません。結果が返ってきます。
   - ` + "`notes_list`" + ` — 自分のメモの一覧
   - ` + "`notes_read`" + ` — paths のメモの中身
   - ` + "`tool`" + ` — 下の道具で調べる
   - ` + "`image`" + ` — 投稿の元画像を見る
   読む必要が無ければ、いきなり 2 に進んで構いません。read は ` + fmt.Sprint(maxReads) + ` 回までです。

**2. workflow を出す。** submit_workflow で、やることを上から順に並べます。本体が確認してから、その通りに実行します。
- ` + "`write_note`" + ` — path に text を書く（そのファイルだけ丸ごと差し替え）
- ` + "`forget_note`" + ` — path のメモを捨てる
` + schedules + `
- ` + "`tool`" + ` — 下の道具を呼ぶ
- ` + "`speak`" + ` — text を発言する。**入れるなら最後に1つだけ。** 入れなければ黙って終わる
何もしないなら steps を空にします。

書き方:
- **text には、そのまま書き込まれる・発言される文面を最後まで書きます。** あとから埋まる部分はありません。
- 既存のメモを直すなら、先に read で読み、直した全文を text に書きます。
- **起きていないことを書かないでください。** 「これからドラマを見る」は予定です。見た・済んだとは書きません。
- 時刻を約束する発言（「22:30ごろに聞くね」）をするなら、同じ workflow に write_schedule を入れます。
- id は、渡された一覧にあるものだけを使います。s_xxx のような仮の id は書かないでください。
- steps は上から順に実行され、**失敗したステップがあればそこで止まります。** 後ろのステップ（発言など）は行われません。
- 各ステップのすべての項目を書き、使わない項目は ""・false・"{}" にします。

例（ドラマの予定を覚えて、見終わったころに聞く）:
submit_workflow: {"summary":"ドラマの予定を覚え、終わるころに起きる","steps":[
 {"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\ntitle: ドラマ\ndescription: 9/17 22:30から見ると言っていた\n---\n\n* 9/17 22:30から見る予定（本人の投稿）","tool":"","args":"{}","at":"2026-09-17T23:30:00+09:00","daily":false,"note":"ドラマを見たか","id":""},
 {"type":"speak","path":"","text":"見終わったころに聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}

あなたが tool で使える道具:
` + toolList
}

// turnRules are what the app enforces on a workflow regardless of review.
type turnRules struct {
	named, listening bool
	set              *tools.Set
	schedules        map[string]bool
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

// checkWorkflow reports what makes a workflow unrunnable or not this persona's
// to run. It reads the workflow, never the notes.
func checkWorkflow(workflow Workflow, rules turnRules) []string {
	var problems []string
	if len(workflow.Steps) > maxSteps {
		problems = append(problems, fmt.Sprintf("ステップが多すぎます（%d件。上限は%d件）", len(workflow.Steps), maxSteps))
	}
	speaks := 0
	for i, step := range workflow.Steps {
		at := fmt.Sprintf("%d番目（%s）", i+1, step.Type)
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
			}
		case "speak":
			speaks++
			if strings.TrimSpace(step.Text) == "" {
				problems = append(problems, at+": text がありません")
			}
			if i != len(workflow.Steps)-1 {
				problems = append(problems, at+": speak は最後のステップにしてください")
			}
		default:
			problems = append(problems, at+": 知らないステップです")
		}
	}
	switch {
	case speaks > 1:
		problems = append(problems, "speak は1回までです")
	case rules.listening && speaks > 0:
		problems = append(problems, "このターンは発言できません。speak を外してください")
	case rules.named && !rules.listening && speaks == 0:
		problems = append(problems, "名指しで呼ばれています。最後に speak を入れてください")
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
2. **ありもしない id を使っている。** 渡された schedule 一覧に無い id。
3. **時刻を約束しているのに、それを置いていない。** 「22:30ごろに聞くね」と発言するのに、write_schedule が無い。
4. **担当自身への指示にはっきり反している。** 例: 薬について催促する、飲み忘れと決めつける。
5. **投稿と違う言語で発言している。** 日本語の投稿に英語で答える、など。発言は投稿と同じ言語でするのが規則です。
6. **既存のメモを読まずに丸ごと書き換えている。** メモ一覧にある path への write_note / write_schedule なのに、担当がそのメモを読んでいない（中身が消える）。

medium / low（実行してよい。verdict は pass）:
- 発言が長い、同じことを繰り返している

high が無ければ verdict は pass です。迷ったら pass にしてください。
submit_review を呼んで答えてください。`

// review asks the app's own model about one workflow. An error means no
// verdict could be had; the caller runs the workflow anyway and the log says it
// was not reviewed, because a check that is down should not silence everyone.
func (r *Runner) review(ctx context.Context, client *llm.Client, settings store.Settings, skill Skill, thread []timeline.Entry, trigger timeline.Entry,
	workflow Workflow, known string, names map[string]string, here *time.Location) (*Review, error) {

	model := harnessModel(settings)
	if model == "" {
		return nil, errors.New("本体のモデルが設定されていません")
	}
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
	reply, err := client.Complete(ctx, llm.Request{
		Model: model, Tools: []llm.Tool{reviewTool}, ToolChoice: "required",
		Messages: []llm.Message{{Role: "system", Content: reviewPrompt}, {Role: "user", Content: user}},
	})
	if err != nil {
		return nil, err
	}
	raw := firstJSON(reply.Content)
	for _, call := range reply.Calls {
		if call.Name == reviewTool.Name {
			raw = call.Args
		}
	}
	var verdict Review
	if err := json.Unmarshal([]byte(raw), &verdict); err != nil || (verdict.Verdict != "pass" && verdict.Verdict != "fail") {
		return nil, fmt.Errorf("確認結果を読めませんでした: %q", raw)
	}
	return &verdict, nil
}

// harnessModel is the app's own judgement - routing and review - and it is the
// best model configured, because the app is the one place a wrong call reaches
// everyone.
func harnessModel(settings store.Settings) string {
	return modelFor(TierHigh, settings)
}

// readAll does what a persona asked to look at. Nothing here writes, so there
// is nothing to review; a schedule is not something to read with a tool.
func (r *Runner) readAll(ctx context.Context, skill Skill, steps []ReadStep, set *tools.Set, post timeline.Entry,
	noteSources *[]store.NoteSource) (text string, sources []timeline.Source, files []llm.Attachment) {

	var parts []string
	for _, step := range steps {
		var out string
		name := step.Type
		switch step.Type {
		case "notes_list":
			out, _ = r.notesTool(skill.ID, "notes_list", nil, noteSources)
		case "notes_read":
			var paths []any
			for _, path := range step.Paths {
				paths = append(paths, path)
			}
			out, _ = r.notesTool(skill.ID, "notes_read", map[string]any{"paths": paths}, noteSources)
		case "tool":
			name = step.Tool
			args, err := argsOf(step.Args)
			switch {
			case err != nil:
				out = "args を JSON オブジェクトとして読めません: " + err.Error()
			case step.Tool == "schedule":
				out = "予定は読むものではありません。置いている予定は最初に渡した一覧にあります。置く・消すは workflow の write_schedule / cancel_schedule で。"
			default:
				result, err := set.Run(ctx, step.Tool, args, post.Place)
				out = result.Text
				if err != nil {
					out = "道具が使えませんでした: " + err.Error()
				}
				sources = append(sources, result.Sources...)
			}
		case "image":
			if post.Media == nil || post.Media.BlobID == "" {
				out = "この投稿に画像はありません。"
			} else if data, mime, err := r.store.Blob(post.Media.BlobID); err != nil {
				out = "画像を読み出せませんでした。"
			} else {
				files = []llm.Attachment{{Name: "capture", MIME: mime, Data: data}}
				out = "元の画像を次に添えます。"
			}
		default:
			out = "知らない読み方です: " + step.Type
		}
		parts = append(parts, name+" の結果:\n"+out)
	}
	if len(parts) == 0 {
		return "何も読みませんでした。", sources, files
	}
	return strings.Join(parts, "\n\n"), sources, files
}

var placedID = regexp.MustCompile(`schedule (s_[A-Za-z0-9]+) を置きました`)

// execute runs an approved workflow in order and stops at the first step that
// fails, so a remark at the end never goes out when what it rests on did not
// happen.
func (r *Runner) execute(ctx context.Context, skill Skill, workflow Workflow, set *tools.Set, rules turnRules, post timeline.Entry,
	noteSources *[]store.NoteSource) (spoken string, sources []timeline.Source, records []stepRecord) {

	for _, step := range workflow.Steps {
		record := stepRecord{Type: step.Type, Tool: step.Tool, Path: step.Path}
		var out string
		switch step.Type {
		case "write_note":
			out, record.Error = r.writeNote(skill, step.Path, step.Text, noteSources)
		case "forget_note":
			out, _ = r.notesTool(skill.ID, "notes_forget", map[string]any{"path": step.Path}, noteSources)
			if !strings.HasPrefix(out, "捨てました") {
				record.Error = out
			}
		case "write_schedule":
			out, record.Error = r.writeSchedule(ctx, skill, step, set, noteSources)
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
			if rules.listening {
				record.Error = "このターンは発言できません"
				break
			}
			spoken = strings.TrimSpace(step.Text)
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

func (r *Runner) writeNote(skill Skill, path, text string, noteSources *[]store.NoteSource) (string, string) {
	out, _ := r.notesTool(skill.ID, "notes_write", map[string]any{"path": path, "text": text}, noteSources)
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
func (r *Runner) writeSchedule(ctx context.Context, skill Skill, step Step, set *tools.Set, noteSources *[]store.NoteSource) (string, string) {
	result, err := set.Run(ctx, "schedule", map[string]any{
		"action": "set", "at": strings.TrimSpace(step.At), "daily": step.Daily,
		"note": step.Note, "note_path": step.Path,
	}, nil)
	if err != nil {
		return result.Text, err.Error()
	}
	written, failure := r.writeNote(skill, step.Path, step.Text, noteSources)
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
	Severity string        `json:"severity"`
	Message  string        `json:"message"`
	Persona  string        `json:"persona"`
	Trigger  string        `json:"trigger,omitempty"`
	Wake     string        `json:"wake"`
	Model    string        `json:"model"`
	Reviewer string        `json:"reviewer"`
	Reads    []readRecord  `json:"reads,omitempty"`
	Drafts   []draftRecord `json:"drafts"`
	Ran      bool          `json:"ran"`
	Steps    []stepRecord  `json:"steps,omitempty"`
	Spoke    string        `json:"spoke,omitempty"`
	Error    string        `json:"error,omitempty"`
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
