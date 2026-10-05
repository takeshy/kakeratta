package persona

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Explicit opt-in: one Super research request per synthetic scenario, with
// no production reads/writes. This checks model judgement, not a keyword rule.
func TestInitialSuperPlanningLive(t *testing.T) {
	if os.Getenv("KAKERATTA_RESEARCH_PLAN_LIVE") != "1" {
		t.Skip("set KAKERATTA_RESEARCH_PLAN_LIVE=1 and NEBIUS_API_KEY to run paid Super planning checks")
	}
	client := llm.New(store.NebiusBaseURL, liveKey(t))
	// Offer the real web-search schema using a fixture key. The lookup is
	// intercepted below, so only the model endpoint receives a real request.
	set := tools.New(tools.Config{}).WithTavilyKey("fixture-only").For([]string{"web_search"})
	for _, tc := range []struct {
		name, parent, request string
		deep                  bool
	}{
		{"details", "PiのエージェントループはLLMの応答とtool実行を繰り返します。", "詳しく", true},
		{"thanks", "PiのエージェントループはLLMの応答とtool実行を繰り返します。", "ありがとう", false},
		{"simple-followup", "明日は午後から雨になりそうです。", "何時ごろ？", false},
		{"simple-question", "", "明日の東京の天気は？", false},
		{"complex-question", "", "分散エージェントの実行キューで、lease切れと再試行が競合した場合の二重実行を防ぐ設計を、所有権・障害復旧・副作用の整合性から比較してください。", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			var decision *researchDecision
			var records []llm.CallRecord
			client.Observe = func(record llm.CallRecord) { records = append(records, record) }
			messages, err := research(ctx, client, store.NemotronSuper, []llm.Message{
				{Role: "system", Content: answerSystem(Skill{ID: "support", Name: "support", Domain: "質問の対象を理解し、技術的な説明や調べものを支援します。"}, namedRule)},
				{Role: "user", Content: "担当の親回答:\n" + tc.parent + "\nユーザーの今回の依頼:\n" + tc.request},
			}, func() readCapabilities {
				remaining := maxReads
				if decision != nil {
					remaining = 0
				}
				return currentReadCapabilities(Skill{}, set, newLookups(maxReads, maxSearches), nil, remaining)
			}, func([]ReadStep) string { return "テストでは実際のreadは実行しません。" }, func(result researchDecision) { decision = &result })
			if err != nil || decision == nil {
				t.Fatalf("planning failed: %v", err)
			}
			if decision.Deep != tc.deep {
				for _, message := range messages {
					if message.Role == "assistant" {
						t.Logf("calls=%+v", message.Calls)
					}
				}
				t.Errorf("deep=%t want=%t plan=%v", decision.Deep, tc.deep, decision.Plan)
			}
			if len(records) != 1 || records[0].Model != store.NemotronSuper || records[0].Purpose != "research" || records[0].Round != 1 {
				t.Errorf("unexpected calls: %+v", records)
			}
			if tc.name == "simple-question" && len(decision.Plan) == 0 {
				t.Error("weather question did not produce a research plan despite available web search")
			}
			usage, _ := json.Marshal(records[0].Usage)
			t.Logf("deep=%t plan=%v usage=%s", decision.Deep, decision.Plan, usage)
		})
	}
}
