package persona

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// The low tier works without a workflow.
//
// A workflow is worth its cost when a persona can do something outside - a
// wake it asks the app for, a map it relies on - because that is where a wrong
// call does something. A low persona can only look: its own notes, and a web
// search, which is reading in the same way. Then it writes its notes and says
// something, or does not. The structure of read-then-workflow was also where
// the small model stumbled, handing in steps that were nothing but a type. So
// the low tier calls these directly, one at a time, and ends the turn by
// speaking or staying quiet.
//
// What it gives up is the review. A low persona can still write a note that
// says something happened when it did not; that is the price of the tier being
// light.

// lightRounds is how many requests a low turn may make: as many looks as the
// workflow path allows, and room to write and to finish.
const lightRounds = maxReads + 2

func objectOf(properties map[string]any, required ...string) map[string]any {
	listed := make([]any, 0, len(required))
	for _, name := range required {
		listed = append(listed, name)
	}
	return map[string]any{"type": "object", "properties": properties, "required": listed}
}

var (
	lightNotesRead = llm.Tool{Name: "notes_read", Description: "自分のメモの中身を読む。", Parameters: objectOf(map[string]any{
		"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "読むメモの path"},
	}, "paths")}
	lightWriteNote = llm.Tool{Name: "write_note", Description: "メモを1件、全文で書く。そのファイルだけが差し替わる。", Parameters: objectOf(map[string]any{
		"path": stringField("tastes/tonkotsu.md のような path"),
		"text": stringField("メモの全文"),
	}, "path", "text")}
	lightForgetNote = llm.Tool{Name: "forget_note", Description: "メモを1件捨てる。", Parameters: objectOf(map[string]any{
		"path": stringField("捨てるメモの path"),
	}, "path")}
	lightSpeak = llm.Tool{Name: "speak", Description: "発言してターンを終える。投稿と同じ言語で、短く。", Parameters: objectOf(map[string]any{
		"text": stringField("発言そのもの"),
	}, "text")}
	lightSilent = llm.Tool{Name: "silent", Description: "何も言わずにターンを終える。書いたメモは残る。", Parameters: objectOf(map[string]any{})}
)

// lightSpec is what a low persona is told about working.
func lightSpec(set *tools.Set) string {
	search := ""
	if set.Allows("web_search") {
		search = "\n- `web_search` — Web を検索する（メモを読むのと同じ、調べるだけの道具）\n  {\"query\":\"神谷町 ランチ 営業時間\"}\n  結果に書いてあることだけを使います。見つからなければ、見つからなかったと言うか黙ります。"
	}
	return `# このターンの進め方

使えるのは、自分のメモの読み書きと、発言だけです` + map[bool]string{true: "（調べるには web_search）", false: ""}[search != ""] + `。
投稿と、スレッドと、自分のメモ` + map[bool]string{true: "と、検索結果", false: ""}[search != ""] + `に書いてあることだけを使ってください。

1回の返事で呼べるものを呼び、結果を見て続けます。**最後は必ず speak か silent で終えます。**

- ` + "`notes_read`" + ` — メモの中身を読む
  {"paths":["tastes/tonkotsu.md"]}` + search + `
- ` + "`write_note`" + ` — メモを全文で書く（既にあるメモなら、読んでから直した全文）
  {"path":"tastes/tonkotsu.md","text":"---\ntype: Preference\ntitle: 豚骨に寄る\ndescription: 選べるときは豚骨を選んでいる。\n---\n\n* 9/17 豚骨を選んだ（本人の投稿）"}
- ` + "`forget_note`" + ` — メモを捨てる
  {"path":"shops/closed-shop.md"}
- ` + "`speak`" + ` — 発言して終える
  {"text":"また豚骨だ"}
- ` + "`silent`" + ` — 何も言わずに終える
  {}

決まり:
- **起きていないことを書きません。** 「これから見る」は予定であって、「見た」「済」ではありません。
- **メモにも投稿にも` + map[bool]string{true: "検索結果にも", false: ""}[search != ""] + `無いことを、事実として言いません。** 店名・場所・時間などを思いつきで言い切らないでください。
- 発言は投稿と同じ言語で、短く。投稿に文章がない（画像だけなど）ときは、担当条件と指示が書かれている言語で。`
}

// converse runs a low persona's turn: notes and a remark, called directly.
func (r *Runner) converse(ctx context.Context, skill Skill, messages []llm.Message, named, listening bool,
	set *tools.Set, post timeline.Entry, client *llm.Client, model string, memory store.Memory, root string, trigger timeline.Entry,
	noteSources *[]store.NoteSource, record *runRecord, failed func(error) *timeline.Entry) *timeline.Entry {

	record.Path = "light"
	record.Reviewer = ""
	record.Ran = true
	offered := []llm.Tool{lightNotesRead}
	for _, declared := range set.Declarations() {
		if lowTools[declared.Name] {
			offered = append(offered, llm.Tool{Name: declared.Name, Description: declared.Description, Parameters: declared.Parameters})
		}
	}
	offered = append(offered, lightWriteNote, lightForgetNote)
	if !listening {
		offered = append(offered, lightSpeak)
	}
	offered = append(offered, lightSilent)
	var sources []timeline.Source
	failures := map[string]string{}
	budget := newLookups(maxReads)
	refused := false
	for round := 0; round < lightRounds; round++ {
		reply, err := client.Complete(ctx, llm.Request{Model: model, Messages: messages, Tools: offered, ToolChoice: "required"})
		if err != nil {
			return failed(err)
		}
		if len(reply.Calls) == 0 {
			record.Stalls++
			messages = append(messages,
				llm.Message{Role: "assistant", Content: reply.Content},
				llm.Message{Role: "user", Content: "道具を呼んでください。最後は speak か silent で終えます。"})
			continue
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: reply.Content, Calls: reply.Calls})
		for _, call := range reply.Calls {
			var args map[string]any
			if json.Unmarshal([]byte(call.Args), &args) != nil || args == nil {
				args = map[string]any{}
			}
			step := stepRecord{Type: call.Name, Path: stringOf(args["path"])}
			var out string
			switch call.Name {
			case "notes_read":
				out, _ = r.notesTool(skill.ID, "notes_read", args, noteSources)
			case "write_note":
				out, _ = r.notesTool(skill.ID, "notes_write", args, noteSources)
				if !strings.HasPrefix(out, "書きました") {
					step.Error = out
				}
			case "forget_note":
				out, _ = r.notesTool(skill.ID, "notes_forget", args, noteSources)
				if !strings.HasPrefix(out, "捨てました") {
					step.Error = out
				}
			case "speak":
				text := stringOf(args["text"])
				switch {
				case listening:
					out = "このターンは発言できません。silent で終えてください。"
					step.Error = out
				case text == "":
					out = "text が空です。"
					step.Error = out
				default:
					step.Output = preview(text)
					record.Steps = append(record.Steps, step)
					record.Spoke = text
					return r.speak(skill, root, text, sources, memory, trigger.ID)
				}
			case "silent":
				if named && !listening && !refused {
					// Being asked by name is the one thing that is not the
					// persona's to decline.
					refused = true
					out = "あなたは名指しで呼ばれています。silent は選べません。短くて構わないので speak してください。"
					step.Error = out
					break
				}
				record.Steps = append(record.Steps, step)
				if named && !listening {
					return failed(errors.New("名指しに答えませんでした"))
				}
				return r.hush(skill, memory)
			default:
				if !lowTools[call.Name] || !set.Allows(call.Name) {
					out = call.Name + " は使えません。"
					step.Error = out
					break
				}
				step.Tool = call.Name
				if answer, ok := budget.take(call.Name, call.Args); !ok {
					// Asked before, or asked too often: answered without a call.
					out = answer
					break
				}
				result, err := set.Run(ctx, call.Name, args, post.Place)
				out = result.Text
				sources = append(sources, result.Sources...)
				if err == nil {
					budget.keep(call.Name, call.Args, out)
				}
				if err != nil {
					out = "道具が使えませんでした: " + err.Error()
					step.Error = err.Error()
					key := call.Name + " " + call.Args
					if failures[key] == err.Error() {
						out += "\n**同じ呼び出しは前にも同じ理由で失敗しています。繰り返さないでください。**"
					}
					failures[key] = err.Error()
				}
			}
			step.Output = preview(out)
			record.Steps = append(record.Steps, step)
			messages = append(messages, llm.Message{Role: "tool", CallID: call.ID, Content: out})
		}
	}
	return failed(errors.New("speak も silent もしないまま回数を使い切りました"))
}
