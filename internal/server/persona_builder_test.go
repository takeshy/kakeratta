package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestPersonaBuilderConversationAndValidation(t *testing.T) {
	s, workspace := app(t)
	reply := `{"message":"どんな話し方がいいですか？","placeholder":"例：友達のように気軽に話してほしい。","draft":null}`
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "availableTools") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "{}"}}}})
			return
		}
		calls++
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
	draft := persona.Skill{ID: "wine", Name: "ぶどう", Domain: "ワイン"}
	raw, _ := json.Marshal(map[string]any{"message": "完成案です", "draft": draft})
	reply = string(raw)
	if w := send(body); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if _, ok := s.catalogue.Find(context.Background(), "wine"); ok {
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
	s, workspace := app(t)
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
	got, _ := s.catalogue.Find(context.Background(), skill.ID)
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

func TestPersonaBuilderReceivesFullPostAndThread(t *testing.T) {
	server, workspace := app(t)
	var captured string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "availableTools") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "{}"}}}})
			return
		}
		captured = request.Messages[len(request.Messages)-1].Content
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"message":"proposal","draft":null}`}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("長い本文", 100) + "末尾の重要条件"
	fullRemark := strings.Repeat("長い返事", 100) + "返事末尾の問題"
	now := time.Now()
	for _, entry := range []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: full, Media: &timeline.Media{Describe: "画像の説明", Text: "画像の文字"}, Sources: []timeline.Source{{Label: "出典名", URL: "https://example.com/source"}}, At: now},
		{ID: "r", Kind: timeline.KindReply, Speaker: timeline.User, Root: "p", ReplyTo: "p", Text: "これも対象に", At: now.Add(time.Second)},
		{ID: "m", Kind: timeline.KindRemark, Speaker: "book", Root: "p", ReplyTo: "r", Text: fullRemark, At: now.Add(2 * time.Second)},
		{ID: "later", Kind: timeline.KindReply, Speaker: timeline.User, Root: "p", ReplyTo: "m", Text: "返事の後で加えた訂正", At: now.Add(3 * time.Second)},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"p", "r", "m", "missing"} {
		captured = ""
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/personas/build", strings.NewReader(`{"id":"book","entryId":"`+id+`","messages":[{"role":"user","content":"条件を直して"}]}`)))
		if id == "missing" {
			if response.Code != http.StatusNotFound || captured != "" {
				t.Fatalf("missing entry: %d %s", response.Code, captured)
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("build: %d %s", response.Code, response.Body.String())
		}
		for _, want := range []string{full, "画像の説明", "画像の文字", "出典名", "https://example.com/source", "これも対象に", "今回の対象発言:\n[" + id + "]"} {
			if !strings.Contains(captured, want) {
				t.Errorf("builder missing %q", want)
			}
		}
		if id == "m" {
			if !strings.Contains(captured, fullRemark) {
				t.Error("the full disputed reply is missing")
			}
			if strings.Contains(captured, "返事の後で加えた訂正") {
				t.Error("reply correction includes context from after the reply")
			}
		} else if !strings.Contains(captured, "返事の後で加えた訂正") {
			t.Error("condition correction lost the current thread context")
		}
	}
}

func TestAddedPersonaStartsActive(t *testing.T) {
	s, workspace := app(t)
	for _, add := range []struct{ path, id string }{{"/api/personas/draft", "wine"}, {"/api/personas/install", "tea"}} {
		raw, _ := json.Marshal(persona.Skill{ID: add.id, Name: add.id, Domain: add.id})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", add.path, strings.NewReader(string(raw))))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) {
			t.Fatalf("%s: %d %s", add.path, w.Code, w.Body.String())
		}
		if !slices.Contains(workspace.Settings().Active, add.id) {
			t.Fatalf("%s: %s not active", add.path, add.id)
		}
	}
}

func TestNewPersonDraftCanPlaceWakes(t *testing.T) {
	s, workspace := app(t)
	draft := persona.Skill{ID: "mother", Name: "母", Domain: "母のこと、または母との出来事。", Kind: persona.KindPerson, Aliases: []string{"お母さん"}}
	raw, _ := json.Marshal(map[string]any{"message": "案です", "draft": draft})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(`{"messages":[{"role":"user","content":"母のことを覚えて"}]}`)))
	var result struct{ Draft persona.Skill }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if slices.Contains(result.Draft.Tools, "route") {
		t.Fatalf("new people should not receive legacy routing by default: %v", result.Draft.Tools)
	}
	for _, name := range personTools {
		if !slices.Contains(result.Draft.Tools, name) {
			t.Fatalf("a new person lacks %s: %v", name, result.Draft.Tools)
		}
	}
}

// Personal facts are part of the saved definition, never a separate post.
func TestPersonDraftCarriesBasicInfo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft persona.Skill
		want  string
	}{
		{"person", persona.Skill{ID: "nagomi", Name: "nagomi", Domain: "nagomi本人のこと、またはnagomiとの出来事。", Kind: persona.KindPerson, Aliases: []string{"なごみ", "恋人"}}, "nagomiは1972/11/10生まれ。タイ料理が好き。"},
		{"subject", persona.Skill{ID: "wine", Name: "wine", Domain: "ワインの話。"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, workspace := app(t)
			tc.draft.BasicInfo = "  nagomiは1972/11/10生まれ。タイ料理が好き。 "
			raw, _ := json.Marshal(map[string]any{"message": "案です", "draft": tc.draft, "checkIn": "monthly"})
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}}}})
			}))
			defer model.Close()
			settings := workspace.ConfiguredSettings()
			settings.BaseURL = model.URL
			if err := workspace.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(`{"messages":[{"role":"user","content":"恋人のnagomiのことを覚えて。1972/11/10生まれ。タイ料理が好き。"}]}`)))
			var result struct {
				Draft   persona.Skill
				CheckIn string
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if result.Draft.BasicInfo != tc.want {
				t.Fatalf("basicInfo = %q, want %q", result.Draft.BasicInfo, tc.want)
			}
			// How often a person is looked back on comes with it; a subject has none.
			if want := map[bool]string{true: "monthly", false: ""}[tc.want != ""]; result.CheckIn != want {
				t.Fatalf("checkIn = %q, want %q", result.CheckIn, want)
			}
		})
	}
}

// The dialog sends the question it opened with ahead of the user's answer. That
// question is what tells the builder a name is a person's, so it reaches the
// model - as context, with the conversation still starting from the user.
func TestPersonaBuilderAcceptsTheDialogsOpeningQuestion(t *testing.T) {
	s, workspace := app(t)
	var got []struct{ Role, Content string }
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "availableTools") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "{}"}}}})
			return
		}
		got = request.Messages
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"message":"どんな関係ですか？","draft":null}`}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	send := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(body)))
		return w
	}
	opening := "誰のことを覚える担当を作りたい？名前や呼び名、あなたとの関係を教えてください。"
	if w := send(`{"messages":[{"role":"assistant","content":"` + opening + `"},{"role":"user","content":"佐藤さん"}]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(got) != 2 || got[0].Role != "system" || !strings.Contains(got[0].Content, opening) || got[1].Role != "user" || got[1].Content != "佐藤さん" {
		t.Fatalf("the model was sent %+v", got)
	}
	// A follow-up keeps the same shape: the opening, then the exchange.
	if w := send(`{"messages":[{"role":"assistant","content":"` + opening + `"},{"role":"user","content":"佐藤さん"},{"role":"assistant","content":"{}"},{"role":"user","content":"大学の友人"}]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(got) != 4 || got[1].Role != "user" || got[3].Content != "大学の友人" {
		t.Fatalf("the model was sent %+v", got)
	}
	// Only the opening may come from the assistant ahead of the user.
	for _, body := range []string{
		`{"messages":[{"role":"assistant","content":"` + opening + `"}]}`,
		`{"messages":[{"role":"assistant","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`,
		`{"messages":[{"role":"assistant","content":" "},{"role":"user","content":"c"}]}`,
	} {
		if w := send(body); w.Code != 400 {
			t.Fatalf("%s was accepted: %d", body, w.Code)
		}
	}
}

func TestPersonBasicInformationSavedAndEditableWithoutPosting(t *testing.T) {
	s, workspace := app(t)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := `{"events":[{"key":"birthday","name":"誕生日","annual":true,"month":11,"day":10,"at":"","cancel":false}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	skill := persona.Skill{ID: "sato", Name: "佐藤さん", Domain: "大学の友人", Kind: persona.KindPerson, BasicInfo: "11月10日生まれ。タイ料理が好き。"}
	save := func(method, path string) {
		raw, _ := json.Marshal(skill)
		response := httptest.NewRecorder()
		s.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(string(raw))))
		if response.Code != 200 {
			t.Fatalf("save: %d %s", response.Code, response.Body.String())
		}
		restored, exists := persona.NewCatalogue(workspace).Find(context.Background(), skill.ID)
		if !exists || restored.BasicInfo != skill.BasicInfo {
			t.Fatalf("restored=%+v, exists=%v", restored, exists)
		}
	}
	save("POST", "/api/personas/draft")
	if schedules, err := workspace.Schedules(skill.ID); err != nil || len(schedules) != 2 {
		t.Fatalf("birthday schedules = %+v, %v", schedules, err)
	}
	skill.BasicInfo = "11月10日生まれ。辛くないタイ料理が好き。"
	save("PUT", "/api/personas/sato")
	skill.BasicInfo = ""
	save("PUT", "/api/personas/sato")
	if schedules, err := workspace.Schedules(skill.ID); err != nil || len(schedules) != 0 {
		t.Fatalf("cleared schedules = %+v, %v", schedules, err)
	}
	entries, err := workspace.Timeline()
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected post: %+v, %v", entries, err)
	}
	paths, err := workspace.NotePaths(skill.ID)
	if err != nil || len(paths) != 0 {
		t.Fatalf("unexpected notes: %+v, %v", paths, err)
	}
}

func TestPersonBuilderPreservesBasicInformationUnlessExplicitlyChanged(t *testing.T) {
	for _, tc := range []struct {
		name        string
		present     bool
		value, want string
	}{
		{name: "omitted", want: "11月10日生まれ。"},
		{name: "modified", present: true, value: "11月11日生まれ。", want: "11月11日生まれ。"},
		{name: "cleared", present: true, value: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, workspace := app(t)
			current := persona.Skill{ID: "sato", Name: "佐藤さん", Domain: "友人", Kind: persona.KindPerson, BasicInfo: "11月10日生まれ。"}
			draft := map[string]any{"id": current.ID, "name": current.Name, "domain": current.Domain, "kind": current.Kind}
			if tc.present {
				draft["basicInfo"] = tc.value
			}
			raw, _ := json.Marshal(map[string]any{"message": "修正案です", "draft": draft})
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct{ Messages []struct{ Content string } }
				_ = json.NewDecoder(r.Body).Decode(&request)
				if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].Content, "availableTools") {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "{}"}}}})
					return
				}
				if !strings.Contains(request.Messages[0].Content, current.BasicInfo) {
					t.Error("current basic information missing from builder context")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}}}})
			}))
			defer model.Close()
			settings := workspace.ConfiguredSettings()
			settings.BaseURL = model.URL
			if err := workspace.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"current": current, "messages": []map[string]string{{"role": "user", "content": "呼び名を追加して"}}})
			response := httptest.NewRecorder()
			s.ServeHTTP(response, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(string(body))))
			var result struct{ Draft persona.Skill }
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
				t.Fatalf("build: %d %s", response.Code, response.Body.String())
			}
			if result.Draft.BasicInfo != tc.want {
				t.Fatalf("basicInfo=%q, want=%q", result.Draft.BasicInfo, tc.want)
			}
		})
	}
}

// Whether a persona is a person is settled when it is made: saving one as the
// other is refused, and the AI revising one keeps its kind.
func TestAPersonasKindDoesNotChange(t *testing.T) {
	s, workspace := app(t)
	put := func(skill persona.Skill) int {
		raw, _ := json.Marshal(skill)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("PUT", "/api/personas/"+skill.ID, strings.NewReader(string(raw))))
		return w.Code
	}

	// A subject cannot become a person.
	subject := persona.Builtins[0]
	subject.Kind = persona.KindPerson
	subject.Aliases = []string{"たべ"}
	if code := put(subject); code != http.StatusBadRequest {
		t.Fatalf("a subject saved as a person = %d", code)
	}

	// A person cannot become a subject.
	person := persona.Skill{ID: "sato", Name: "佐藤さん", Domain: "友人", Kind: persona.KindPerson, Aliases: []string{"サトウ"}}
	raw, _ := json.Marshal(person)
	if _, err := s.catalogue.Install(raw, s.knownTool); err != nil {
		t.Fatal(err)
	}
	subjectAgain := persona.Skill{ID: "sato", Name: "佐藤さん", Domain: "友人"}
	if code := put(subjectAgain); code != http.StatusBadRequest {
		t.Fatalf("a person saved as a subject = %d", code)
	}
	if got, _ := s.catalogue.Find(context.Background(), "sato"); !got.IsPerson() {
		t.Fatal("the refused save changed the persona")
	}

	// The AI revising a person keeps it a person, whatever it proposes.
	proposal, _ := json.Marshal(map[string]any{"message": "直しました", "draft": map[string]any{"id": "sato", "name": "佐藤さん", "domain": "友人", "tone": "やさしく", "memory": "Fact"}})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(proposal)}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"id": "sato", "messages": []map[string]string{{"role": "user", "content": "話し方をやさしく"}}})
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("POST", "/api/personas/build", strings.NewReader(string(body))))
	var result struct{ Draft persona.Skill }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("build: %d %s", response.Code, response.Body.String())
	}
	if !result.Draft.IsPerson() {
		t.Fatalf("the AI turned a person into a subject: %+v", result.Draft)
	}
}
