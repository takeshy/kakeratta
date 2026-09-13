package store

import "testing"

func TestNebiusDefaultModelMigration(t *testing.T) {
	for _, vision := range []string{legacyNemotronNano, NemotronNano, ""} {
		t.Run(vision, func(t *testing.T) {
			s := open(t)
			settings := defaultSettings()
			settings.LowModel, settings.MiddleModel, settings.HighModel, settings.RouteModel = legacyNemotronNano, legacyNemotronNano, legacyNemotronNano, legacyNemotronNano
			settings.VisionModel = vision
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			got := s.Settings()
			for _, model := range []string{got.LowModel, got.MiddleModel, got.HighModel, got.RouteModel} {
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
