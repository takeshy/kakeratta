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
	NemotronNano       = "nvidia/nvidia-nemotron-3-nano-30b-a3b"
	NemotronSuper      = "nvidia/nemotron-3-super-120b-a12b"
	NebiusVision       = "Qwen/Qwen2.5-VL-72B-Instruct"
	legacyNemotronNano = "nvidia/nemotron-3-nano-30b-a3b"
)

// Settings are the endpoints, the models and the tools' credentials.
type Settings struct {
	Provider Provider `json:"provider"`
	BaseURL  string   `json:"baseUrl"`
	APIKey   string   `json:"apiKey"`

	// Three sizes, in the order they answer. A persona declares which tier it
	// belongs to, and the contrast between the tiers is the experience: the low
	// one reacts quickly and shallowly, and what it said is handed to the ones
	// above it so they have something to pick up on.
	//
	// A tier left empty falls back to the one below it, so running everything on
	// one model is a valid setup - it just gives up the contrast.
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
	Active []string `json:"active"`

	Search SearchSettings `json:"search"`
	Places PlaceSettings  `json:"places"`
	Speech SpeechSettings `json:"speech"`

	ReadAloudRate  float64 `json:"readAloudRate"`
	ReadAloudVoice string  `json:"readAloudVoice"`
}

// SearchSettings is the web search a persona may declare it needs. The app
// makes the call; a skill never holds a key.
type SearchSettings struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
}

// PlaceSettings are the endpoints for looking things up around the user.
// Both default to public services that need no key, so the tools work out of
// the box and can be pointed at a private instance.
type PlaceSettings struct {
	NominatimURL string `json:"nominatimUrl"`
	OverpassURL  string `json:"overpassUrl"`
	RouteURL     string `json:"routeUrl"`
}

// SpeechSettings are the dictation settings, ported whole from speech-popup so
// the same engines, phrases and rules work here.
type SpeechSettings struct {
	Provider        string `json:"provider"`
	EndpointType    string `json:"endpointType"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey"`
	Model           string `json:"model"`
	Language        string `json:"language"`
	SilenceSeconds  int    `json:"silenceSeconds"`
	SendPhrase      string `json:"sendPhrase"`
	Replacements    string `json:"replacements"`
	SymbolCommands  string `json:"symbolCommands"`
	VertexProjectID string `json:"vertexProjectId"`
}

func defaultSettings() Settings {
	return Settings{
		Provider:    ProviderNebius,
		BaseURL:     NebiusBaseURL,
		LowModel:    NemotronNano,
		MiddleModel: NemotronSuper,
		// The high tier is left empty rather than guessed at: it falls back to
		// the middle one until something bigger is actually pointed at.
		VisionModel: NebiusVision,
		RouteModel:  NemotronNano,
		Search: SearchSettings{
			Provider: "tavily",
			BaseURL:  "https://api.tavily.com/search",
		},
		Places: PlaceSettings{
			NominatimURL: "https://nominatim.openstreetmap.org",
			OverpassURL:  "https://overpass-api.de/api/interpreter",
			RouteURL:     "https://router.project-osrm.org",
		},
		Speech: SpeechSettings{
			Provider:       "browser",
			EndpointType:   "openai",
			BaseURL:        "https://api.openai.com/v1",
			Model:          "whisper-1",
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
			if strings.TrimSpace(*model) == legacyNemotronNano {
				*model = NemotronNano
			}
		}
		if model := strings.TrimSpace(settings.VisionModel); model == legacyNemotronNano || model == NemotronNano {
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
