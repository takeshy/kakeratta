package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Routing is what the app decides, and the only thing it decides about a post.
//
// It exists so the number of personas can grow with the number of domains
// without every post waking all of them. It is a light judgement and nothing
// more: it drops what is plainly none of a persona's business and passes
// everything else on. Whether to actually say something is still the persona's
// call, made with the post in front of it.
//
// The bias is deliberate, within reason. A post delivered to a persona with
// nothing to say costs one call and ends in silence, so doubt should deliver.
// A post that fits nobody's domain still goes nowhere: that is the routing
// working, not failing.
const routePrompt = `投稿を、担当分野ごとのAIのうち誰に渡すかを振り分けます。

**既定は「渡す」です。** 外すのは、その担当の領域と明らかに無関係なときだけ。

規則:
- 渡した相手が必ず発言するわけではありません。黙るかどうかは相手が自分で決めます。
  あなたの仕事は関係者を選ぶことではなく、**明らかな無関係を落とすこと**だけです。迷ったら渡してください。
- **話題が担当の例に無いというだけで外さないでください。** 領域の説明は例であって、一覧ではありません。
  例: 「今日の20時30分からドラマを見る」は、ドラマが例に無くても、時刻の付いた予定を扱う担当へ渡します。
- どの担当の領域にも当てはまらない投稿なら、「誰にも渡さない」で構いません。それは正しい答えです。
- 渡されたスレッド全体（元の投稿、ユーザーの追加発言、担当の発言）を読んで、今回の対象発言の意味と担当を判断してください。
- 追加発言は単独の投稿ではありません。省略された目的・場所・時間はスレッド内の文脈で補い、ユーザーの訂正や追加条件を優先してください。
  例: 「渋谷にいきたい」の後の「1時間後。」は渋谷への移動の時間に関する補足なので、移動の担当へ渡します。
- 過去に担当が回答済みでも、新しい条件や質問があれば改めて関係する担当へ渡してください。

deliver を呼んで、渡す相手のidを渡してください。**括弧の中の表示名ではなく、左側のidです。**
誰にも渡さないときは空のまま呼びます。`

// deliverTool is the one call routing makes. Routing decides exactly one thing,
// so there is exactly one thing it can do.
var deliverTool = llm.Tool{
	Name:        "deliver",
	Description: "この投稿を渡す担当を決める。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"deliver": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "渡す担当のid。誰にも渡さないなら空",
		},
	}, "required": []any{"deliver"}},
}

// Route picks who a post goes to.
//
// When it cannot decide - no model configured, the call failed, the reply made
// no sense - everyone gets it. Routing is an optimisation, and an optimisation
// that fails should cost calls rather than swallow a post.
func Route(ctx context.Context, client *llm.Client, model string, skills []Skill, thread []Entryish, trigger Entryish) []string {
	if len(skills) == 0 {
		return nil
	}
	all := make([]string, 0, len(skills))
	for _, skill := range skills {
		all = append(all, skill.ID)
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return all
	}
	// Falling back is legitimate but it costs a call per persona, and it looked
	// exactly like a model choosing everyone. Now it says which it was.
	everyone := func(reason string) []string {
		log.Printf("routing: delivering to all %d personas because %s", len(all), reason)
		return all
	}
	var domains []string
	for _, skill := range skills {
		domains = append(domains, fmt.Sprintf("- %s（%s）: %s", skill.ID, skill.Name, skill.Domain))
	}
	reply, err := client.Complete(ctx, llm.Request{
		Model: model, Tools: []llm.Tool{deliverTool}, ToolChoice: "required",
		Messages: []llm.Message{
			{Role: "system", Content: routePrompt},
			{Role: "user", Content: strings.Join([]string{
				"担当:", strings.Join(domains, "\n"), "", "スレッド全体（古い順）:", renderAll(thread),
				"", "今回の対象発言（この発言をスレッド全体の文脈で解釈）:", trigger.Render(nil, nil),
			}, "\n")},
		},
	})
	if err != nil {
		return everyone("the routing call failed: " + err.Error())
	}
	var parsed struct {
		Deliver []string `json:"deliver"`
	}
	// The call is what this asks for, but a model that answered in prose has
	// still answered. Falling back to reading it costs nothing and is better
	// than waking everyone.
	chosen := ""
	for _, call := range reply.Calls {
		if call.Name == deliverTool.Name {
			chosen = call.Args
		}
	}
	if chosen == "" {
		chosen = firstJSON(reply.Content)
	}
	if err := json.Unmarshal([]byte(chosen), &parsed); err != nil || parsed.Deliver == nil {
		return everyone(fmt.Sprintf("the reply made no sense: %q", chosen))
	}
	// A router that answers "plana" when the id is "routine" has named the right
	// persona by the name a person sees. Taking only the id would throw the
	// answer away and wake everyone, which is how tabe kept turning up.
	resolve := func(want string) string {
		want = strings.TrimSpace(want)
		if want == "" {
			return ""
		}
		for _, skill := range skills {
			if skill.ID == want {
				return skill.ID
			}
		}
		for _, skill := range skills {
			if skill.Answers(want) {
				return skill.ID
			}
		}
		return ""
	}
	var out []string
	var invented []string
	seen := map[string]bool{}
	for _, named := range parsed.Deliver {
		id := resolve(named)
		if id == "" {
			invented = append(invented, named)
			continue
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	// One made-up name among real ones is not a reason to wake everyone; it is a
	// reason to ignore that one. Only an answer that resolved to nobody at all,
	// while asking for someone, means the reply could not be used.
	if len(invented) > 0 {
		log.Printf("routing: ignoring %d name(s) that match no persona: %v", len(invented), invented)
		if len(out) == 0 {
			return everyone(fmt.Sprintf("nothing it named exists: %v", invented))
		}
	}
	return out
}

// Entryish is the little of an entry that routing needs. It keeps this function
// testable without a store and without a timeline.
type Entryish = timeline.Entry

func renderAll(entries []Entryish) string {
	return timeline.Render(entries, nil, nil)
}

// firstJSON pulls the object out of a reply that came wrapped in prose or a
// fenced block.
func firstJSON(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if fence := strings.Index(trimmed, "```"); fence >= 0 {
		rest := trimmed[fence+3:]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			trimmed = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start < 0 || end <= start {
		return trimmed
	}
	return trimmed[start : end+1]
}

// Named returns the personas called out by name in the text.
//
// A mention skips the routing entirely: being able to choose who to ask is the
// point of having separate domains, and a named persona that never heard the
// question would make the choice meaningless. Routing still runs normally for
// everyone else.
func Named(skills []Skill, text string) []string {
	mentions := timeline.Mentions(text)
	if len(mentions) == 0 {
		return nil
	}
	var out []string
	for _, skill := range skills {
		for _, mention := range mentions {
			if skill.Answers(mention) {
				out = append(out, skill.ID)
				break
			}
		}
	}
	return out
}
