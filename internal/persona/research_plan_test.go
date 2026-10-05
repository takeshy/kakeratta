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

func TestInitialResearchPlanningAndBudget(t *testing.T) {
	for _, deep := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "deep"}[deep], func(t *testing.T) {
			budget := newLookups(maxReads, maxSearches)
			readsUsed, readLimit, calls, decisions := 0, maxReads, 0, 0
			plan := []string{"前回答の仕組みを確認", "必要な実装詳細を確認"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct {
					Tools []struct {
						Function struct {
							Name       string
							Parameters map[string]any
						}
					}
					Messages []llm.Message
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				props := body.Tools[0].Function.Parameters["properties"].(map[string]any)
				_, planning := props["deep"]
				if planning != (calls == 1) {
					t.Errorf("round %d planning schema=%t", calls, planning)
				}
				if calls == 1 {
					for _, want := range []string{"詳しく", "親回答", "ありがとう", "複数情報の比較・統合", "思考過程"} {
						if !strings.Contains(body.Messages[0].Content, want) {
							t.Errorf("missing initial instruction %q", want)
						}
					}
					if !strings.Contains(body.Messages[0].Content, "read は残り 10 回") {
						t.Error("initial normal read budget missing")
					}
					writeResearchReply(w, "read", map[string]any{"deep": deep, "research_plan": plan, "steps": []ReadStep{{Type: "tool", Tool: "web_search", Args: `{"query":"one"}`}}})
				} else {
					if strings.Contains(body.Messages[0].Content, "# 初回の調査方針") {
						t.Error("initial instruction repeated")
					}
					if !strings.Contains(body.Messages[0].Content, plan[0]) {
						t.Error("research plan lost")
					}
					wantReads, wantSearch := 9, 1
					if deep {
						wantReads, wantSearch = 19, 3
					}
					if readLimit-readsUsed != wantReads || budget.SearchRemaining() != wantSearch || budget.used != 1 || budget.searched != 1 {
						t.Errorf("budget reset or not expanded: reads=%d budget=%+v", readLimit-readsUsed, budget)
					}
					writeResearchReply(w, "finish_read", map[string]any{})
				}
			}))
			defer server.Close()
			client := llm.New(server.URL, "")
			var usage []llm.CallRecord
			client.Observe = func(record llm.CallRecord) { usage = append(usage, record) }
			messages, err := research(context.Background(), client, "super", []llm.Message{{Role: "system", Content: "test"}, {Role: "user", Content: "親回答: Piのエージェントループについて...\n返信: 詳しく"}}, func() readCapabilities {
				return readCapabilities{readsRemaining: readLimit - readsUsed, searchRemaining: budget.SearchRemaining()}
			}, func(steps []ReadStep) string {
				if decisions != 1 {
					t.Error("read ran before initial planning")
				}
				budget.used++
				budget.searched++
				readsUsed++
				return "最初のread結果"
			}, func(decision researchDecision) {
				decisions++
				if decision.Deep != deep || len(decision.Plan) != 2 {
					t.Errorf("decision=%+v", decision)
				}
				if decision.Deep {
					readLimit = ultraReads
					budget.limit = ultraReads
					budget.searches = ultraSearches
				}
			})
			if err != nil || len(messages) == 0 || calls != 2 || decisions != 1 || len(usage) != 2 {
				t.Fatalf("calls=%d decisions=%d usage=%v err=%v", calls, decisions, usage, err)
			}
			for i, record := range usage {
				if record.Purpose != "research" || record.Model != "super" || record.Round != i+1 || record.RetryCount != 0 {
					t.Errorf("usage=%+v", record)
				}
			}
		})
	}
}

func writeResearchReply(w http.ResponseWriter, name string, args any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": testCall(name, args)}}})
}

func TestInitialResearchRejectsMalformedPlanBeforeRead(t *testing.T) {
	for _, args := range []string{`{}`, `{"deep":false}`, `{"deep":"true","research_plan":[]}`, `{"deep":true,"research_plan":[""]}`, `{"deep":true,"research_plan":["1","2","3","4","5","6"]}`, `{"deep":true,"research_plan":[],"steps":"invalid"}`} {
		t.Run(args, func(t *testing.T) {
			calls, reads, decisions := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				writeResearchReply(w, "read", json.RawMessage(args))
			}))
			defer server.Close()
			_, err := research(context.Background(), llm.New(server.URL, ""), "super", nil, func() readCapabilities { return readCapabilities{readsRemaining: 10} }, func([]ReadStep) string { reads++; return "" }, func(researchDecision) { decisions++ })
			if err == nil || reads != 0 || decisions != 0 || calls != maxMalformed+1 {
				t.Fatalf("err=%v calls=%d reads=%d decisions=%d", err, calls, reads, decisions)
			}
		})
	}
}
