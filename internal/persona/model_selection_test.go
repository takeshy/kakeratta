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

func TestTurnModelsSeparateResearchFromAnswer(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		deep, always          bool
		high, middle, low     string
		wantSuper, wantAnswer string
	}{
		{"ordinary", false, false, "ultra", "super", "lightning", "super", "super"},
		{"deep", true, false, "ultra", "super", "lightning", "super", "ultra"},
		{"always", false, true, "ultra", "super", "lightning", "super", "ultra"},
		{"both", true, true, "ultra", "super", "lightning", "super", "ultra"},
		{"no-high", true, false, "", "super", "lightning", "super", "super"},
		{"low-only", false, true, "", "", "lightning", "lightning", "lightning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, nil, "gourmet")
			skill, _ := r.catalogue.Find(context.Background(), "gourmet")
			skill.AlwaysUltra = tc.always
			skill.Tools = []string{"kakeratta_help", "schedule", "web_search"}
			trigger := post(t, s, "調査した根拠で答えて")
			reads := 0
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				switch {
				case offered["finish_read"]:
					if !strings.Contains(system, fmt.Sprintf("最大%d回", turnReadLimit(tc.deep || tc.always))) ||
						!strings.Contains(system, fmt.Sprintf("Web検索: 残り %d 回", turnSearchLimit(tc.deep || tc.always))) {
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
				case offered["submit_answer"]:
					if len(offered) != 1 {
						t.Errorf("answer may call research tools: %v", offered)
					}
					for _, text := range []string{trigger.Text, "kakeratta specification: index.md", "kakeratta specification: architecture.md", "finish_read", "調査を終えました"} {
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
			entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", tc.deep, client, settings, r.registry(settings))
			if entry == nil || entry.Text != "根拠を踏まえた回答" {
				t.Fatalf("answer = %+v", entry)
			}
			want := []string{"research:" + tc.wantSuper, "research:" + tc.wantSuper, "research:" + tc.wantSuper, "answer:" + tc.wantAnswer, "plan_schedule:" + tc.wantSuper, "memory_update:" + tc.wantSuper}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}
