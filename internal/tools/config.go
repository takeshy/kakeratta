package tools

import (
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
	Places PlaceEndpoints
	// MCP is the shared tool catalogue, or nil when no server is configured.
	MCP *mcp.Catalogue
}

// PlaceEndpoints are where place_search and route go. Defaults are the public
// OpenStreetMap services, which need no key and allow one request per second.
type PlaceEndpoints struct {
	Nominatim    string
	Overpass     string
	Route        string
	WalkingRoute string
	CyclingRoute string
}

func DefaultPlaces() PlaceEndpoints {
	return PlaceEndpoints{
		Nominatim:    "https://nominatim.openstreetmap.org",
		Overpass:     "https://overpass-api.de/api/interpreter",
		Route:        "https://router.project-osrm.org",
		WalkingRoute: "https://routing.openstreetmap.de/routed-foot",
		CyclingRoute: "https://routing.openstreetmap.de/routed-bike",
	}
}

// ConfigFromEnv reads the operator's environment. Unset endpoints fall back
// to the public services; a malformed MCP list is an error, because a server
// that silently lost a tool is harder to notice than one that did not start.
//
//	KAKERATTA_NOMINATIM_URL    name lookup
//	KAKERATTA_OVERPASS_URL     nearby search
//	KAKERATTA_OSRM_CAR_URL     driving routes
//	KAKERATTA_OSRM_FOOT_URL    walking routes
//	KAKERATTA_OSRM_BIKE_URL    cycling routes
//	KAKERATTA_MCP_SERVERS      JSON: [{"name":"geo","url":"https://…/mcp","headers":{…}}]
func ConfigFromEnv(version string) (Config, error) {
	places := DefaultPlaces()
	for _, binding := range []struct {
		name  string
		field *string
	}{
		{"KAKERATTA_NOMINATIM_URL", &places.Nominatim},
		{"KAKERATTA_OVERPASS_URL", &places.Overpass},
		{"KAKERATTA_OSRM_CAR_URL", &places.Route},
		{"KAKERATTA_OSRM_FOOT_URL", &places.WalkingRoute},
		{"KAKERATTA_OSRM_BIKE_URL", &places.CyclingRoute},
	} {
		if value := strings.TrimSpace(os.Getenv(binding.name)); value != "" {
			*binding.field = value
		}
	}
	config := Config{Places: places}
	servers, err := mcp.ParseServers(os.Getenv("KAKERATTA_MCP_SERVERS"))
	if err != nil {
		return Config{}, fmt.Errorf("KAKERATTA_MCP_SERVERS: %w", err)
	}
	if len(servers) > 0 {
		config.MCP = mcp.NewCatalogue(servers, version)
	}
	return config, nil
}
