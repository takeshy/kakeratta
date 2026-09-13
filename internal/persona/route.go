package persona

import (
	"context"
	"encoding/json"
	"fmt"
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
const routePrompt = `投稿を、担当分野ごとのAIのうち誰に渡すかを振り分けます。

規則:
- **明らかに関係ないものを外すだけ**です。関係あるかもしれないものは渡してください。
- 渡した相手が必ず発言するわけではありません。最終的に黙るかどうかは相手が決めます。
  だから迷ったら渡す方に倒してください。外すのは「この担当には絶対に関係ない」ときだけです。
- 渡されたスレッド全体（元の投稿、ユーザーの追加発言、担当の発言）を読んで、今回の対象発言の意味と担当を判断してください。
- 追加発言は単独の投稿ではありません。省略された目的・場所・時間はスレッド内の文脈で補い、ユーザーの訂正や追加条件を優先してください。
  例: 「渋谷にいきたい」の後の「1時間後。」は渋谷への移動の時間に関する補足なので、移動の担当へ渡します。
- 過去に担当が回答済みでも、新しい条件や質問があれば改めて関係する担当へ渡してください。
- 誰にも渡さないという判断もあります。

{"deliver":["id", ...]} の形式で、渡す相手のidだけを返してください。`

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
	var domains []string
	for _, skill := range skills {
		domains = append(domains, fmt.Sprintf("- %s（%s）: %s", skill.ID, skill.Name, skill.Domain))
	}
	reply, err := client.Chat(ctx, llm.Request{
		Model: model, JSON: true,
		Messages: []llm.Message{
			{Role: "system", Content: routePrompt},
			{Role: "user", Content: strings.Join([]string{
				"担当:", strings.Join(domains, "\n"), "", "スレッド全体（古い順）:", renderAll(thread),
				"", "今回の対象発言（この発言をスレッド全体の文脈で解釈）:", trigger.Render(nil),
			}, "\n")},
		},
	})
	if err != nil {
		return all
	}
	var parsed struct {
		Deliver []string `json:"deliver"`
	}
	if err := json.Unmarshal([]byte(firstJSON(reply)), &parsed); err != nil || parsed.Deliver == nil {
		return all
	}
	known := map[string]bool{}
	for _, id := range all {
		known[id] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range parsed.Deliver {
		id = strings.TrimSpace(id)
		if !known[id] {
			return all
		}
		if known[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Entryish is the little of an entry that routing needs. It keeps this function
// testable without a store and without a timeline.
type Entryish = timeline.Entry

func renderAll(entries []Entryish) string {
	return timeline.Render(entries, nil)
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
