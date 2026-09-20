package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestDictationFormattingBeforePersistence(t *testing.T) {
	for _, tc := range []struct {
		name               string
		enabled, auto, bad bool
	}{
		{"auto on", true, true, false}, {"auto off", false, true, false},
		{"manual on", true, false, false}, {"failed format", true, true, true},
	} {
		for _, path := range []string{"/api/post", "/api/reply"} {
			t.Run(tc.name+path, func(t *testing.T) {
				calls := 0
				model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var request struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Model != "light-model" {
						t.Errorf("model = %q", request.Model)
					}
					content := `{"text":"明日は10時に行きます。"}`
					if tc.bad {
						content = `{"text":""}`
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
				}))
				defer model.Close()
				server, s := app(t)
				settings := s.ConfiguredSettings()
				settings.BaseURL, settings.LowModel, settings.Active = model.URL, "light-model", []string{}
				settings.Speech.FormatBeforeSend = tc.enabled
				if err := s.SaveSettings(settings); err != nil {
					t.Fatal(err)
				}
				if err := s.Append(timeline.Entry{ID: "parent", Root: "parent", Kind: timeline.KindPost, Speaker: timeline.User, Text: "parent"}); err != nil {
					t.Fatal(err)
				}
				raw := "えー明日は10時に行きます"
				body, _ := json.Marshal(map[string]any{"text": raw, "replyTo": "parent", "speechAutoSend": tc.auto})
				response := httptest.NewRecorder()
				server.ServeHTTP(response, httptest.NewRequest("POST", path, bytes.NewReader(body)))
				wantCalls := 0
				if tc.auto && tc.enabled {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatalf("calls = %d", calls)
				}
				if tc.bad {
					if response.Code != 502 {
						t.Fatalf("status = %d", response.Code)
					}
					page, err := s.TimelinePage(t.Context(), 20, "")
					if err != nil {
						t.Fatal(err)
					}
					if len(page.Entries) != 1 || page.Entries[0].ID != "parent" {
						t.Fatalf("failed formatting persisted a post: %+v", page.Entries)
					}
					return
				}
				if response.Code != 200 {
					t.Fatalf("status = %d: %s", response.Code, response.Body.String())
				}
				var entry timeline.Entry
				if err := json.Unmarshal(response.Body.Bytes(), &entry); err != nil {
					t.Fatal(err)
				}
				want := raw
				if wantCalls == 1 {
					want = "明日は10時に行きます。"
				}
				saved, ok := s.Entry(entry.ID)
				if !ok || saved.Text != want {
					t.Fatalf("saved = %+v", saved)
				}
			})
		}
	}
}

func TestLegacySpeechSettingsUseBrowser(t *testing.T) {
	_, s := app(t)
	settings := s.ConfiguredSettings()
	settings.Speech.Provider = "live"
	settings.Speech.FormatBeforeSend = true
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := s.ConfiguredSettings().Speech; got.Provider != "browser" || !got.FormatBeforeSend {
		t.Fatalf("speech = %+v", got)
	}
	if got := s.Settings().HighModel; got != store.NemotronUltra {
		t.Fatalf("high = %q", got)
	}
}
