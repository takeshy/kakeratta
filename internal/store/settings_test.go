package store

import "testing"

func TestDefaultPersonasAreActiveAndSavedChoicesArePreserved(t *testing.T) {
	s := open(t)
	if got := s.Settings().Active; len(got) != 3 || got[0] != "routine" || got[1] != "support" || got[2] != "infographic" {
		t.Fatalf("default active personas = %v", got)
	}
	for _, active := range [][]string{nil, {}, {"gourmet"}} {
		settings := s.ConfiguredSettings()
		settings.Active = active
		if err := s.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		got := s.Settings().Active
		if len(got) != len(active) || (len(active) > 0 && got[0] != active[0]) {
			t.Fatalf("saved active personas = %v, want %v", got, active)
		}
	}
}

// Nebius' own endpoint runs the shipped models whatever an older version
// saved, and saves none of them, so a later version's defaults apply at once.
// Custom endpoints and providers keep their models.
func TestNebiusModelsAreShippedAndNotStored(t *testing.T) {
	s := open(t)
	settings := defaultSettings()
	settings.LowModel, settings.MiddleModel, settings.HighModel = "old-low", NemotronSuperVL, ""
	settings.VisionModel, settings.RouterModel = "old-vision", NemotronSuperVL
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := readJSON(s.Root()+"/settings.json", &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"lowModel", "middleModel", "highModel", "visionModel", "routerModel"} {
		if raw[key] != "" {
			t.Fatalf("%s was stored: %v", key, raw[key])
		}
	}
	if err := writeJSON(s.Root()+"/settings.json", settings); err != nil {
		t.Fatal(err)
	}
	for _, got := range []Settings{s.ConfiguredSettings(), s.Settings()} {
		if got.LowModel != NemotronLightning || got.MiddleModel != NemotronSuper || got.HighModel != NemotronUltra ||
			got.VisionModel != NebiusVision || got.RouterModel != NemotronSuper {
			t.Fatalf("Nebius models = %+v", got)
		}
	}
	for _, endpoint := range []string{NebiusBaseURL, "https://custom.example/v1"} {
		for _, provider := range []Provider{ProviderNebius, ProviderOpenAI} {
			if provider == ProviderNebius && endpoint == NebiusBaseURL {
				continue
			}
			custom := defaultSettings()
			custom.Provider, custom.BaseURL = provider, endpoint
			custom.LowModel, custom.MiddleModel, custom.VisionModel = "my-low", "my-middle", "my-vision"
			if err := s.SaveSettings(custom); err != nil {
				t.Fatal(err)
			}
			if got := s.ConfiguredSettings(); got.LowModel != "my-low" || got.MiddleModel != "my-middle" || got.VisionModel != "my-vision" {
				t.Fatalf("%s %s: custom models changed: %+v", provider, endpoint, got)
			}
		}
	}
}

func TestNewWorkspaceUsesVisionModel(t *testing.T) {
	got := open(t).Settings()
	if got.VisionModel != NebiusVision || got.VisionModel == got.LowModel {
		t.Fatalf("vision = %s", got.VisionModel)
	}
}

// On a custom endpoint an empty high model falls back to the middle one.
func TestHighTierFallsBackOnCustomEndpoints(t *testing.T) {
	s := open(t)
	settings := s.ConfiguredSettings()
	settings.BaseURL, settings.HighModel, settings.MiddleModel = "https://custom.example/v1", "", "chosen-middle"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().HighModel; got != "chosen-middle" {
		t.Fatalf("high = %q", got)
	}
	if got := s.ConfiguredSettings().HighModel; got != "" {
		t.Fatalf("editable high changed to %q", got)
	}
}

func TestToolSettingsInOldDocumentsAreIgnored(t *testing.T) {
	s := open(t)
	// A settings document written when users could point the tools elsewhere.
	if err := writeJSON(s.Root()+"/settings.json", map[string]any{"lowModel": "x", "search": map[string]any{"provider": "brave", "apiKey": "k"}, "places": map[string]any{"routeUrl": "https://custom.example/car"}}); err != nil {
		t.Fatal(err)
	}
	if settings := s.Settings(); settings.Search.APIKey != "" {
		t.Fatalf("a Brave key survived as a Tavily key: %+v", settings)
	}
	if err := writeJSON(s.Root()+"/settings.json", map[string]any{"search": map[string]any{"provider": "tavily", "baseUrl": "https://api.tavily.com/search", "apiKey": "tvly"}}); err != nil {
		t.Fatal(err)
	}
	if settings := s.Settings(); settings.Search.APIKey != "tvly" || settings.Search.Provider != "" {
		t.Fatalf("a Tavily key was not kept: %+v", settings)
	}
	if err := s.SaveSettings(s.ConfiguredSettings()); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := readJSON(s.Root()+"/settings.json", &raw); err != nil {
		t.Fatal(err)
	}
	if search, _ := raw["search"].(map[string]any); search["provider"] != nil || search["baseUrl"] != nil {
		t.Fatalf("provider or endpoint written back: %v", search)
	}
	if _, ok := raw["places"]; ok {
		t.Fatal("the old places key was written back")
	}
}

// The server's clock is UTC and means nothing to the person reading the
// timeline, so an unusable zone falls back to UTC rather than to the container.
func TestSettingsLocation(t *testing.T) {
	for zone, want := range map[string]string{
		"":           "UTC",
		"Local":      "UTC",
		"bad/zone":   "UTC",
		"Asia/Tokyo": "Asia/Tokyo",
	} {
		if got := (Settings{TimeZone: zone}).Location().String(); got != want {
			t.Errorf("Settings{TimeZone: %q}.Location() = %q, want %q", zone, got, want)
		}
	}
}

func TestLegacyJevMigratesToOpenRouterSelection(t *testing.T) {
	got := normaliseConfiguredSettings(Settings{JevEnabled: true})
	if got.JevEnabled || !got.OpenRouterMode || got.OpenRouterRouterModel != "~typesafe/jev-latest" {
		t.Fatalf("legacy Jev settings were not migrated: %+v", got)
	}
	got.OpenRouterRouterModel = OpenRouterRouter
	if next := normaliseConfiguredSettings(got); next.OpenRouterRouterModel != OpenRouterRouter {
		t.Fatal("legacy Jev must not override a later router selection")
	}
}

func TestOpenRouterVisionDefaultAndPersistence(t *testing.T) {
	s := open(t)
	for _, selection := range []string{"", " ", "provider/vision-model"} {
		settings := s.ConfiguredSettings()
		settings.OpenRouterVisionModel = selection
		settings.BaseURL, settings.VisionModel = "https://custom.example/v1", "main-provider-vision"
		if err := s.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(s.Root())
		if err != nil {
			t.Fatal(err)
		}
		want := selection
		if selection == "" || selection == " " {
			want = OpenRouterVision
		}
		for _, got := range []Settings{reopened.ConfiguredSettings(), reopened.Settings()} {
			if got.OpenRouterVisionModel != want || got.VisionModel != "main-provider-vision" {
				t.Fatalf("vision selections = %q / %q", got.OpenRouterVisionModel, got.VisionModel)
			}
		}
	}
}
