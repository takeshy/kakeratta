package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
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

func TestAnswerPromptOmitsUnusedThreadAndScheduleContext(t *testing.T) {
	noSchedSet := tools.New(tools.Config{}).For(nil)
	single := []timeline.Entry{{ID: "e_root", Text: "肉じゃが"}}
	sys := answerSystemFor(Skill{ID: "gourmet"}, single, noSchedSet, briefRule)
	for _, unwanted := range []string{"渋谷にいきたい", "他の担当の発言がすでに", "schedule=true"} {
		if strings.Contains(sys, unwanted) {
			t.Errorf("single-post non-schedule prompt included %q", unwanted)
		}
	}
	props, _ := answerToolFor(noSchedSet).Parameters["properties"].(map[string]any)
	if props["schedule"] != nil {
		t.Error("non-schedule persona was asked for schedule in submit_answer")
	}
	if strings.Contains(recordsReadSpec, "まとめ") || !strings.Contains(recordsReadSpec, "いまの事実と最近の記録") {
		t.Errorf("recordsReadSpec = %q", recordsReadSpec)
	}
}
