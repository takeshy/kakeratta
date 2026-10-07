package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestAnswerWithoutReadsNeedsOnlyOneCallAndCannotSelectDepth(t *testing.T) {
	for _, text := range []string{"返答です", ""} {
		t.Run(fmt.Sprintf("spoken=%t", text != ""), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct {
					Tools []struct {
						Function struct {
							Name       string
							Parameters map[string]any
						}
					}
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				for _, tool := range body.Tools {
					if tool.Function.Name == "finish_read" || tool.Function.Name == "finish_silent" {
						t.Error("preliminary research tool still offered")
					}
					props, _ := tool.Function.Parameters["properties"].(map[string]any)
					if props["deep"] != nil || props["research_plan"] != nil {
						t.Error("model still selects depth or a plan")
					}
				}
				writeResearchReply(w, submitAnswer, Answer{Text: text, Sources: []string{}})
			}))
			defer server.Close()
			answer, err := composeAnswer(context.Background(), llm.New(server.URL, ""), "super", nil, turnRules{}, answerReads{
				capabilities: func() readCapabilities { return readCapabilities{readsRemaining: 10} }, lookup: func([]ReadStep) string { t.Error("unneeded read"); return "" },
			})
			if err != nil || answer == nil || answer.Text != text || calls != 1 {
				t.Fatalf("answer=%+v calls=%d err=%v", answer, calls, err)
			}
		})
	}
}
