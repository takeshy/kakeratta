package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

func TestSilentCheckInSkipsCompositionButCanSchedulePreparation(t *testing.T) {
	for _, wake := range []string{dailyShareInstructions, monthlyShareInstructions} {
		t.Run(wakeKind(timeline.Entry{Speaker: "system"}, false, wake), func(t *testing.T) {
			r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
			calls := map[string]int{}
			at := time.Now().Add(time.Hour).UTC()
			f.phaseReply = func(_, _ string, offered map[string]bool) string {
				switch {
				case offered["finish_silent"]:
					calls["research"]++
					return "silent_done"
				case offered["submit_schedule"]:
					calls["schedule"]++
					return fmt.Sprintf(`{"operations":[{"type":"create","at":%q,"note":"外出前の準備","daily":false}],"consistent":true,"reason":"準備時刻まで黙る"}`, at.Format(time.RFC3339))
				default:
					t.Fatalf("unexpected composition or action: %v", offered)
					return ""
				}
			}
			client, settings := r.clientOf()
			skill, _ := r.catalogue.Find(context.Background(), "routine")
			trigger := timeline.Entry{Speaker: "system", At: time.Now(), Text: "予定の準備は1時間後"}
			r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, false, false, wake, false, client, settings, r.registry(settings))
			if calls["research"] != 1 || calls["schedule"] != 1 || len(f.reviews) != 1 {
				t.Fatalf("calls=%v reviews=%d", calls, len(f.reviews))
			}
			if entries, _ := s.Timeline(); len(entries) != 0 {
				t.Fatalf("silent check-in published: %+v", entries)
			}
			if schedules, _ := s.Schedules("routine"); len(schedules) != 1 || schedules[0].Note != "外出前の準備" {
				t.Fatalf("silent check-in lost preparation: %+v", schedules)
			}
		})
	}
}

func TestMalformedPeriodReadsAreBoundedAfterInitialPlanning(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			calls, reads, decisions, silences := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct{ Messages []llm.Message }
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				switch {
				case calls == 1:
					writeResearchReply(w, "read", map[string]any{"deep": false, "research_plan": []string{"最近の投稿を確認"}, "steps": []ReadStep{{Type: "entries", Args: `{"ids":["e_known"]}`}}})
				case recover && calls == 3:
					// Correct the format instead of silently pretending to have
					// searched. The intended period reaches the real lookup.
					if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "timeline_search") {
						t.Error("refusal did not explain period search")
					}
					writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "timeline_search", Args: `{"from":"2026-10-07 06:30","to":"2026-10-07 14:00"}`}}})
				case recover && calls == 4:
					writeResearchReply(w, "finish_silent", map[string]any{"unrelated": false})
				default:
					writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "entries", Args: `{"from":"2026-10-07 06:30","to":"2026-10-07 14:00"}`}}})
				}
			}))
			defer server.Close()
			_, err := research(context.Background(), llm.New(server.URL, ""), "super", nil,
				func() readCapabilities {
					return readCapabilities{readsRemaining: 10 - reads, tools: []tools.Declaration{{Name: "timeline_search"}}}
				}, func(steps []ReadStep) string {
					reads++
					if reads == 2 && (steps[0].Tool != "timeline_search" || !strings.Contains(steps[0].Args, "06:30")) {
						t.Error("corrected period search was lost")
					}
					return "確認済み。関連する新しい投稿なし。"
				}, func(researchDecision) { decisions++ }, func(Answer) { silences++ })
			if recover {
				if err != nil || calls != 4 || reads != 2 || decisions != 1 || silences != 1 {
					t.Fatalf("calls=%d reads=%d decisions=%d silences=%d err=%v", calls, reads, decisions, silences, err)
				}
			} else if err == nil || calls != maxMalformed+2 || reads != 1 || decisions != 1 || silences != 0 {
				t.Fatalf("calls=%d reads=%d decisions=%d silences=%d err=%v", calls, reads, decisions, silences, err)
			}
		})
	}
}

func TestResearchRejectsInvalidOrUnpermittedSilence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
		args    string
	}{
		{"not offered", false, `{"unrelated":false,"deep":false,"research_plan":[]}`},
		{"missing unrelated", true, `{"deep":false,"research_plan":[]}`},
		{"invalid unrelated", true, `{"unrelated":"false","deep":false,"research_plan":[]}`},
		{"missing initial plan", true, `{"unrelated":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, decisions, silences := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				// The offered schema is checked in integration tests; here a
				// provider deliberately emits malformed or unoffered calls.
				if calls == 1 {
					writeResearchReply(w, "finish_silent", json.RawMessage(tc.args))
				} else {
					writeResearchReply(w, "finish_read", map[string]any{"deep": false, "research_plan": []string{}})
				}
			}))
			defer server.Close()
			var silent []func(Answer)
			if tc.allowed {
				silent = append(silent, func(Answer) { silences++ })
			}
			_, err := research(context.Background(), llm.New(server.URL, ""), "super", nil,
				func() readCapabilities { return readCapabilities{readsRemaining: 10} },
				func([]ReadStep) string { t.Fatal("invalid silence executed a read"); return "" },
				func(researchDecision) { decisions++ }, silent...)
			if err != nil || calls != 2 || decisions != 1 || silences != 0 {
				t.Fatalf("calls=%d decisions=%d silences=%d err=%v", calls, decisions, silences, err)
			}
		})
	}
}

func TestRememberingReadOnlyPersonaSkipsSideEffectCalls(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	calls := map[string]int{}
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if offered["finish_silent"] {
			t.Fatal("ordinary post can finish silently during research")
		}
		switch {
		case offered["finish_read"]:
			calls["research"]++
			return "read_done"
		case offered["submit_answer"]:
			calls["answer"]++
			return `{"text":"好みを覚えておきます。","sources":[]}`
		default:
			t.Fatalf("read-only persona planned side effects: %v", offered)
			return ""
		}
	}
	r.react(post(t, s, "豚骨が好き").ID)
	if calls["research"] != 1 || calls["answer"] != 1 || len(f.reviews) != 0 || len(f.keeps) != 1 {
		t.Fatalf("calls=%v reviews=%d keeps=%d", calls, len(f.reviews), len(f.keeps))
	}
	if len(remarks(t, s)) != 1 || len(recordIDs(r, "gourmet")) != 1 {
		t.Fatal("answer or memory was lost")
	}
}

func TestCheckInWithDueReviewStillComposesQuestion(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	calls := map[string]int{}
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if offered["finish_silent"] {
			t.Fatal("review question can be skipped during research")
		}
		switch {
		case offered["finish_read"]:
			calls["research"]++
			return "read_done"
		case offered["submit_answer"]:
			calls["answer"]++
			return `{"text":"行きたい店の記憶は、まだ残しておきますか？","sources":[]}`
		default:
			t.Fatalf("unexpected phase: %v", offered)
			return ""
		}
	}
	client, settings := r.clientOf()
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	trigger := timeline.Entry{Speaker: "system", At: time.Now(), Text: "Review due"}
	r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, false, false, dailyShareInstructions+reviewInstructions, false, client, settings, r.registry(settings))
	entries, _ := s.Timeline()
	if calls["research"] != 1 || calls["answer"] != 1 || len(entries) != 1 || !strings.Contains(entries[0].Text, "まだ残しておきますか") {
		t.Fatalf("calls=%v entries=%+v", calls, entries)
	}
}
