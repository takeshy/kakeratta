package tools

import (
	"testing"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestReadAvailabilityUsesConfigurationAndTurnContext(t *testing.T) {
	names := []string{"web_search", "weather_forecast", "timeline_search", "schedule", "route", "place_search", "kakeratta_help", "unknown"}
	bare := New(Config{}).For(names)
	for _, name := range append(names, "schedules_list") {
		if got, want := bare.ReadAvailable(name, nil), name == "kakeratta_help"; got != want {
			t.Errorf("unconfigured %s: available=%t, want %t", name, got, want)
		}
	}
	r := New(Config{Places: DefaultPlaces(), Weather: WeatherAPIConfig{APIKey: "key"}}).
		WithTavilyKey("key").WithSearch(&search.Service{}).WithSchedules(&fakeSchedules{})
	configured := r.For(names)
	for _, name := range []string{"web_search", "weather_forecast", "timeline_search", "schedules_list", "place_search", "kakeratta_help"} {
		if !configured.ReadAvailable(name, nil) {
			t.Errorf("configured read %s missing", name)
		}
	}
	if configured.ReadAvailable("route", nil) || !configured.ReadAvailable("route", &timeline.Place{}) {
		t.Error("route availability must follow the post's location")
	}
	if configured.ReadAvailable("schedule", nil) || r.For(nil).ReadAvailable("web_search", nil) {
		t.Error("actions or undeclared tools were advertised as reads")
	}
}

func TestReadAvailabilityPreservesMCPDelegationWithoutBuiltinEndpoints(t *testing.T) {
	backend := fakeMCPWith(t, geoPlaceTools(), func(string, map[string]any) any {
		t.Error("availability check executed a tool")
		return nil
	})
	defer backend.Close()
	set := New(Config{MCP: mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")}).For([]string{"place_search", "route"})
	if !set.ReadAvailable("place_search", nil) || !set.ReadAvailable("route", &timeline.Place{}) || set.ReadAvailable("route", nil) {
		t.Error("MCP map delegation or location requirement was lost")
	}
}
