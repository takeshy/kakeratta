package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestBuiltinDirectDeliverySearchesTimelineAfterSettingsChange(t *testing.T) {
	r, s, _ := setup(t, nil) // No active personas are required.
	evidence := post(t, s, "横浜を出発して大阪へ向かった")
	if err := s.WriteNote("gourmet", "Memo.md", "移動前に横浜で昼食"); err != nil {
		t.Fatal(err)
	}
	trigger := post(t, s, "@ai When did I last leave Yokohama?")
	trigger.Language = "en"
	if err := s.UpdateEntry(trigger); err != nil {
		t.Fatal(err)
	}
	plan, routing, err := r.prepareEntry(context.Background(), trigger.ID, nil, false)
	if err != nil || plan == nil || routing != nil {
		t.Fatalf("plan=%+v routing=%+v err=%v", plan, routing, err)
	}
	// Simulate a persisted delivery resuming after a connection was enabled.
	r.SetAgentName(func() string { return "Hermes" })
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []llm.Message }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		calls++
		if calls == 1 {
			writeResearchReply(w, "read", map[string]any{"steps": []any{
				map[string]any{"type": "tool", "tool": "timeline_search", "args": map[string]any{"from": evidence.At.Add(-time.Hour).Format(time.RFC3339), "to": evidence.At.Add(time.Hour).Format(time.RFC3339)}},
				map[string]any{"type": "tool", "tool": "read_note", "args": map[string]any{"persona": "gourmet", "path": "Memo.md"}},
			}})
			return
		}
		if calls > 2 {
			t.Error("unexpected model call")
		}
		if !strings.Contains(body.Messages[len(body.Messages)-1].Content, evidence.Text) {
			t.Error("timeline evidence did not reach built-in model")
		}
		if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "移動前に横浜で昼食") {
			t.Error("another persona's note did not reach the direct model")
		}
		writeResearchReply(w, submitAnswer, map[string]any{"text": "The departure is recorded in this post.", "sources": []string{evidence.ID}, "unrelated": false})
	}))
	defer server.Close()
	r.clientOf = func() (*llm.Client, store.Settings) { return llm.New(server.URL, "test"), s.Settings() }
	if err := r.deliverEntry(context.Background(), trigger.ID, nil, *plan, false); err != nil {
		t.Fatal(err)
	}
	answers := entriesBy(s, timeline.Agent)
	if calls != 2 || len(answers) != 1 || answers[0].Language != "en" || answers[0].Model != "super" || len(answers[0].Sources) == 0 {
		t.Fatalf("calls=%d answers=%+v", calls, answers)
	}
	if speakerName(answers[0], map[string]string{timeline.Agent: "Hermes"}) != "Built-in AI" {
		t.Fatal("built-in answer was named as the external connection")
	}
	if len(r.Requests()) != 0 {
		t.Fatal("built-in delivery switched to external requests")
	}
}

func TestBuiltinAgentModels(t *testing.T) {
	for _, tc := range []struct {
		text, model, slot string
	}{
		{"@ai 最近いつ横浜から離れたっけ？", store.NemotronSuperVL, "super"},
		{"@ai 詳しく教えて", store.NemotronUltra, "ultra"},
		{"@ai explain in detail", store.NemotronUltra, "ultra"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r, s, stub := setup(t, func(string, string) string {
				return `{"action":"speak","text":"確認しました"}`
			})
			entry := post(t, s, tc.text)
			r.react(entry.ID)
			answers := entriesBy(s, timeline.Agent)
			wantCalls := 1
			if tc.slot == "ultra" {
				wantCalls = 2
			}
			if len(stub.models) != wantCalls || stub.models[0] != store.NemotronSuperVL || stub.models[len(stub.models)-1] != tc.model || len(answers) != 1 || answers[0].Model != tc.slot {
				t.Fatalf("models=%v answers=%+v; want model=%s slot=%s", stub.models, answers, tc.model, tc.slot)
			}
			if s.Settings().MiddleModel == store.NemotronSuperVL {
				t.Fatal("direct-agent model choice changed the workspace settings")
			}
		})
	}
}

func TestBuiltinAgentReadsHelpForAppQuestion(t *testing.T) {
	r, s, _ := setup(t, nil)
	trigger := post(t, s, "@ai 過去の投稿はどこから検索できますか？")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model    string
			Messages []llm.Message
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != store.NemotronSuperVL {
			t.Errorf("help model = %s", body.Model)
		}
		calls++
		switch calls {
		case 1:
			if !strings.Contains(body.Messages[0].Content, "その場合だけ") {
				t.Error("conditional support instructions missing")
			}
			writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "kakeratta_help", Args: `{"path":"index.md"}`}}})
		case 2:
			if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "kakeratta specification: index.md") {
				t.Error("index was not read")
			}
			writeResearchReply(w, "read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "kakeratta_help", Args: `{"path":"frontend.md"}`}}})
		default:
			if calls != 3 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "kakeratta specification: frontend.md") {
				t.Error("relevant chapter was not read")
			}
			writeResearchReply(w, submitAnswer, Answer{Text: "検索ボタンから過去の投稿を検索できます。", Sources: []string{}})
		}
	}))
	defer server.Close()
	r.clientOf = func() (*llm.Client, store.Settings) { return llm.New(server.URL, "test"), s.Settings() }
	r.react(trigger.ID)
	answers := entriesBy(s, timeline.Agent)
	if calls != 3 || len(answers) != 1 || answers[0].ReplyTo != trigger.ID || len(s.Memory(timeline.Agent).Notes) != 0 {
		t.Fatalf("calls=%d answers=%+v", calls, answers)
	}
	if schedules, err := s.Schedules(""); err != nil || len(schedules) != 0 {
		t.Fatalf("schedules=%+v err=%v", schedules, err)
	}
}
