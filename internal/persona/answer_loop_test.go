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

func TestRecallChronologyRulesReachBuiltInAndExternalAnswers(t *testing.T) {
	skill := (Skill{ID: "dad", Name: "父", Kind: KindPerson}).forTurn()
	post := timeline.Entry{ID: "question", Speaker: timeline.User, Text: "父に何送ったか言ったっけ？"}
	for _, thread := range [][]timeline.Entry{{post}, {post, {ID: "followup", Speaker: timeline.User, Text: "最近のほう"}}} {
		for path, prompt := range map[string]string{
			"built-in": answerSystemFor(skill, thread, nil, briefRule),
			"external": answerRulesFor(thread, nil, agentRule),
		} {
			for _, rule := range []string{
				"対象発言までの直近の該当する出来事を優先",
				"特定の時期や以前の出来事を指定",
				"より新しい該当記録がないか確認",
				"検索結果の関連度順やメモの並び順",
				"投稿日時・記憶の更新日",
				"昔の出来事を最近の投稿で振り返っていても",
				"確認できた範囲を示し",
			} {
				if !strings.Contains(prompt, rule) {
					t.Errorf("%s answer (%d entries) missing chronology rule %q", path, len(thread), rule)
				}
			}
		}
	}
}
