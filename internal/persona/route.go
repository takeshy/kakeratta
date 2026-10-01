package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const openRouterDecisionsURL = "https://openrouter.ai/api/alpha/decisions"
const jevModel = "~typesafe/jev-latest"

var jevHTTPClient = &http.Client{Timeout: 4 * time.Minute}

// Routing is what the app decides, and the only thing it decides about a post.
//
// It is made by Lightning so every post can be classified quickly, and it
// errs the other way from how it used to. Delivering by default made
// sense when a cheap model made the call and a wrong "no" swallowed a post;
// in practice it meant "明日9:10に神谷町に着きたい" reached the wine persona,
// which then had to be persuaded to stay quiet. A post now goes only where it
// belongs, and nobody is an ordinary answer.
const routePrompt = `投稿内容と担当条件を照合して振り分けます。
本人と担当の発言を含むスレッド全体で今回の発言を解釈し、補足・訂正を反映してください。画像の説明・文字・出典も投稿内容です。
各条件に当てはまる担当をすべて選びます。質問や依頼の有無、回答済みかは問いません。単語だけで判定せず、条件にない前提を足しません。列挙された対象はいずれか1つに当てはまれば一致です（条件が同時成立を指定する場合を除く）。例示は限定列挙ではなく、担当が調べる情報が投稿に揃っている必要もありません。
deliver に一致する担当id、excluded_reasons に不一致の担当idと短い理由を入れてツールを呼んでください。担当idは「担当条件」の各行の先頭にあるidで、発言のidではありません。表示名を付けず、全担当idをどちらか一方に含めます。一致なしなら deliver は空です。
理由は本人が読みます。今回の対象発言と同じ言語で書き、発言に文章がない（画像だけなど）ときは担当条件が書かれている言語で書いてください。`

// deliverTool returns condition matches and non-match explanations. The latter
// are logged for diagnosing omissions; only matched IDs control delivery.
var deliverTool = llm.Tool{
	Name:        "deliver",
	Description: "投稿内容が一致する担当条件を分類する。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"deliver": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "条件に一致する担当のid一覧。一致なしなら空",
		},
		"excluded_reasons": map[string]any{
			"type": "object", "additionalProperties": map[string]any{"type": "string"},
			"description": "条件に一致しない全担当のidごとの不一致の根拠。対象発言と同じ言語で（文章がなければ担当条件の言語で）",
		},
	}, "required": []any{"deliver", "excluded_reasons"}},
}

// Routing is what the router said about one post: who it goes to, and for
// everyone else, why not. Answered is false when there was no usable answer -
// no model configured, the call failed, the reply made no sense - and Error
// then says which.
type Routing struct {
	Deliver  []string
	Excluded map[string]string
	// Heard is, for each person delivered, the name or keyword it was found by.
	Heard    map[string]string
	Answered bool
	Error    string
}

// Route picks who a post goes to.
//
// When it cannot decide - no model configured, the call failed, the reply made
// no sense - nobody gets it. A persona named in the post is reached without
// routing, so the one case where a post must not be lost does not depend on
// this.
func Route(ctx context.Context, client *llm.Client, model string, skills []Skill, thread []Entryish, trigger Entryish) Routing {
	if len(skills) == 0 || client == nil || client.BaseURL == "" || model == "" {
		return Routing{Error: "routing is not configured"}
	}
	decision := routeRecord{Severity: "INFO", Message: "routing", Trigger: trigger.ID, Model: model}
	defer func() { logRouting(decision) }()
	nobody := func(reason string) Routing {
		decision.Severity = "WARNING"
		decision.Error = reason
		return Routing{Error: reason}
	}
	var domains []string
	for _, skill := range skills {
		domains = append(domains, fmt.Sprintf("- %s（%s）: %s", skill.ID, skill.Name, skill.Domain))
	}
	thinking := true
	reply, err := client.Complete(ctx, llm.Request{
		Model: model, Tools: []llm.Tool{deliverTool}, ToolChoice: "required",
		Thinking: &thinking, Exacto: true,
		Messages: []llm.Message{
			{Role: "system", Content: routePrompt},
			{Role: "user", Content: strings.Join([]string{
				"担当条件:", strings.Join(domains, "\n"), "", "スレッド全体（古い順）:", renderAll(thread),
				"", "今回の対象発言（この発言をスレッド全体の文脈で解釈）:", trigger.Render(nil, nil),
			}, "\n")},
		},
	})
	if err != nil {
		return nobody("the routing call failed: " + err.Error())
	}
	var parsed struct {
		Deliver         []string          `json:"deliver"`
		Reasons         map[string]string `json:"reasons"`
		ExcludedReasons map[string]string `json:"excluded_reasons"`
		Reason          string            `json:"reason"`
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
	decision.ExcludedReasons = parsed.ExcludedReasons
	// A router that answers "sona" when the id is "routine" has named the right
	// persona by the name a person sees. One that copies "book（shiori）" from the
	// list has named it twice.
	resolve := func(want string) string {
		want = strings.TrimSpace(want)
		if open := strings.IndexAny(want, "（("); open > 0 {
			want = strings.TrimSpace(want[:open])
		}
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
	// The reasons are keyed however the router chose to name each persona;
	// they are kept under the id so a reader can find them again. The first
	// reason given for a persona stands when it was named twice.
	excluded := map[string]string{}
	for named, reason := range parsed.ExcludedReasons {
		id := resolve(named)
		if id == "" {
			continue
		}
		if _, ok := excluded[id]; !ok {
			excluded[id] = reason
		}
	}
	return Routing{Deliver: decision.Deliver, Excluded: excluded, Answered: true}
}

// RouteJev uses OpenRouter's Decisions API, which is deliberately separate
// from its OpenAI-compatible chat endpoint. Each persona is one independent
// deliver/exclude choice in a single request.
func RouteJev(ctx context.Context, endpoint, key string, skills []Skill, thread []Entryish, trigger Entryish) Routing {
	decision := routeRecord{Severity: "INFO", Message: "routing", Trigger: trigger.ID, Model: jevModel}
	defer func() { logRouting(decision) }()
	nobody := func(reason string) Routing {
		decision.Severity, decision.Error = "WARNING", reason
		return Routing{Error: reason}
	}
	if len(skills) == 0 || strings.TrimSpace(endpoint) == "" || strings.TrimSpace(key) == "" {
		return nobody("routing is not configured")
	}
	questions := map[string]any{}
	for _, skill := range skills {
		questions[skill.ID] = map[string]any{
			"type":         "choice",
			"instructions": routePrompt + "\nこの担当条件に今回の対象発言が一致するか判定してください。担当条件: " + skill.Domain,
			"criteria": map[string]any{
				"deliver": "担当条件に一致する",
				"exclude": "担当条件に一致しない",
			},
		}
	}
	body, err := json.Marshal(map[string]any{
		"model": jevModel,
		"state": strings.Join([]string{
			"スレッド全体（古い順）:", renderAll(thread), "",
			"今回の対象発言（この発言をスレッド全体の文脈で解釈）:", trigger.Render(nil, nil),
		}, "\n"),
		"questions": questions,
	})
	if err != nil {
		return nobody("the routing request could not be encoded: " + err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/"), bytes.NewReader(body))
	if err != nil {
		return nobody("the routing request could not be made: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	req.Header.Set("Content-Type", "application/json")
	res, err := jevHTTPClient.Do(req)
	if err != nil {
		return nobody("the routing call failed: " + err.Error())
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nobody("the routing response could not be read: " + err.Error())
	}
	if res.StatusCode >= 300 {
		return nobody(fmt.Sprintf("the routing call failed: HTTP %d", res.StatusCode))
	}
	var parsed struct {
		Answers map[string]struct {
			Choice string `json:"choice"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nobody("the routing response made no sense: " + err.Error())
	}
	excluded := map[string]string{}
	for _, skill := range skills {
		answer, ok := parsed.Answers[skill.ID]
		if !ok || (answer.Choice != "deliver" && answer.Choice != "exclude") {
			return nobody("the routing response omitted a persona")
		}
		if answer.Choice == "deliver" {
			decision.Deliver = append(decision.Deliver, skill.ID)
		} else {
			// Decisions API returns only the choice. Keep the excluded ID for
			// feedback, but do not invent a reason Jev did not provide.
			excluded[skill.ID] = ""
		}
	}
	decision.ExcludedReasons = excluded
	return Routing{Deliver: decision.Deliver, Excluded: excluded, Answered: true}
}

// routeRecord is one routing decision, logged as a structured entry.
type routeRecord struct {
	Severity        string            `json:"severity"`
	Message         string            `json:"message"`
	Trigger         string            `json:"trigger,omitempty"`
	Model           string            `json:"model"`
	Deliver         []string          `json:"deliver"`
	Reasons         map[string]string `json:"reasons,omitempty"`
	ExcludedReasons map[string]string `json:"excluded_reasons,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	Unknown         []string          `json:"unknown,omitempty"`
	Error           string            `json:"error,omitempty"`
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
// point of having separate domains. The HTTP API accepts at most one distinct
// mentioned persona; other personas are not added by automatic routing.
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
