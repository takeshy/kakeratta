package tools

import "testing"

// A tool is offered to the model as a schema, not as an example buried in
// prose, so every tool a persona declared has to carry the shape of its
// arguments - and the names in that shape are the ones the tool actually reads.
func TestDeclarationsCarryTheArgumentShapes(t *testing.T) {
	set := New(Config{}).WithSchedules(nil).For([]string{"web_search", "place_search", "schedule", "weather_forecast", "kakeratta_help"})
	declarations := map[string]Declaration{}
	for _, declaration := range set.Declarations() {
		if declaration.Description == "" {
			t.Errorf("%s has no description", declaration.Name)
		}
		properties, _ := declaration.Parameters["properties"].(map[string]any)
		if len(properties) == 0 {
			t.Errorf("%s has no argument schema: %v", declaration.Name, declaration.Parameters)
		}
		declarations[declaration.Name] = declaration
	}
	for _, name := range []string{"web_search", "place_search", "schedule", "weather_forecast", "kakeratta_help"} {
		if _, ok := declarations[name]; !ok {
			t.Fatalf("%s was not offered: %v", name, declarations)
		}
	}
	schedule, _ := declarations["schedule"].Parameters["properties"].(map[string]any)
	for _, argument := range []string{"action", "at", "in_minutes", "note", "daily", "time_zone", "id"} {
		if _, ok := schedule[argument]; !ok {
			t.Errorf("schedule's schema lacks %q", argument)
		}
	}
	if places, _ := declarations["place_search"].Parameters["properties"].(map[string]any); places["query"] == nil {
		t.Errorf("place search schema lacks query: %v", places)
	}
}
