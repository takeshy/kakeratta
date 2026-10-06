package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestComposeAnswerRetriesTruncationWithoutAccumulatingDrafts(t *testing.T) {
	for _, recover := range []bool{true, false} {
		t.Run(map[bool]string{true: "recover", false: "exhausted"}[recover], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					MaxTokens int `json:"max_tokens"`
					Messages  []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
						Calls   []any  `json:"tool_calls"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				calls++
				if request.MaxTokens != 26000 {
					t.Errorf("answer output allowance = %d, want 26000", request.MaxTokens)
				}
				if len(request.Messages) < 3 || request.Messages[1].Content != "original research" {
					t.Error("research context was lost")
				}
				if calls > 1 {
					if len(request.Messages) != 4 || !strings.Contains(request.Messages[3].Content, "出力上限") {
						t.Errorf("retry did not start from original context: %+v", request.Messages)
					}
					for _, message := range request.Messages {
						if strings.Contains(message.Content, "truncated draft") || len(message.Calls) != 0 {
							t.Error("incomplete draft was sent back")
						}
					}
				}
				if recover && calls == 3 {
					_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"done","function":{"name":"submit_answer","arguments":"{\"text\":\"complete diagram\",\"sources\":[]}"}}]}}]}`))
					return
				}
				// Even a syntactically valid call is unsafe when the provider
				// reports that generation was cut off.
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"truncated draft","tool_calls":[{"id":"cut","function":{"name":"submit_answer","arguments":"{\"text\":\"incomplete\",\"sources\":[]}"}}]}}]}`))
			}))
			defer server.Close()
			client := llm.New(server.URL, "")
			client.Observe = func(llm.CallRecord) {}
			answer, err := composeAnswer(context.Background(), client, "test", []llm.Message{{Role: "system", Content: "instructions"}, {Role: "user", Content: "original research"}}, turnRules{named: true})
			if calls != maxMalformed+1 {
				t.Fatalf("calls = %d", calls)
			}
			if recover {
				if err != nil || answer == nil || answer.Text != "complete diagram" {
					t.Fatalf("answer = %+v, error = %v", answer, err)
				}
			} else if answer != nil || err == nil || !strings.Contains(err.Error(), "出力上限") {
				t.Fatalf("answer = %+v, error = %v", answer, err)
			}
		})
	}
}

// A long answer citing the post it diagrams is kept on the first draft, with
// the stray id dropped; a turn that still fails says why in its error.
func TestComposeAnswerKeepsDraftsWithStraySourcesAndReportsRefusals(t *testing.T) {
	reply := func(arguments string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{
				"tool_calls": []any{map[string]any{"id": "a", "function": map[string]any{"name": "submit_answer", "arguments": arguments}}}}}}})
			_, _ = w.Write(body)
		}
	}
	compose := func(handler http.HandlerFunc) (*Answer, error, int) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; handler(w, r) }))
		defer server.Close()
		client := llm.New(server.URL, "")
		client.Observe = func(llm.CallRecord) {}
		answer, err := composeAnswer(context.Background(), client, "test", []llm.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}, turnRules{named: true, cited: map[string]string{}})
		return answer, err, calls
	}
	answer, err, calls := compose(reply(`{"text":"# 📊 図解","sources":["e_replied_to"],"unrelated":false}`))
	if err != nil || calls != 1 || answer.Text != "# 📊 図解" || answer.Sources == nil || len(answer.Sources) != 0 {
		t.Fatalf("answer = %+v, error = %v, calls = %d", answer, err, calls)
	}
	answer, err, calls = compose(reply(`{"text":"","sources":[]}`))
	if answer != nil || calls != maxMalformed+1 || err == nil || !strings.Contains(err.Error(), "attempt 3") || !strings.Contains(err.Error(), "名指し") {
		t.Fatalf("answer = %+v, error = %v, calls = %d", answer, err, calls)
	}
}
