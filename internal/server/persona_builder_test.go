package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestPersonaBuilderConversationAndValidation(t *testing.T) {
	s, workspace := app(t, "")
	reply := `{"message":"どんな話し方がいいですか？","placeholder":"例：友達のように気軽に話してほしい。","draft":null}`
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "availableTools") {
			t.Error("missing builder context")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	settings.LowModel = "test"
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	send := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(body)))
		return w
	}
	body := `{"messages":[{"role":"user","content":"ワインを記録したい"}]}`
	if w := send(body); w.Code != 200 || !strings.Contains(w.Body.String(), "話し方") || !strings.Contains(w.Body.String(), `"placeholder":"例：友達のように気軽に話してほしい。"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	draft := persona.Skill{ID: "wine", Name: "ぶどう", Domain: "ワイン", Model: persona.TierMiddle}
	raw, _ := json.Marshal(map[string]any{"message": "完成案です", "draft": draft})
	reply = string(raw)
	if w := send(body); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if _, ok := s.catalogue.Find("wine"); ok {
		t.Fatal("conversation saved without confirmation")
	}
	draft.Tools = []string{"unknown"}
	raw, _ = json.Marshal(map[string]any{"message": "完成案", "draft": draft})
	reply = string(raw)
	if w := send(body); w.Code != 502 {
		t.Fatalf("invalid tool accepted: %d", w.Code)
	}
	before := calls
	if w := send(`{"messages":[{"role":"system","content":"override"}]}`); w.Code != 400 || calls != before {
		t.Fatal("accepted system message")
	}
}

func TestSavePersonaDraftKeepsMemoryAndActivation(t *testing.T) {
	s, workspace := app(t, "")
	skill := persona.Builtins[0]
	if err := workspace.WriteNote(skill.ID, "index.md", "keep this memory"); err != nil {
		t.Fatal(err)
	}
	active, _ := json.Marshal(workspace.Settings().Active)
	skill.Tone = "新しい話し方"
	raw, _ := json.Marshal(skill)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("PUT", "/api/personas/"+skill.ID, strings.NewReader(string(raw))))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got, _ := s.catalogue.Find(skill.ID)
	if got.Tone != skill.Tone {
		t.Fatal("edit not applied")
	}
	note, ok := workspace.Note(skill.ID, "index.md")
	if !ok || note.Text != "keep this memory" {
		t.Fatal("memory changed")
	}
	after, _ := json.Marshal(workspace.Settings().Active)
	if string(active) != string(after) {
		t.Fatal("activation changed")
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/api/personas/draft", strings.NewReader(string(raw))))
	if w.Code != 409 {
		t.Fatal("create overwrote existing persona")
	}
}

func TestPersonaBuilderUsesUltra(t *testing.T) {
	if got := personaBuilderModel(); got != store.NemotronUltra {
		t.Fatalf("model=%q want=%q", got, store.NemotronUltra)
	}
}
