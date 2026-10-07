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

func TestSilentCheckInUsesOneAnswerAndCanSchedulePreparation(t *testing.T) {
	for _, wake := range []string{dailyShareInstructions, monthlyShareInstructions} {
		t.Run(wakeKind(timeline.Entry{Speaker: "system"}, false, wake), func(t *testing.T) {
			r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
			calls := map[string]int{}
			at := time.Now().Add(time.Hour).UTC()
			f.phaseReply = func(_, _ string, offered map[string]bool) string {
				switch {
				case offered["submit_answer"]:
					calls["answer"]++
					return `{"text":"","sources":[],"unrelated":false}`
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
			if calls["answer"] != 1 || calls["schedule"] != 1 || len(f.reviews) != 1 {
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

func TestMalformedPeriodReadsAreBoundedAndCanRecover(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			calls, reads := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct{ Messages []llm.Message }
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				switch {
				case calls == 1:
					writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "entries", Args: `{"ids":["e_known"]}`}}})
				case recover && calls == 3:
					if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "timeline_search") {
						t.Error("period correction missing")
					}
					writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "timeline_search", Args: `{"from":"2026-10-07 06:30","to":"2026-10-07 14:00"}`}}})
				case recover && calls == 4:
					writeResearchReply(w, submitAnswer, Answer{Sources: []string{}})
				default:
					writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "entries", Args: `{"from":"2026-10-07 06:30","to":"2026-10-07 14:00"}`}}})
				}
			}))
			defer server.Close()
			answer, err := composeAnswer(context.Background(), llm.New(server.URL, ""), "super", nil, turnRules{}, answerReads{
				capabilities: func() readCapabilities {
					return readCapabilities{readsRemaining: 10 - reads, tools: []tools.Declaration{{Name: "timeline_search"}}}
				},
				lookup: func(steps []ReadStep) string {
					reads++
					if reads == 2 && steps[0].Tool != "timeline_search" {
						t.Error("wrong corrected tool")
					}
					return "関連する新しい投稿なし"
				},
			})
			if recover {
				if err != nil || answer == nil || calls != 4 || reads != 2 {
					t.Fatalf("answer=%+v calls=%d reads=%d err=%v", answer, calls, reads, err)
				}
			} else if err == nil || calls != maxMalformed+2 || reads != 1 {
				t.Fatalf("calls=%d reads=%d err=%v", calls, reads, err)
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
		case offered["submit_answer"]:
			calls["answer"]++
			return `{"text":"好みを覚えておきます。","sources":[]}`
		default:
			t.Fatalf("read-only persona planned side effects: %v", offered)
			return ""
		}
	}
	r.react(post(t, s, "豚骨が好き").ID)
	if calls["research"] != 0 || calls["answer"] != 1 || len(f.reviews) != 0 || len(f.keeps) != 1 {
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
	if calls["research"] != 0 || calls["answer"] != 1 || len(entries) != 1 || !strings.Contains(entries[0].Text, "まだ残しておきますか") {
		t.Fatalf("calls=%v entries=%+v", calls, entries)
	}
}
