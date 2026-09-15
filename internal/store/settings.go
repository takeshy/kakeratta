package store

import (
	"path/filepath"
	"strings"
)

// Provider is where the models run.
//
// The SPA never talks to any of them: it talks to this app, and this app talks
// to the provider. The keys are here because they cannot be in a browser.
type Provider string

const (
	ProviderNebius Provider = "nebius"
	ProviderOpenAI Provider = "openai-compatible"
)

// Nebius Token Factory is the default. Its inference API is OpenAI-compatible,
// so nothing else in the app changes when it is swapped out.
const (
	NebiusBaseURL = "https://api.tokenfactory.nebius.com/v1"

	// Which model sits in which tier is what carries the contrast, so these are
	// only where it starts. Any OpenAI-compatible name goes in any tier.
	NemotronLightning  = "nvidia/Nemotron-3_5-Lightning"
	NemotronNano       = "nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B"
	NemotronSuper      = "nvidia/nemotron-3-super-120b-a12b"
	NemotronUltra      = "nvidia/Nemotron-3-Ultra-550b-a55b"
	NebiusVision       = "zai-org/GLM-5.3-Flash"
	legacyNemotronNano = "nvidia/nemotron-3-nano-30b-a3b"
)

// Settings are the endpoints, the models and the user's own web search key.
// Where the tools go is not here: the map endpoints and the MCP servers are the
// operator's configuration (see tools.Config), the same for everyone.
type Settings struct {
	Provider Provider `json:"provider"`
	BaseURL  string   `json:"baseUrl"`
	APIKey   string   `json:"apiKey"`

	// Three sizes, in the order they answer. A persona declares which tier it
	// belongs to, and the contrast between the tiers is the experience: the low
	// one reacts quickly and shallowly, and what it said is handed to the ones
	// above it so they have something to pick up on.
	//
	// An empty high tier uses Nemotron Ultra on Token Factory. Other endpoints
	// retain the fallback to the tier below.
	LowModel    string `json:"lowModel"`
	MiddleModel string `json:"middleModel"`
	HighModel   string `json:"highModel"`
	// VisionModel reads a capture once for everyone. On Nebius, an empty
	// selection uses the vision default, never a text-only conversation model.
	VisionModel string `json:"visionModel"`
	// RouteModel decides who a post goes to. It is a light judgement on
	// purpose: it only drops what is plainly none of a persona's business.
	RouteModel string `json:"routeModel"`

	// Active is which personas are in the room, in order.
	//
	// There is no ceiling. The central routing is what makes that safe: a post
	// wakes the few personas it was routed to, not everyone on the list, so
	// adding a domain costs one more line in the routing call rather than one
	// more model call per post.
	Active        []string                `json:"active"`
	DailyCheckIns map[string]DailyCheckIn `json:"dailyCheckIns,omitempty"`

	Search SearchSettings `json:"search"`
	Speech SpeechSettings `json:"speech"`

	ReadAloudRate  float64 `json:"readAloudRate"`
	ReadAloudVoice string  `json:"readAloudVoice"`
}

// SpeechSettings configures browser dictation and optional automatic-send polishing.
// Speech recognition runs only in the browser; there is no transcription API.
type SpeechSettings struct {
	FormatBeforeSend bool   `json:"formatBeforeSend"`
	Provider         string `json:"provider"`
	Language         string `json:"language"`
	SilenceSeconds   int    `json:"silenceSeconds"`
	SendPhrase       string `json:"sendPhrase"`
	Replacements     string `json:"replacements"`
	SymbolCommands   string `json:"symbolCommands"`
}

// SearchSettings is the user's Tavily key. The app makes the call; a skill
// never holds a key. The provider field only survives so a key that was issued
// for Brave, which is no longer supported, can be recognised and dropped.
type SearchSettings struct {
	APIKey   string `json:"apiKey"`
	Provider string `json:"provider,omitempty"`
}

func defaultSettings() Settings {
	return Settings{
		Provider:    ProviderNebius,
		BaseURL:     NebiusBaseURL,
		LowModel:    NemotronLightning,
		MiddleModel: NemotronSuper,
		HighModel:   NemotronUltra,
		VisionModel: NebiusVision,
		RouteModel:  NemotronLightning,
		Speech: SpeechSettings{
			Provider:       "browser",
			Language:       "auto",
			SilenceSeconds: 3,
			SendPhrase:     "終わります",
		},
		ReadAloudRate: 1.2,
	}
}

// EndpointFor is the address a provider listens on by default.
func EndpointFor(provider Provider) string {
	switch provider {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	default:
		return NebiusBaseURL
	}
}

// ConfiguredSettings preserves empty model fields so editing another setting
// does not turn a model fallback into a permanent model selection.
func (s *Store) ConfiguredSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := defaultSettings()
	_ = readJSON(filepath.Join(s.root, "settings.json"), &settings)
	return normaliseConfiguredSettings(settings)
}

func normaliseConfiguredSettings(settings Settings) Settings {
	settings.Speech.Provider = "browser"
	if settings.Search.Provider == "brave" {
		settings.Search.APIKey = ""
	}
	settings.Search.Provider = ""
	if settings.ReadAloudRate <= 0 {
		settings.ReadAloudRate = 1.2
	}
	if settings.Provider == "" {
		settings.Provider = ProviderNebius
	}
	if strings.TrimSpace(settings.BaseURL) == "" {
		settings.BaseURL = EndpointFor(settings.Provider)
	}
	// Repair only the defaults shipped for Nebius. Custom endpoints and model
	// selections belong to the user, even when their names resemble ours.
	if usesNebiusEndpoint(settings) {
		for _, model := range []*string{&settings.LowModel, &settings.MiddleModel, &settings.HighModel, &settings.RouteModel} {
			if name := strings.TrimSpace(*model); name == legacyNemotronNano || name == "nvidia/nvidia-nemotron-3-nano-30b-a3b" {
				*model = NemotronNano
			}
		}
		// Upgrade the shipped light and routing models while preserving explicit
		// middle/high choices and models served by other providers.
		for _, model := range []*string{&settings.LowModel, &settings.RouteModel} {
			if strings.TrimSpace(*model) == NemotronNano {
				*model = NemotronLightning
			}
		}
		if model := strings.TrimSpace(settings.VisionModel); model == legacyNemotronNano || model == NemotronNano || model == "nvidia/nvidia-nemotron-3-nano-30b-a3b" || model == "Qwen/Qwen2.5-VL-72B-Instruct" {
			settings.VisionModel = NebiusVision
		}
	}
	return settings
}

func usesNebiusEndpoint(settings Settings) bool {
	return settings.Provider == ProviderNebius && strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/") == NebiusBaseURL
}

func (s *Store) Settings() Settings {
	return effectiveSettings(s.ConfiguredSettings())
}

func effectiveSettings(settings Settings) Settings {
	if strings.TrimSpace(settings.MiddleModel) == "" {
		settings.MiddleModel = settings.LowModel
	}
	if strings.TrimSpace(settings.HighModel) == "" {
		settings.HighModel = settings.MiddleModel
		if usesNebiusEndpoint(settings) {
			settings.HighModel = NemotronUltra
		}
	}
	if strings.TrimSpace(settings.VisionModel) == "" {
		settings.VisionModel = settings.LowModel
		if usesNebiusEndpoint(settings) {
			settings.VisionModel = NebiusVision
		}
	}
	if strings.TrimSpace(settings.RouteModel) == "" {
		settings.RouteModel = settings.LowModel
	}
	return settings
}

func (s *Store) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.root, "settings.json"), settings)
}
