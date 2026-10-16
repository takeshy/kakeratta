package persona

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestAnswerReadsUseSelectedDepthAndPhasesUseSuper(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		deep, always          bool
		high, middle, low     string
		wantSuper, wantAnswer string
		// split turns read on Super and leave only the answer to Ultra.
		split bool
	}{
		{"ordinary", false, false, "ultra", "super", "lightning", "super", "super", false},
		{"deep", true, false, "ultra", "super", "lightning", "super", "ultra", true},
		{"always", false, true, "ultra", "super", "lightning", "super", "ultra", true},
		{"both", true, true, "ultra", "super", "lightning", "super", "ultra", true},
		{"no-high", true, false, "", "super", "lightning", "super", "super", false},
		{"low-only", false, true, "", "", "lightning", "lightning", "lightning", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, nil, "gourmet")
			skill, _ := r.catalogue.Find(context.Background(), "gourmet")
			skill.AlwaysUltra = tc.always
			skill.Tools = []string{"kakeratta_help", "schedule", "web_search"}
			text := "調査した根拠で答えて"
			if tc.deep {
				text += "。詳しく"
			}
			trigger := post(t, s, text)
			reads := 0
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				switch {
				case offered["read"] && reads < 2:
					if !strings.Contains(system, fmt.Sprintf("read は残り %d 回", turnReadLimit(tc.always || tc.deep)-reads)) ||
						!strings.Contains(system, fmt.Sprintf("Web検索: 残り %d 回", turnSearchLimit(tc.always || tc.deep))) {
						t.Error("research budget instructions changed")
					}
					reads++
					if reads == 1 {
						return `{"steps":[{"type":"tool","tool":"kakeratta_help","args":"{\"path\":\"index.md\"}"}]}`
					}
					if reads == 2 {
						if !strings.Contains(user, "kakeratta specification: index.md") {
							t.Error("next research round lost the previous result")
						}
						return `{"steps":[{"type":"tool","tool":"kakeratta_help","args":"{\"path\":\"architecture.md\"}"}]}`
					}
					return "read_done"
				case offered["finish_read"]:
					if !tc.split {
						t.Errorf("an ordinary turn read apart from its answer: %v", offered)
					}
					return "read_done"
				case offered["submit_answer"]:
					if offered["read"] == tc.split {
						t.Errorf("answer tools = %v (split %v)", offered, tc.split)
					}
					for _, text := range []string{trigger.Text, "kakeratta specification: index.md", "kakeratta specification: architecture.md"} {
						if !strings.Contains(user, text) {
							t.Errorf("answer lost research context %q", text)
						}
					}
					return `{"text":"根拠を踏まえた回答","sources":[]}`
				case offered["submit_schedule"]:
					return `{"operations":[],"consistent":true,"reason":"約束なし"}`
				default:
					t.Errorf("unexpected phase: %v", offered)
					return ""
				}
			}
			client, settings := r.clientOf()
			settings.HighModel, settings.MiddleModel, settings.LowModel = tc.high, tc.middle, tc.low
			settings.Search.APIKey = "test-key"
			var calls []string
			client.Observe = func(call llm.CallRecord) {
				calls = append(calls, call.Purpose+":"+call.Model)
			}
			entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", false, client, settings, r.registry(settings))
			if entry == nil || entry.Text != "根拠を踏まえた回答" {
				t.Fatalf("answer = %+v", entry)
			}
			want := []string{"answer:" + tc.wantAnswer, "answer:" + tc.wantAnswer, "answer:" + tc.wantAnswer, "plan_schedule:" + tc.wantSuper, "memory_select:" + tc.wantSuper, "memory_update:" + tc.wantSuper}
			if tc.split {
				// Two reads and finish_read on Super, then one Ultra answer.
				want = []string{"research:" + tc.wantSuper, "research:" + tc.wantSuper, "research:" + tc.wantSuper, "answer:" + tc.wantAnswer, "plan_schedule:" + tc.wantSuper, "memory_select:" + tc.wantSuper, "memory_update:" + tc.wantSuper}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}

// A persona without lookup tools of its own - the infographic, working from
// what the thread already holds - answers on Ultra in one call, with no Super
// call that would only finish reading.
func TestUltraPersonaWithoutLookupToolsAnswersInOneCall(t *testing.T) {
	r, s, f := setup(t, nil, "infographic")
	skill, _ := r.catalogue.Find(context.Background(), "infographic")
	if !skill.AlwaysUltra {
		t.Fatal("infographic is expected to always think deeply")
	}
	f.phaseReply = func(_, _ string, offered map[string]bool) string {
		if offered["finish_read"] {
			t.Errorf("reading was split from the answer: %v", offered)
			return "read_done"
		}
		return `{"text":"# 📊 図解\n要点です。","sources":[]}`
	}
	client, settings := r.clientOf()
	settings.HighModel, settings.MiddleModel = "ultra", "super"
	var calls []string
	client.Observe = func(call llm.CallRecord) {
		if call.Purpose == "answer" || call.Purpose == "research" {
			calls = append(calls, call.Purpose+":"+call.Model)
		}
	}
	trigger := post(t, s, "これを図解して")
	if entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", false, client, settings, r.registry(settings)); entry == nil {
		t.Fatal("no answer")
	}
	if want := []string{"answer:ultra"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCheckInsIgnoreHistoricalDepthPhrases(t *testing.T) {
	for _, tc := range []struct {
		name, instructions  string
		always              bool
		wantModel, wantSlot string
	}{
		{"daily", dailyShareInstructions, false, "super-test", "super"},
		{"monthly", monthlyShareInstructions, false, "super-test", "super"},
		{"daily-always", dailyShareInstructions, true, "ultra-test", "ultra"},
		{"monthly-always", monthlyShareInstructions, true, "ultra-test", "ultra"},
		{"user-request", "", false, "ultra-test", "ultra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, nil, "infographic")
			skill, _ := r.catalogue.Find(context.Background(), "infographic")
			skill.AlwaysUltra = tc.always
			trigger := timeline.Entry{Speaker: "system", Text: "過去の発言: 詳しく教えて。実装の深掘りと活用"}
			if tc.instructions == "" {
				trigger = post(t, s, trigger.Text)
			}
			f.phaseReply = func(_, _ string, offered map[string]bool) string {
				if !offered["submit_answer"] {
					t.Fatalf("unexpected phase: %v", offered)
				}
				return `{"text":"回答","sources":[],"remember":false,"schedule":false}`
			}
			client, settings := r.clientOf()
			settings.MiddleModel, settings.HighModel = "super-test", "ultra-test"
			result := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, false, false, tc.instructions, false, client, settings, r.registry(settings))
			if result == nil || result.Model != tc.wantSlot || f.lastModelFor("インフォグラフィック") != tc.wantModel {
				t.Fatalf("result=%+v model=%s", result, f.lastModelFor("インフォグラフィック"))
			}
		})
	}
}
