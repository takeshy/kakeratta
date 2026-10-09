package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/takeshy/kakeratta/internal/mcp"
)

// Config is what the operator gives the tools. None of it is a user's to
// change: the map endpoints are the operator's servers or the public ones, and
// the MCP servers are what the operator chose to expose to every persona in
// the deployment. The one key a user holds, for web search, is not here.
type Config struct {
	ObserveWebSearch func(context.Context, bool)
	Places           PlaceEndpoints
	Weather          WeatherAPIConfig
	// MCP is the shared tool catalogue, or nil when no server is configured.
	MCP *mcp.Catalogue
}

// WeatherAPIConfig is server-side configuration, never included in tool declarations.
type WeatherAPIConfig struct {
	APIKey string
	URL    string
}

// PlaceEndpoints are where place_search goes. Defaults are the public
// OpenStreetMap services, which need no key and allow one request per second.
type PlaceEndpoints struct {
	Nominatim string
	Overpass  string
}

func DefaultPlaces() PlaceEndpoints {
	return PlaceEndpoints{
		Nominatim: "https://nominatim.openstreetmap.org",
		Overpass:  "https://overpass-api.de/api/interpreter",
	}
}

// ConfigFromEnv reads the operator's environment. Unset endpoints fall back
// to the public services; a malformed MCP list is an error, because a server
// that silently lost a tool is harder to notice than one that did not start.
//
//	KAKERATTA_NOMINATIM_URL    name lookup
//	KAKERATTA_OVERPASS_URL     nearby search
//	KAKERATTA_MCP_SERVERS      JSON: [{"name":"geo","url":"https://…/mcp","headers":{…}}]
//	KAKERATTA_WEATHERAPI_KEY   WeatherAPI key, taking precedence over the key file
//	KAKERATTA_WEATHERAPI_KEY_FILE key file (optional local default: tmp/weatherapi.key)
//	KAKERATTA_WEATHERAPI_URL   WeatherAPI base URL (default: https://api.weatherapi.com/v1)
func ConfigFromEnv(version string) (Config, error) {
	places := DefaultPlaces()
	for _, binding := range []struct {
		name  string
		field *string
	}{
		{"KAKERATTA_NOMINATIM_URL", &places.Nominatim},
		{"KAKERATTA_OVERPASS_URL", &places.Overpass},
	} {
		if value := strings.TrimSpace(os.Getenv(binding.name)); value != "" {
			*binding.field = value
		}
	}
	config := Config{Places: places}
	config.Weather = WeatherAPIConfig{APIKey: strings.TrimSpace(os.Getenv("KAKERATTA_WEATHERAPI_KEY")), URL: "https://api.weatherapi.com/v1"}
	if endpoint := strings.TrimSpace(os.Getenv("KAKERATTA_WEATHERAPI_URL")); endpoint != "" {
		config.Weather.URL = endpoint
	}
	if config.Weather.APIKey == "" {
		path := strings.TrimSpace(os.Getenv("KAKERATTA_WEATHERAPI_KEY_FILE"))
		explicit := path != ""
		if !explicit {
			path = "tmp/weatherapi.key"
		}
		key, err := os.ReadFile(path)
		if err != nil && (explicit || !errors.Is(err, os.ErrNotExist)) {
			return Config{}, errors.New("WeatherAPI のキーファイルを読み込めません")
		}
		config.Weather.APIKey = strings.TrimSpace(string(key))
		if err == nil && config.Weather.APIKey == "" {
			return Config{}, errors.New("WeatherAPI のキーファイルが空です")
		}
	}
	servers, err := mcp.ParseServers(os.Getenv("KAKERATTA_MCP_SERVERS"))
	if err != nil {
		return Config{}, fmt.Errorf("KAKERATTA_MCP_SERVERS: %w", err)
	}
	if len(servers) > 0 {
		config.MCP = mcp.NewCatalogue(servers, version)
	}
	return config, nil
}
