package store

import (
	"path/filepath"
	"strings"
	"time"
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
	NebiusBaseURL     = "https://api.tokenfactory.nebius.com/v1"
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// Lightning judges replies, Super answers, Ultra answers a reply asking for
	// more. These are only where it starts; any OpenAI-compatible name fits.
	NemotronLightning  = "nvidia/Nemotron-3_5-Lightning"
	NemotronNano       = "nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B"
	NemotronSuper      = "nvidia/nemotron-3-super-120b-a12b"
	NemotronUltra      = "nvidia/Nemotron-3-Ultra-550b-a55b"
	NebiusVision       = "zai-org/GLM-5.3-Flash"
	OpenRouterRouter   = "nvidia/nemotron-3-super-120b-a12b"
	OpenRouterHigh     = "nvidia/nemotron-3-ultra-550b-a55b"
	OpenRouterMiddle   = "nvidia/nemotron-3-super-120b-a12b"
	OpenRouterLow      = "nvidia/nemotron-3.5-lightning"
	OpenRouterVision   = "z-ai/glm-5.3-flash"
	legacyNemotronNano = "nvidia/nemotron-3-nano-30b-a3b"
)

// Settings are the endpoints, the models and the user's own web search key.
// Where the tools go is not here: the map endpoints and the MCP servers are the
// operator's configuration (see tools.Config), the same for everyone.
type Settings struct {
	Provider Provider `json:"provider"`
	BaseURL  string   `json:"baseUrl"`
	APIKey   string   `json:"apiKey"`

	// Three sizes. Every persona answers on the middle one (Super). The low one
	// (Lightning) only judges whether a reply to an answer asks for a better or
	// deeper one, and such a reply is answered on the high one (Ultra).
	//
	// An empty high tier uses Nemotron Ultra on Token Factory. Other endpoints
	// retain the fallback to the tier below.
	LowModel    string `json:"lowModel"`
	MiddleModel string `json:"middleModel"`
	HighModel   string `json:"highModel"`
	// VisionModel reads a capture once for everyone. On Nebius, an empty
	// selection uses the vision default, never a text-only conversation model.
	VisionModel string `json:"visionModel"`
	RouterModel string `json:"routerModel"`

	// Jev optionally replaces the default Super router. It uses OpenRouter's
	// Decisions API; the key is kept separately from the persona model key.
	JevEnabled            bool   `json:"jevEnabled"`
	OpenRouterAPIKey      string `json:"openRouterApiKey"`
	OpenRouterMode        bool   `json:"openRouterMode"`
	OpenRouterRouterModel string `json:"openRouterRouterModel"`
	OpenRouterHighModel   string `json:"openRouterHighModel"`
	OpenRouterMiddleModel string `json:"openRouterMiddleModel"`
	OpenRouterLowModel    string `json:"openRouterLowModel"`

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

	// TimeZone is the user's own, saved by the page. Everything the app writes
	// for a persona to read - the current time, a note's source label, the wake
	// it was given - is formatted in it, because the server's clock is UTC and
	// means nothing to the person reading the timeline.
	TimeZone string `json:"timeZone"`

	// MarkdownInput renders the user's own posts and replies as Markdown. A
	// persona's reply is always rendered, and a Hermes HTML report is shown in
	// a frame; only the user's own words depend on this switch, because only
	// they are edited by hand.
	MarkdownInput bool `json:"markdownInput"`

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
		Provider:              ProviderNebius,
		BaseURL:               NebiusBaseURL,
		LowModel:              NemotronLightning,
		MiddleModel:           NemotronSuper,
		HighModel:             NemotronUltra,
		VisionModel:           NebiusVision,
		RouterModel:           NemotronSuper,
		OpenRouterRouterModel: OpenRouterRouter,
		OpenRouterHighModel:   OpenRouterHigh,
		OpenRouterMiddleModel: OpenRouterMiddle,
		OpenRouterLowModel:    OpenRouterLow,
		Speech: SpeechSettings{
			Provider:       "browser",
			Language:       "auto",
			SilenceSeconds: 3,
			SendPhrase:     "終わります",
		},
		ReadAloudRate: 1.2,
	}
}

// Location is the zone the app formats times in for this workspace. An unset or
// unusable zone falls back to UTC rather than the server's own local time,
// which is whatever the container happens to be set to.
func (s Settings) Location() *time.Location {
	zone := strings.TrimSpace(s.TimeZone)
	if zone == "" || zone == "Local" {
		return time.UTC
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
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
	if strings.TrimSpace(settings.RouterModel) == "" {
		settings.RouterModel = NemotronSuper
	}
	if strings.TrimSpace(settings.OpenRouterRouterModel) == "" {
		settings.OpenRouterRouterModel = OpenRouterRouter
	}
	if strings.TrimSpace(settings.OpenRouterHighModel) == "" {
		settings.OpenRouterHighModel = OpenRouterHigh
	}
	if strings.TrimSpace(settings.OpenRouterMiddleModel) == "" {
		settings.OpenRouterMiddleModel = OpenRouterMiddle
	}
	if strings.TrimSpace(settings.OpenRouterLowModel) == "" {
		settings.OpenRouterLowModel = OpenRouterLow
	}
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
		for _, model := range []*string{&settings.LowModel, &settings.MiddleModel, &settings.HighModel} {
			if name := strings.TrimSpace(*model); name == legacyNemotronNano || name == "nvidia/nvidia-nemotron-3-nano-30b-a3b" {
				*model = NemotronNano
			}
		}
		// Upgrade the shipped light model while preserving explicit middle/high
		// choices and models served by other providers.
		for _, model := range []*string{&settings.LowModel} {
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
	return settings
}

func (s *Store) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.root, "settings.json"), settings)
}
