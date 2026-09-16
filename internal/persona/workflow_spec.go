package persona

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/takeshy/kakeratta/internal/tools"
)

// The workflow spec is what a persona is told about how to work, written the
// way gemihub writes its own: every step type with its fields, a sample that is
// right, and the mistake that was actually made. It is assembled for the tools
// a persona holds, so a persona without schedule is not taught write_schedule
// and a persona with web_search is shown a search call it can copy.
//
// The mistakes are not invented. Each ❌ below is something a live run handed
// in: a step with only "type", a schedule split across two steps, "済" for
// something that had not happened yet.

// toolSamples are argument objects for the built-in tools, written out the way
// a step carries them.
var toolSamples = map[string]string{
	"web_search":      `{"query":"神谷町 ランチ 営業時間"}`,
	"place_search":    `{"query":"ラーメン","radius":800}`,
	"route":           `{"to":"神谷町駅","mode":"foot"}`,
	"timeline_search": `{"query":"尿酸値の薬","limit":5}`,
}

// sampleArgs is a tool's sample arguments: the built-in one, or one made from
// the required properties of the schema an MCP server gave.
func sampleArgs(declared tools.Declaration) string {
	if sample, ok := toolSamples[declared.Name]; ok {
		return sample
	}
	properties, _ := declared.Parameters["properties"].(map[string]any)
	required := map[string]bool{}
	if list, ok := declared.Parameters["required"].([]any); ok {
		for _, name := range list {
			if text, ok := name.(string); ok {
				required[text] = true
			}
		}
	}
	sample := map[string]any{}
	for name, raw := range properties {
		if len(required) > 0 && !required[name] {
			continue
		}
		property, _ := raw.(map[string]any)
		switch property["type"] {
		case "number", "integer":
			sample[name] = 1
		case "boolean":
			sample[name] = true
		case "array":
			sample[name] = []any{}
		case "object":
			sample[name] = map[string]any{}
		default:
			if enum, ok := property["enum"].([]any); ok && len(enum) > 0 {
				sample[name] = enum[0]
			} else {
				sample[name] = "…"
			}
		}
	}
	encoded, _ := json.Marshal(sample)
	return string(encoded)
}

// argsTable lists a tool's arguments from its schema.
func argsTable(declared tools.Declaration) string {
	properties, _ := declared.Parameters["properties"].(map[string]any)
	if len(properties) == 0 {
		return "  引数はありません。args は \"{}\"。"
	}
	required := map[string]bool{}
	if list, ok := declared.Parameters["required"].([]any); ok {
		for _, name := range list {
			if text, ok := name.(string); ok {
				required[text] = true
			}
		}
	}
	var names []string
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		property, _ := properties[name].(map[string]any)
		kind, _ := property["type"].(string)
		line := "  - **" + name + "**"
		if required[name] {
			line += "（必須）"
		}
		if kind != "" {
			line += " " + kind
		}
		if description, ok := property["description"].(string); ok && description != "" {
			line += " — " + description
		}
		if enum, ok := property["enum"].([]any); ok && len(enum) > 0 {
			var values []string
			for _, value := range enum {
				values = append(values, fmt.Sprint(value))
			}
			line += "（" + strings.Join(values, " / ") + "）"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// jsonString writes a JSON object as the string a step's args field holds.
func jsonString(object string) string {
	encoded, _ := json.Marshal(object)
	return string(encoded)
}

// workflowSpec is the whole of what a persona is told about working, for the
// tools this persona holds.
func workflowSpec(set *tools.Set) string {
	var searchTools []tools.Declaration
	schedules := false
	for _, declared := range set.Declarations() {
		if declared.Name == "schedule" {
			schedules = true
			continue
		}
		searchTools = append(searchTools, declared)
	}

	var b strings.Builder
	b.WriteString(`# workflow の仕様

このターンは2つに分かれます。

1. **read で読む**（必要なら。` + fmt.Sprint(maxReads) + ` 回まで）。何も書き換えません。本体が実行して結果を返します。
2. **submit_workflow で workflow を出す**（1回。差し戻されたら直して、もう1回だけ）。
   本体が確認してから、上から順に実行します。**失敗したステップがあれば、そこで止まります。**

本体は workflow の文面をそのまま書き込み・発言します。あとから埋まる部分はありません。

## 全体の決まり

- **各ステップのすべての項目を書きます。** 使わない項目は ""・[]・false・"{}" にします。
- **text には、書き込まれる・発言される文面を最後まで書きます。**
- **起きていないことを書きません。** 「これから見る」は予定であって、「見た」「済」ではありません。
- **id は渡された一覧にあるものだけ。** s_xxx のような仮の id は書きません。
- 道具の args は、JSON オブジェクトを**文字列にして**書きます（例: ` + jsonString(`{"query":"赤ワイン"}`) + `）。
- 既存のメモを直すときは、先に read で読んでから、直した**全文**を書きます（index.md は最初から渡してあるので読み直さなくて構いません）。
- **1つの workflow で同じメモを2回書きません。** 1つのメモは1つのステップで書きます。

❌ 項目が欠けている（本体が読めず差し戻されます）
` + "```json" + `
{"type":"write_note"}
` + "```" + `

---

## 1. read のステップ

read の形:
` + "```json" + `
{"steps":[ {"type":"…","paths":[],"tool":"","args":"{}"} ]}
` + "```" + `
1回の read に複数のステップを並べられます。

### notes_list
自分のメモの一覧を取り直します。
` + "```json" + `
{"type":"notes_list","paths":[],"tool":"","args":"{}"}
` + "```" + `

### notes_read
paths のメモの中身を読みます。
` + "```json" + `
{"type":"notes_read","paths":["tastes/tonkotsu.md","shops/jiro-mita.md"],"tool":"","args":"{}"}
` + "```" + `

### image
投稿の元画像を見ます（画像の付いた投稿だけ）。読み取った文字だけでは足りないときに使います。
` + "```json" + `
{"type":"image","paths":[],"tool":"","args":"{}"}
` + "```" + `
`)

	if len(searchTools) > 0 {
		b.WriteString(`
### tool（調べる）
下の道具で調べます。結果は次の返事で届きます。
**道具が使えなかったら、同じ呼び出しを繰り返さないでください。** 別の道具（持っているなら web_search など）か別の引数で試すか、読むのをやめて workflow を出します。
❌ メモを tool で読む（メモは type を notes_read にします）
` + "```json" + `
{"type":"tool","paths":["index.md"],"tool":"notes_read","args":"{}"}
` + "```" + `
`)
		for _, declared := range searchTools {
			b.WriteString("\n#### " + declared.Name + "\n")
			b.WriteString(declared.Description + "\n\n")
			b.WriteString("args:\n" + argsTable(declared) + "\n\n")
			b.WriteString("```json\n")
			b.WriteString(`{"type":"tool","paths":[],"tool":"` + declared.Name + `","args":` + jsonString(sampleArgs(declared)) + "}\n")
			b.WriteString("```\n")
		}
	} else {
		b.WriteString("\n（あなたが調べるために使える道具はありません。read では notes_list / notes_read / image だけを使います）\n")
	}

	b.WriteString(`
---

## 2. workflow のステップ

submit_workflow の形:
` + "```json" + `
{"summary":"何をするかを一文で","steps":[ {"type":"…","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""} ]}
` + "```" + `
何もしないなら steps を空にします。
` + "```json" + `
{"summary":"言うことも覚えることも無い","steps":[]}
` + "```" + `

### write_note
path のメモを text で丸ごと書きます。他のメモは変わりません。
- **path**（必須）— tastes/tonkotsu.md のような path
- **text**（必須）— メモの全文（OKF の書き方で）
` + "```json" + `
{"type":"write_note","path":"tastes/tonkotsu.md","text":"---\ntype: Preference\ntitle: 豚骨に寄る\ndescription: 選べるときは豚骨を選んでいる。\n---\n\n# 根拠\n\n* 9/17 豚骨を選んだ（本人の投稿）","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}
` + "```" + `
❌ 起きていないことを書く
` + "```json" + `
{"type":"write_note","path":"errands/drama.md","text":"---\ntype: Errand\n---\n\n2026-09-16 22:21 済", …}
` + "```" + `
「22時半からドラマを見る」は、これから見るという予定です。済んだとは書きません。

### forget_note
path のメモを捨てます。
- **path**（必須）
` + "```json" + `
{"type":"forget_note","path":"shops/closed-shop.md","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}
` + "```" + `
`)

	if schedules {
		b.WriteString(`
### write_schedule
**予定と、その根拠のメモを1つのステップで作ります。** 予定だけ・メモだけにはなりません。
本体は先に予定を置き、メモが書けなければ予定を取り消します。
起きたときは、済んでいるかを確かめて、まだなら軽くひとこと言うためのものです。やる前に急かすためではありません。
- **at**（必須）— 起きる時刻。RFC3339（2026-09-18T22:00:00+09:00）。30日以内。**その用が済んでいるはずの時刻**にします
- **daily** — 毎日繰り返すなら true
- **note**（必須）— 起きたときに自分が読む、何のためかの一言
- **path**（必須）/ **text**（必須）— 根拠のメモ。既にあるメモなら、読んでから直した全文
` + "```json" + `
{"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\ntitle: ドラマ\ndescription: 9/18 21:00から見ると言っていた\n---\n\n* 9/18 21:00から見る予定（本人の投稿）","tool":"","args":"{}","at":"2026-09-18T22:00:00+09:00","daily":false,"note":"ドラマを見たか","id":""}
` + "```" + `
毎日:
` + "```json" + `
{"type":"write_schedule","path":"routines/medicine.md","text":"---\ntype: Routine\ntitle: 尿酸値の薬\ndescription: 毎朝8:00に飲む\n---\n\n* 毎朝8:00に飲む（本人の投稿）","tool":"","args":"{}","at":"2026-09-18T08:30:00+09:00","daily":true,"note":"朝の薬","id":""}
` + "```" + `
❌ 予定とメモを別々のステップに分ける（片方が欠けて差し戻されます）
` + "```json" + `
{"type":"write_schedule","path":"routines/medicine.md","text":"…","at":"",…},
{"type":"write_schedule","path":"","text":"","at":"2026-09-18T08:00:00+09:00",…}
` + "```" + `
❌ 開始時刻ちょうどに置く（「21時から見る」なら、見終わっているはずの時刻に置きます）
❌ write_note と write_schedule で同じメモを2回書く（write_schedule がメモも書きます）
` + "```json" + `
{"type":"write_note","path":"errands/drama.md","text":"…",…},
{"type":"write_schedule","path":"errands/drama.md","text":"…","at":"2026-09-18T22:00:00+09:00",…}
` + "```" + `

### cancel_schedule
予定を消します。id は「あなたが置いている schedule」の一覧にあるものだけです。
済んだと分かったとき、やめたとき、根拠のメモを捨てるときに使います。
- **id**（必須）
` + "```json" + `
{"type":"cancel_schedule","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":"s_17895801754983143966fb18de8"}
` + "```" + `
❌ 一覧に無い id（s_xxx など）
`)
	}

	if len(searchTools) > 0 {
		var names []string
		for _, declared := range searchTools {
			names = append(names, declared.Name)
		}
		b.WriteString(`
### tool
道具を呼びます（` + strings.Join(names, " / ") + `）。調べるだけなら read で行ってください。workflow に入れるのは、呼ぶこと自体が用であるときだけです。
- **tool**（必須）/ **args**（必須）— read の tool と同じ
`)
	}
	if schedules {
		b.WriteString("❌ schedule を tool で呼ぶ（予定は write_schedule / cancel_schedule で扱います）\n")
	}

	b.WriteString(`
### speak
発言します。**入れるなら1つだけ、必ず最後に。** 入れなければ黙って終わります。
- **text**（必須）— 発言そのもの。投稿と同じ言語で、短く
` + "```json" + `
{"type":"speak","path":"","text":"また豚骨だ。前の二郎もそうだったね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}
` + "```" + `
❌ 投稿と違う言語で話す（日本語の投稿に "Acknowledged."）
❌ 希望を事実にする（「9:10に着きたい」に「9:10に着きます」）
`)
	if schedules {
		b.WriteString("❌ 時刻を約束するのに write_schedule が無い（「22時ごろに聞くね」と言うなら、同じ workflow に write_schedule を入れます）\n")
	}

	b.WriteString("\n---\n\n## 例\n")
	if schedules {
		b.WriteString(`
### 時刻のある用を覚えて、済んだころに聞く
投稿: 「明日の21時からドラマを見る」
` + "```json" + `
{"summary":"ドラマの予定を覚え、見終わったころに起きる","steps":[
 {"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\ntitle: ドラマ\ndescription: 9/18 21:00から見ると言っていた\n---\n\n* 9/18 21:00から見る予定（本人の投稿）","tool":"","args":"{}","at":"2026-09-18T22:00:00+09:00","daily":false,"note":"ドラマを見たか","id":""},
 {"type":"speak","path":"","text":"見終わったころに聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}
` + "```" + `

### 済んだと分かったので、予定を消して記録する
投稿: 「ドラマ見た」（一覧に s_1789… 「ドラマを見たか」がある。先に read で errands/drama.md を読んだ）
` + "```json" + `
{"summary":"見たことを記録して、予定を消す","steps":[
 {"type":"write_note","path":"errands/drama.md","text":"---\ntype: Errand\ntitle: ドラマ\ndescription: 9/18 21:00から見ると言っていた\n---\n\n* 9/18 21:00から見る予定（本人の投稿）\n* 9/18 見た（本人の投稿）","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
 {"type":"cancel_schedule","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":"s_1789…"}]}
` + "```" + `
`)
	}
	if len(searchTools) > 0 {
		first := searchTools[0]
		b.WriteString(`
### 調べてから言う
read:
` + "```json" + `
{"steps":[{"type":"tool","paths":[],"tool":"` + first.Name + `","args":` + jsonString(sampleArgs(first)) + `}]}
` + "```" + `
結果を見てから:
` + "```json" + `
{"summary":"調べた結果をひとこと","steps":[
 {"type":"speak","path":"","text":"（結果に書いてあったことだけを使った発言）","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}
` + "```" + `
結果に無いことは言いません。
`)
	}
	b.WriteString(`
### 読んで、書き足して、ひとこと
read:
` + "```json" + `
{"steps":[{"type":"notes_read","paths":["tastes/tonkotsu.md"],"tool":"","args":"{}"}]}
` + "```" + `
結果を見てから:
` + "```json" + `
{"summary":"豚骨の好みに今回を足して、ひとこと","steps":[
 {"type":"write_note","path":"tastes/tonkotsu.md","text":"（読んだ全文に、今回の根拠を1行足したもの）","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
 {"type":"speak","path":"","text":"また豚骨だ","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}
` + "```" + `

### 覚えるだけで、何も言わない
` + "```json" + `
{"summary":"好みを記録するだけ","steps":[
 {"type":"write_note","path":"tastes/light.md","text":"---\ntype: Preference\ntitle: 朝は軽め\ndescription: 朝は軽いものを選ぶ。\n---\n\n* 9/17 朝にうどん（本人の投稿）","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}
` + "```" + `
`)
	return b.String()
}
