package geocode

import (
	"encoding/json"
	"github.com/takeshy/kakeratta/internal/timeline"
	"testing"
)

func TestAdministrativeLabels(t *testing.T) {
	for _, tc := range []struct{ name, language, raw, want string }{
		{"Japanese hierarchy", "ja", `{"found":true,"place":{"name":"大豆戸町","administrativeAreas":[{"level":3,"name":"港北区"},{"level":1,"name":"神奈川県"},{"level":2,"name":"横浜市"}]}}`, "横浜市港北区大豆戸町"},
		{"parent already included", "ja", `{"found":true,"place":{"name":"横浜市港北区","administrativeAreas":[{"level":2,"name":"横浜市"},{"level":3,"name":"横浜市港北区"}]}}`, "横浜市港北区"},
		{"settlement equals district", "ja", `{"found":true,"place":{"name":"港北区","administrativeAreas":[{"level":2,"name":"横浜市"},{"level":3,"name":"港北区"}]}}`, "横浜市港北区"},
		{"English hierarchy", "en", `{"found":true,"place":{"name":"Mamedo","administrativeAreas":[{"level":2,"name":"Yokohama"},{"level":3,"name":"Kohoku"}]}}`, "Yokohama, Kohoku, Mamedo"},
		{"old response", "ja", `{"found":true,"place":{"name":"Tokyo","countryCode":"JP"}}`, "Tokyo"},
		{"missing area names", "ja", `{"found":true,"place":{"name":"村","administrativeAreas":[{"level":2,"name":" "},{"level":4,"name":"地区"}]}}`, "地区村"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := timeline.Place{Lat: 35, Lon: 139}
			got, found, err := Resolve(json.RawMessage(tc.raw), before, tc.language)
			if err != nil || !found || got.Label != tc.want || !got.Approximate || got.Lat != before.Lat || got.Lon != before.Lon {
				t.Fatalf("got %+v found=%v err=%v", got, found, err)
			}
		})
	}
}

func TestLanguage(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"ja", "ja"}, {"en", "en"}, {"", "ja"}, {"fr", "ja"}} {
		if got := Language(tc.input); got != tc.want {
			t.Fatalf("Language(%q)=%q", tc.input, got)
		}
	}
}

func TestSearchHierarchyDoesNotChangeDisplay(t *testing.T) {
	for _, tc := range []struct{ language, country, region, name string }{
		{"ja", "日本", "秋田県", "田沢湖生保内"}, {"en", "Japan", "Akita", "Tazawako"},
	} {
		raw, _ := json.Marshal(map[string]any{"found": true, "place": map[string]any{"name": tc.name, "countryCode": "JP", "administrativeAreas": []any{map[string]any{"level": 1, "name": tc.region}}}})
		got, found, err := Resolve(raw, timeline.Place{Lat: 39, Lon: 140}, tc.language)
		if err != nil || !found || got.Label != tc.name || got.Country != tc.country || got.CountryCode != "JP" || got.Region != tc.region {
			t.Fatalf("%+v %v", got, err)
		}
	}
}
