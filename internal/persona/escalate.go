package persona

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Escalation is the one thing Lightning decides about a reply to a persona:
// whether the user is asking for a better or deeper answer than the one they
// got. It does not judge whether Super would be good enough - only whether
// the user said, in so many words, that they want more.

const escalatePrompt = `あなたは、ユーザーの返信が「前の回答より良い・深い回答」を明示的に求めているかだけを判定します。

true にするのは、前の回答を受けて次のどれかを求めている場合だけです:
- 訂正・否定・疑い（「違う」「本当？」「それは間違い」）
- 再検討（「もう一度考えて」「ちゃんと調べて」）
- 詳細化・深掘り（「もっと詳しく」「深掘りして」「比較して詳しく」「根拠は？」）

false にするのは、それ以外すべてです:
- お礼・相づち（「ありがとう」「なるほど」）
- 前の回答に関連する普通の追加質問や条件の追加（「1時間後。」「駅から近い店は？」）
- 別の話題

迷ったら false にします。escalate を呼んで答えてください。`

var escalateTool = llm.Tool{
	Name:        "escalate",
	Description: "返信が前の回答より良い・深い回答を明示的に求めているかを答える。",
	Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"escalate": map[string]any{"type": "boolean"}},
		"required":   []any{"escalate"},
	},
}

// Escalate reports whether reply asks for a better or deeper answer than
// answer. Anything that goes wrong - no model, a failed call, a reply that
// makes no sense - is a no: the reply is still answered, on Super.
func Escalate(ctx context.Context, client *llm.Client, model string, answer, reply timeline.Entry) bool {
	if client == nil || client.BaseURL == "" || strings.TrimSpace(model) == "" || strings.TrimSpace(reply.Text) == "" {
		return false
	}
	thinking := false
	ctx = llm.WithTrace(ctx, llm.Trace{Trigger: reply.ID, Wake: "post"})
	result, err := client.Complete(ctx, llm.Request{
		Purpose: "escalation", Model: model, Tools: []llm.Tool{escalateTool}, ToolChoice: "required", Thinking: &thinking,
		Messages: []llm.Message{
			{Role: "system", Content: escalatePrompt},
			{Role: "user", Content: "前の回答:\n" + answer.Text + "\n\nユーザーの返信:\n" + reply.Text},
		},
	})
	if err != nil {
		return false
	}
	raw := firstJSON(result.Content)
	for _, call := range result.Calls {
		if call.Name == escalateTool.Name {
			raw = call.Args
		}
	}
	var parsed struct {
		Escalate bool `json:"escalate"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return false
	}
	return parsed.Escalate
}
