package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/avatar"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func profilePhoto(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 128, 128)), nil); err != nil {
		t.Fatal(err)
	}
	return avatar.Prefix + base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestProfileSettingsPersistenceAndValidation(t *testing.T) {
	s, workspace := app(t)
	initial := workspace.ConfiguredSettings()
	initial.Active = nil
	_ = workspace.SaveSettings(initial)
	expected := store.Profile{Name: "佐藤", Icon: "🌸", Avatar: profilePhoto(t)}
	save := func(profile store.Profile) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"profile": map[string]string{"name": profile.Name, "icon": profile.Icon, "avatar": profile.Avatar}})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(raw)))
		return w
	}
	if w := save(expected); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got := workspace.ConfiguredSettings().Profile; got != expected {
		t.Fatalf("profile not persisted: %v", got)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var settings store.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil || settings.Profile != expected {
		t.Fatal("profile not returned by API")
	}
	for _, bad := range []store.Profile{{Name: strings.Repeat("あ", 41)}, {Icon: "abcdef"}, {Avatar: "https://example.com/image.jpg"}, {Avatar: "data:image/svg+xml;base64,PHN2Zy8+"}} {
		if w := save(bad); w.Code != 400 {
			t.Fatalf("invalid profile accepted: %d", w.Code)
		}
		if workspace.ConfiguredSettings().Profile != expected {
			t.Fatal("invalid profile replaced saved profile")
		}
	}
	if w := save(store.Profile{}); w.Code != 200 || workspace.ConfiguredSettings().Profile != (store.Profile{}) {
		t.Fatal("profile could not be reset")
	}
}

func TestPersonaPhotoPersistsAndInvalidPhotoIsRejected(t *testing.T) {
	s, workspace := app(t)
	initial := workspace.ConfiguredSettings()
	initial.Active = nil
	_ = workspace.SaveSettings(initial)
	skill := persona.Builtins[0]
	skill.Avatar = profilePhoto(t)
	save := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(skill)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/personas/"+skill.ID, bytes.NewReader(raw)))
		return w
	}
	if w := save(); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	saved, ok := s.catalogue.Find(context.Background(), skill.ID)
	if !ok || saved.Avatar != skill.Avatar {
		t.Fatal("persona photo not persisted")
	}
	skill.Avatar = "data:image/svg+xml;base64,PHN2Zy8+"
	if w := save(); w.Code != 400 {
		t.Fatalf("unsafe avatar accepted: %d", w.Code)
	}
}

func TestPersonaAIKeepsPhotoWithoutSendingItToTheModel(t *testing.T) {
	s, workspace := app(t)
	skill := persona.Builtins[0]
	skill.Avatar = profilePhoto(t)
	originalPhoto := skill.Avatar
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "data:image/") || strings.Contains(message.Content, "\"avatar\"") {
				t.Error("photo sent to text model")
			}
		}
		response := skill
		response.Avatar = ""
		raw, _ := json.Marshal(map[string]any{"message": "案です", "draft": response})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}}}})
	}))
	defer model.Close()
	settings := workspace.ConfiguredSettings()
	settings.BaseURL = model.URL
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	previous, _ := json.Marshal(map[string]any{"draft": skill})
	raw, _ := json.Marshal(map[string]any{"id": skill.ID, "current": skill, "messages": []map[string]string{{"role": "user", "content": "変えて"}, {"role": "assistant", "content": string(previous)}, {"role": "user", "content": "短く"}}})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/personas/build", bytes.NewReader(raw)))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var response struct{ Draft persona.Skill }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Draft.Avatar != originalPhoto {
		t.Fatal("AI draft lost photo")
	}
}
