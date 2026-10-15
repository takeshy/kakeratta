package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestOpenRouterVisionModelIsSavedAndSelectedIndependently(t *testing.T) {
	s, workspace := app(t)
	initial := workspace.ConfiguredSettings()
	initial.APIKey = "nebius-key"
	initial.VisionModel = "nebius-vision"
	if err := workspace.SaveSettings(initial); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "  ", "provider/chosen-vision"} {
		body, _ := json.Marshal(map[string]any{"openRouterMode": true, "openRouterApiKey": "router-key", "openRouterVisionModel": model})
		saved := httptest.NewRecorder()
		s.ServeHTTP(saved, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(string(body))))
		if saved.Code != 200 {
			t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
		}
		want := model
		if strings.TrimSpace(model) == "" {
			want = store.OpenRouterVision
		}
		var editable store.Settings
		if err := json.Unmarshal(saved.Body.Bytes(), &editable); err != nil {
			t.Fatal(err)
		}
		if editable.OpenRouterVisionModel != want || editable.VisionModel != "nebius-vision" || editable.OpenRouterAPIKey != "********" {
			t.Fatalf("saved settings = %+v", editable)
		}
		client, effective := s.client()
		if client.BaseURL != store.OpenRouterBaseURL || client.APIKey != "router-key" || effective.VisionModel != want {
			t.Fatalf("OpenRouter vision = %q at %s", effective.VisionModel, client.BaseURL)
		}
		// Disabling and re-enabling the provider must keep both choices intact.
		for _, enabled := range []bool{false, true} {
			next := workspace.ConfiguredSettings()
			next.OpenRouterMode = enabled
			if err := workspace.SaveSettings(next); err != nil {
				t.Fatal(err)
			}
			client, effective = s.client()
			expected := "nebius-vision"
			if enabled {
				expected = want
			}
			if effective.VisionModel != expected || workspace.ConfiguredSettings().OpenRouterVisionModel != want {
				t.Fatalf("enabled=%v: vision=%q", enabled, effective.VisionModel)
			}
		}
	}
}
