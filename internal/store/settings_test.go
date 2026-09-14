package store

import "testing"

func TestNebiusDefaultModelMigration(t *testing.T) {
	for _, vision := range []string{legacyNemotronNano, "nvidia/nvidia-nemotron-3-nano-30b-a3b", "Qwen/Qwen2.5-VL-72B-Instruct", NemotronNano, ""} {
		t.Run(vision, func(t *testing.T) {
			s := open(t)
			settings := defaultSettings()
			settings.LowModel, settings.MiddleModel, settings.HighModel, settings.RouteModel = legacyNemotronNano, legacyNemotronNano, legacyNemotronNano, legacyNemotronNano
			settings.VisionModel = vision
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			got := s.Settings()
			if got.LowModel != NemotronLightning || got.RouteModel != NemotronLightning {
				t.Fatal("light models not upgraded")
			}
			for _, model := range []string{got.MiddleModel, got.HighModel} {
				if model != NemotronNano {
					t.Fatalf("old text model not migrated: %s", model)
				}
			}
			if got.VisionModel != NebiusVision {
				t.Fatalf("vision model = %s", got.VisionModel)
			}
			if vision == "" && s.ConfiguredSettings().VisionModel != "" {
				t.Fatal("empty selection became explicit")
			}
		})
	}
}

func TestModelMigrationPreservesCustomSettings(t *testing.T) {
	for _, endpoint := range []string{NebiusBaseURL, "https://custom.example/v1"} {
		for _, provider := range []Provider{ProviderNebius, ProviderOpenAI} {
			settings := defaultSettings()
			settings.Provider, settings.BaseURL = provider, endpoint
			settings.LowModel = "my-text-model"
			settings.VisionModel = "my-vision-model"
			got := normaliseConfiguredSettings(settings)
			if got.LowModel != settings.LowModel || got.VisionModel != settings.VisionModel {
				t.Fatal("custom model changed")
			}
			if provider != ProviderNebius || endpoint != NebiusBaseURL {
				settings.LowModel, settings.VisionModel = legacyNemotronNano, legacyNemotronNano
				got = normaliseConfiguredSettings(settings)
				if got.LowModel != legacyNemotronNano || got.VisionModel != legacyNemotronNano {
					t.Fatal("custom provider/endpoint changed")
				}
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

func TestIntermediateNanoDefaultIsMigrated(t *testing.T) {
	settings := defaultSettings()
	settings.LowModel = "nvidia/nvidia-nemotron-3-nano-30b-a3b"
	settings.RouteModel = settings.LowModel
	got := normaliseConfiguredSettings(settings)
	if got.LowModel != NemotronLightning || got.RouteModel != NemotronLightning {
		t.Fatal("intermediate default was not migrated")
	}
}

func TestHighTierDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, high, want string }{
		{"existing empty Nebius", NebiusBaseURL, "", NemotronUltra},
		{"explicit selection", NebiusBaseURL, "chosen-model", "chosen-model"},
		{"custom endpoint fallback", "https://custom.example/v1", "", "chosen-middle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			settings := s.ConfiguredSettings()
			settings.BaseURL, settings.HighModel, settings.MiddleModel = tc.endpoint, tc.high, "chosen-middle"
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if got := s.Settings().HighModel; got != tc.want {
				t.Fatalf("high = %q, want %q", got, tc.want)
			}
			if got := s.ConfiguredSettings().HighModel; got != tc.high {
				t.Fatalf("editable high changed to %q", got)
			}
		})
	}
}

func TestToolSettingsInOldDocumentsAreIgnored(t *testing.T) {
	s := open(t)
	// A settings document written when users could point the tools elsewhere.
	if err := writeJSON(s.Root()+"/settings.json", map[string]any{"lowModel": "x", "search": map[string]any{"provider": "brave", "apiKey": "k"}, "places": map[string]any{"routeUrl": "https://custom.example/car"}}); err != nil {
		t.Fatal(err)
	}
	if settings := s.Settings(); settings.LowModel != "x" || settings.Search.APIKey != "" {
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

func TestLightningDefaultsAndNanoMigration(t *testing.T) {
	s := open(t)
	if got := s.Settings(); got.LowModel != NemotronLightning || got.RouteModel != NemotronLightning {
		t.Fatal("wrong defaults")
	}
	for _, old := range []string{NemotronNano, legacyNemotronNano, "nvidia/nvidia-nemotron-3-nano-30b-a3b"} {
		settings := defaultSettings()
		settings.LowModel, settings.RouteModel = old, old
		if err := writeJSON(s.Root()+"/settings.json", settings); err != nil {
			t.Fatal(err)
		}
		got := s.ConfiguredSettings()
		if got.LowModel != NemotronLightning || got.RouteModel != NemotronLightning {
			t.Fatal("stored Nano was not upgraded")
		}
		if got.MiddleModel != NemotronSuper || got.HighModel != NemotronUltra || got.VisionModel != NebiusVision {
			t.Fatal("unrelated model changed")
		}
	}
	settings := defaultSettings()
	settings.LowModel, settings.RouteModel = "custom-low", "custom-router"
	got := normaliseConfiguredSettings(settings)
	if got.LowModel != "custom-low" || got.RouteModel != "custom-router" {
		t.Fatal("custom models changed")
	}
}
