package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Routing is what the app decides, and the only thing it decides about a post.
//
// It is made by the app's own model - the same one that reviews workflows -
// and it errs the other way from how it used to. Delivering by default made
// sense when a cheap model made the call and a wrong "no" swallowed a post;
// in practice it meant "明日9:10に神谷町に着きたい" reached the wine persona,
// which then had to be persuaded to stay quiet. A post now goes only where it
// belongs, and nobody is an ordinary answer.
const routePrompt = `投稿を、担当分野ごとのAIに振り分けます。誰に渡すかを決めてください。

**渡すのは、投稿の中身がその担当の領域に本当に属するときだけです。** 当てはまる担当がいなければ、誰にも渡しません。それは普通の答えです。

規則:
- **言葉が出てきただけでは渡しません。** 地名・時刻・食べものの名前が出てきても、投稿がその担当の扱う用件でなければ渡しません。
  例: 「明日9:10に神谷町に着きたい」は移動の相談です。移動の担当にだけ渡し、ワインや食事の担当には渡しません。
  例: 「今日の20時30分からドラマを見る」は、時刻の付いた、これからやると言ったことです。それを扱う担当にだけ渡します。
- 1つの投稿が本当に複数の領域にまたがるときだけ、複数に渡します。「ついでに何か言えそう」は理由になりません。
- 渡されたスレッド全体（元の投稿、ユーザーの追加発言、担当の発言）を読んで、今回の対象発言の意味を判断してください。
- 追加発言は単独の投稿ではありません。省略された目的・場所・時間はスレッド内の文脈で補い、ユーザーの訂正や追加条件を優先してください。
  例: 「渋谷にいきたい」の後の「1時間後。」は渋谷への移動の時間に関する補足なので、移動の担当へ渡します。
- 過去に担当が回答済みでも、新しい条件や質問があれば改めてその担当へ渡してください。

deliver を呼んで答えてください。deliver には渡す担当のid（**括弧の中の表示名ではなく、左側のid**）を、
reasons にはそれぞれの担当に渡す理由を一文で書きます。誰にも渡さないときは deliver を空にして、reason にその理由を書きます。`

// deliverTool is the one call routing makes. The reasons are not used to
// decide anything; they are there so the log can say why a post went where it
// went, which is the question every wrong delivery raises.
var deliverTool = llm.Tool{
	Name:        "deliver",
	Description: "この投稿を渡す担当を決める。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"deliver": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "渡す担当のid。誰にも渡さないなら空",
		},
		"reasons": map[string]any{
			"type": "object", "additionalProperties": map[string]any{"type": "string"},
			"description": "担当のidごとに、渡す理由を一文で",
		},
		"reason": map[string]any{"type": "string", "description": "誰にも渡さないとき、その理由"},
	}, "required": []any{"deliver"}},
}

// Route picks who a post goes to.
//
// When it cannot decide - no model configured, the call failed, the reply made
// no sense - nobody gets it. A persona named in the post is reached without
// routing, so the one case where a post must not be lost does not depend on
// this.
func Route(ctx context.Context, client *llm.Client, model string, skills []Skill, thread []Entryish, trigger Entryish) []string {
	if len(skills) == 0 || client == nil || client.BaseURL == "" || model == "" {
		return nil
	}
	decision := routeRecord{Severity: "INFO", Message: "routing", Trigger: trigger.ID, Model: model}
	defer func() { logRouting(decision) }()
	nobody := func(reason string) []string {
		decision.Severity = "WARNING"
		decision.Error = reason
		return nil
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
		return nobody("the routing call failed: " + err.Error())
	}
	var parsed struct {
		Deliver []string          `json:"deliver"`
		Reasons map[string]string `json:"reasons"`
		Reason  string            `json:"reason"`
	}
	// The call is what this asks for, but a model that answered in prose has
	// still answered.
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
		return nobody(fmt.Sprintf("the reply made no sense: %q", chosen))
	}
	decision.Reasons, decision.Reason = parsed.Reasons, parsed.Reason
	// A router that answers "plana" when the id is "routine" has named the right
	// persona by the name a person sees.
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
	seen := map[string]bool{}
	for _, named := range parsed.Deliver {
		id := resolve(named)
		if id == "" {
			// A name that matches nobody is ignored, not a reason to guess.
			decision.Unknown = append(decision.Unknown, named)
			continue
		}
		if !seen[id] {
			seen[id] = true
			decision.Deliver = append(decision.Deliver, id)
		}
	}
	return decision.Deliver
}

// routeRecord is one routing decision, logged as a structured entry.
type routeRecord struct {
	Severity string            `json:"severity"`
	Message  string            `json:"message"`
	Trigger  string            `json:"trigger,omitempty"`
	Model    string            `json:"model"`
	Deliver  []string          `json:"deliver"`
	Reasons  map[string]string `json:"reasons,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Unknown  []string          `json:"unknown,omitempty"`
	Error    string            `json:"error,omitempty"`
}

func logRouting(decision routeRecord) {
	if decision.Deliver == nil {
		decision.Deliver = []string{}
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(encoded))
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
