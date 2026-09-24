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
	"web_search":   `{"query":"神谷町 ランチ 営業時間"}`,
	"place_search": `{"query":"ラーメン","radius":800}`,
	// No mode: a live michi copied "foot" from here into every route it asked
	// for. The means belongs to the post, and the args table lists the choices.
	"route":           `{"to":"東京タワー"}`,
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

// sampleStep writes one workflow step with exactly the fields this persona's
// schema has, so a sample never shows a field the persona cannot send.
func sampleStep(shape stepShape, values map[string]any) string {
	defaults := map[string]any{"type": "", "path": "", "text": "", "sources": []any{}, "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""}
	var parts []string
	for _, field := range shape.fields() {
		value, ok := values[field]
		if !ok {
			value = defaults[field]
		}
		encoded, _ := json.Marshal(value)
		parts = append(parts, `"`+field+`":`+string(encoded))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// workflowSpec is the whole of what a persona is told about working, for the
// tools this persona holds.
// speakLength is what a remark's text is told about its length. A reply that
// asked for a better or deeper answer is not held to "短く", and neither is
// one to a persona the user named: it was asked something directly.
func speakLength(deep, named bool) string {
	if deep {
		return "求められた深さに見合う長さで、Markdown で。"
	}
	if named {
		return "聞かれたことに答えるのに必要な長さで。**強調**・箇条書き・見出しなどの Markdown が使え、そのままの見た目で表示されます。短く済むなら装飾は要りません。"
	}
	return "短く。**強調**・箇条書き・見出しなどの Markdown が使え、そのままの見た目で表示されます。短く済むなら装飾は要りません。"
}

func workflowSpec(set *tools.Set, deep, named bool) string {
	shape := shapeOf(set)
	var lookups, actions []tools.Declaration
	for _, declared := range set.Declarations() {
		switch {
		case declared.Name == "schedule":
		case lookupTools[declared.Name]:
			lookups = append(lookups, declared)
		default:
			lookups = append(lookups, declared)
			actions = append(actions, declared)
		}
	}
	step := func(values map[string]any) string { return sampleStep(shape, values) }
	fence := func(body string) string { return "```json\n" + body + "\n```\n" }

	var b strings.Builder
	b.WriteString(`# workflow の仕様

このターンは2つに分かれます。

1. **read で読む**（必要なら。` + fmt.Sprint(turnReadLimit(deep)) + ` 回まで）。メモを読む・調べる。何も書き換えません。本体が実行して結果を返します。` + searchLimitLine(set, deep) + `
2. **submit_workflow で workflow を出す**。やること（メモを書く・` + map[bool]string{true: "予定を置く・", false: ""}[shape.schedules] + `発言する）を並べます。
   本体が確認してから、上から順に実行します。**失敗したステップがあれば、そこで止まります。**

**調べるのは read だけです。** workflow には調べるステップはありません。調べ終わってから、決めたことだけを workflow に書きます。

本体は workflow の文面をそのまま書き込み・発言します。あとから埋まる部分はありません。

## 全体の決まり

- **workflow の各ステップには、次の項目をすべて書きます:** ` + strings.Join(shape.fields(), ", ") + `。使わない項目は ""・[]` + map[bool]string{true: "・false", false: ""}[shape.schedules] + map[bool]string{true: `・"{}"`, false: ""}[len(shape.actions) > 0] + ` にします。
- **text には、書き込まれる・発言される文面を最後まで書きます。**
- **起きていないことを書きません。** 「これから見る」は予定であって、「見た」「済」ではありません。
- **調べた結果にも投稿にもメモにも無いことを、事実として書きません。**
- 既存のメモを直すときは、先に read で読んでから、直した**全文**を書きます（index.md は最初から渡してあるので読み直さなくて構いません）。
- **1つの workflow で同じメモを2回書きません。**

❌ 項目が欠けている（本体が読めず差し戻されます）
` + fence(`{"type":"write_note"}`) + `
---

## 1. read のステップ

read の形:
` + fence(`{"steps":[ {"type":"…","paths":[],"tool":"","args":"{}"} ]}`) + `1回の read に複数のステップを並べられます。

### notes_list
自分のメモの一覧を、更新が新しい順に100件ずつ出します。一覧には各メモの tags が出ます。args に {"tag":"…"} を書くと、そのタグのメモだけになります。
続きがあるときは一覧の最後に次の args（{"after":"…"}）が出ます。メモを探す入口は index.md です。notes_list はタグで横断して探すときに使います。
` + fence(`{"type":"notes_list","paths":[],"tool":"","args":"{}"}`) + fence(`{"type":"notes_list","paths":[],"tool":"","args":"{\"tag\":\"アルゼンチン\"}"}`) + `
### notes_read
paths のメモの中身を読みます。
` + fence(`{"type":"notes_read","paths":["tastes/tonkotsu.md","shops/jiro-mita.md"],"tool":"","args":"{}"}`) + `❌ メモを tool で読む
` + fence(`{"type":"tool","paths":["index.md"],"tool":"notes_read","args":"{}"}`))

	if len(lookups) > 0 {
		b.WriteString(`
### tool（調べる）
下の道具で調べます。**道具が使えない・見つからないときは、同じ呼び出しを繰り返さず、別の道具か別の引数で試します。**
`)
		for _, declared := range lookups {
			b.WriteString("\n#### " + declared.Name + "\n" + declared.Description + "\n\nargs:\n" + argsTable(declared) + "\n\n")
			b.WriteString(fence(`{"type":"tool","paths":[],"tool":"` + declared.Name + `","args":` + jsonString(sampleArgs(declared)) + `}`))
		}
	} else {
		b.WriteString("\n（あなたが調べるために使える道具はありません。read では notes_list / notes_read だけを使います）\n")
	}

	b.WriteString(`
---

## 2. workflow のステップ

使えるステップ: ` + strings.Join(func() []string {
		var out []string
		for _, t := range shape.types() {
			out = append(out, fmt.Sprint(t))
		}
		return out
	}(), " / ") + `

submit_workflow の形:
` + fence(`{"summary":"何をするかを一文で","steps":[ `+step(map[string]any{"type": "…"})+` ]}`) + `何もしないなら steps を空にします。
` + fence(`{"summary":"言うことも覚えることも無い","steps":[]}`) + `
### write_note
path のメモを text で丸ごと書きます。他のメモは変わりません。
- **path** — tastes/tonkotsu.md のような path
- **text** — メモの全文（OKF の書き方で）
` + fence(step(map[string]any{"type": "write_note", "path": "tastes/tonkotsu.md", "text": "---\ntype: Preference\ntitle: 豚骨に寄る\ndescription: 選べるときは豚骨を選んでいる。\n---\n\n# 根拠\n\n* 9/17 豚骨を選んだ（本人の投稿）"})) + `❌ 起きていないことを書く（「22時半からドラマを見る」は予定です）
` + fence(step(map[string]any{"type": "write_note", "path": "errands/drama.md", "text": "---\ntype: Errand\n---\n\n2026-09-16 22:21 済"})) + `
### forget_note
path のメモを捨てます。
` + fence(step(map[string]any{"type": "forget_note", "path": "shops/closed-shop.md"})))

	if shape.schedules {
		b.WriteString(`
### write_schedule
**予定と、その根拠のメモを1つのステップで作ります。** 予定だけ・メモだけにはなりません。本体は先に予定を置き、メモが書けなければ予定を取り消します。
起きたときは、済んでいるかを確かめて、まだなら軽くひとこと言うためのものです。やる前に急かすためではありません。
- **at** — 起きる時刻。RFC3339。30日以内。**その用が済んでいるはずの時刻**にします
- **daily** — 毎日繰り返すなら true
- **note** — 起きたときに自分が読む、何のためかの一言
- **path** / **text** — 根拠のメモ。既にあるメモなら、読んでから直した全文
` + fence(step(map[string]any{"type": "write_schedule", "path": "errands/drama.md", "text": "---\ntype: Errand\ntitle: ドラマ\ndescription: 9/18 21:00から見ると言っていた\n---\n\n* 9/18 21:00から見る予定（本人の投稿）", "at": "2026-09-18T22:00:00+09:00", "note": "ドラマを見たか"})) + `毎日:
` + fence(step(map[string]any{"type": "write_schedule", "path": "routines/medicine.md", "text": "---\ntype: Routine\ntitle: 尿酸値の薬\ndescription: 毎朝8:00に飲む\n---\n\n* 毎朝8:00に飲む（本人の投稿）", "at": "2026-09-18T08:30:00+09:00", "daily": true, "note": "朝の薬"})) + `❌ 予定とメモを別々のステップに分ける／メモを write_note でも書く
❌ 開始時刻ちょうどに置く（「21時から見る」なら、見終わっているはずの時刻に置きます）

### cancel_schedule
予定を消します。id は「あなたが置いている schedule」の一覧にあるものだけです。
` + fence(step(map[string]any{"type": "cancel_schedule", "id": "s_17895801754983143966fb18de8"})) + `❌ 一覧に無い id（s_xxx など）
`)
	}

	if len(actions) > 0 {
		var names []string
		for _, declared := range actions {
			names = append(names, declared.Name)
		}
		b.WriteString(`
### tool
道具を呼びます（` + strings.Join(names, " / ") + `）。**呼ぶこと自体が用であるときだけ**です。調べるだけなら read で行います。
` + fence(step(map[string]any{"type": "tool", "tool": actions[0].Name, "args": sampleArgs(actions[0])})))
	}

	b.WriteString(`
### speak
発言します。**必ず1つ、最後に入れます。** 黙って終わるなら text を空（""）にします。黙るかどうかは、あなたが毎回はっきり決めることです。
**投稿に発言するなら、その根拠や覚えたことを write_note でも残します。** 発言だけでメモが無い workflow は差し戻されます。黙ってメモだけ残すのは構いません。
- **text** — 発言そのもの。投稿と同じ言語で、` + speakLength(deep, named) + `何も言わないなら ""
- **sources** — 発言の根拠にした過去の投稿の id。**このターンの timeline_search の結果にある id だけ**を、特に重要なものから最大3件書けます。発言の下に、その投稿へのリンクが付きます。使わなければ []
` + fence(step(map[string]any{"type": "speak", "text": "また豚骨だ。前の二郎もそうだったね", "sources": []any{"e_（timeline_search の結果の id）"}})) + `❌ 投稿と違う言語で話す（日本語の投稿に "Acknowledged."）
❌ 希望を事実にする（「9:10に着きたい」に「9:10に着きます」）
❌ 投稿に無い条件を足す（「ランチどこがいい?」に「1時間後に着くなら」）
❌ 作業の説明を発言にする（「神谷町駅周辺のランチ情報をWeb検索で探す」）。調べ終わってから、分かったことを話します
`)
	if shape.schedules {
		b.WriteString("❌ 時刻を約束するのに write_schedule が無い\n")
	}

	b.WriteString("\n---\n\n## 例\n")
	if shape.schedules {
		b.WriteString(`
### 時刻のある用を覚えて、済んだころに聞く
投稿: 「明日の21時からドラマを見る」
` + fence(`{"summary":"ドラマの予定を覚え、見終わったころに起きる","steps":[
 `+step(map[string]any{"type": "write_schedule", "path": "errands/drama.md", "text": "---\ntype: Errand\ntitle: ドラマ\ndescription: 9/18 21:00から見ると言っていた\n---\n\n* 9/18 21:00から見る予定（本人の投稿）", "at": "2026-09-18T22:00:00+09:00", "note": "ドラマを見たか"})+`,
 `+step(map[string]any{"type": "speak", "text": "見終わったころに聞くね"})+`]}`) + `
### 済んだと分かったので、記録して予定を消す
投稿: 「ドラマ見た」（一覧に s_1789… 「ドラマを見たか」がある。先に read で errands/drama.md を読んだ）
` + fence(`{"summary":"見たことを記録して、予定を消す","steps":[
 `+step(map[string]any{"type": "write_note", "path": "errands/drama.md", "text": "（読んだ全文に「* 9/18 見た（本人の投稿）」を足したもの）"})+`,
 `+step(map[string]any{"type": "cancel_schedule", "id": "s_1789…"})+`]}`))
	}
	if len(lookups) > 0 {
		first := lookups[0]
		for _, declared := range lookups {
			if declared.Name == "web_search" {
				first = declared
			}
		}
		b.WriteString(`
### 調べてから言う
read:
` + fence(`{"steps":[{"type":"tool","paths":[],"tool":"`+first.Name+`","args":`+jsonString(sampleArgs(first))+`}]}`) + `結果を見てから（workflow には調べるステップを入れません）:
` + fence(`{"summary":"調べた結果をひとこと","steps":[
 `+step(map[string]any{"type": "speak", "text": "（結果に書いてあったことだけを使った発言）"})+`]}`))
	}
	b.WriteString(`
### 読んで、書き足して、ひとこと
read:
` + fence(`{"steps":[{"type":"notes_read","paths":["tastes/tonkotsu.md"],"tool":"","args":"{}"}]}`) + `結果を見てから:
` + fence(`{"summary":"豚骨の好みに今回を足して、ひとこと","steps":[
 `+step(map[string]any{"type": "write_note", "path": "tastes/tonkotsu.md", "text": "（読んだ全文に、今回の根拠を1行足したもの）"})+`,
 `+step(map[string]any{"type": "speak", "text": "また豚骨だ"})+`]}`) + `
### 覚えるだけで、何も言わない
` + fence(`{"summary":"好みを記録するだけ","steps":[
 `+step(map[string]any{"type": "write_note", "path": "tastes/light.md", "text": "---\ntype: Preference\ntitle: 朝は軽め\ndescription: 朝は軽いものを選ぶ。\n---\n\n* 9/17 朝にうどん（本人の投稿）"})+`]}`))
	return b.String()
}

// searchLimitLine tells a persona with web_search how many searches it has,
// so it words the first query to count rather than finding out at the limit.
func searchLimitLine(set *tools.Set, deep bool) string {
	if !set.Allows("web_search") {
		return ""
	}
	return "\n   web_search はこのうち " + fmt.Sprint(turnSearchLimit(deep)) + " 回までです。同じ語句で検索し直さず、1回目の結果で足りるよう語句を選んでください。"
}
