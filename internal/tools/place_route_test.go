package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestRouteUsesModeSpecificBackend(t *testing.T) {
	for _, tc := range []struct {
		mode, endpoint, label string
		seconds               int
	}{
		{"driving", "car", "車", 448}, {"walking", "foot", "徒歩", 3281}, {"cycling", "bike", "自転車", 1345},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/search" {
					fmt.Fprint(w, `[{"lat":"35.6580","lon":"139.7016","display_name":"渋谷駅"}]`)
					return
				}
				if !strings.HasPrefix(r.URL.Path, "/"+tc.endpoint+"/route/v1/driving/") {
					t.Errorf("wrong backend: %s", r.URL.Path)
				}
				fmt.Fprintf(w, `{"code":"Ok","routes":[{"duration":%d,"distance":4101}]}`, tc.seconds)
			}))
			defer backend.Close()
			registry := New(Config{Places: PlaceEndpoints{Nominatim: backend.URL, Route: backend.URL + "/car", WalkingRoute: backend.URL + "/foot", CyclingRoute: backend.URL + "/bike"}})
			result, err := registry.For([]string{"route"}).Run(context.Background(), "route", Args{"to": "渋谷駅", "mode": tc.mode}, &timeline.Place{Lat: 35.6896, Lon: 139.7006})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Text, fmt.Sprintf("%s で約%d分", tc.label, (tc.seconds+59)/60)) {
				t.Fatal(result.Text)
			}
			if !strings.Contains(result.Sources[0].URL, "fossgis_osrm_"+tc.endpoint) {
				t.Fatal(result.Sources)
			}
		})
	}
}

func TestRouteRejectsUnsupportedOrMissingModeEndpoint(t *testing.T) {
	for _, tc := range []struct{ mode, walkingURL string }{
		{"walking", ""}, {"walking", "https://router.project-osrm.org"}, {"transit", ""},
	} {
		registry := New(Config{Places: PlaceEndpoints{Route: "https://router.project-osrm.org", WalkingRoute: tc.walkingURL}})
		_, err := registry.For([]string{"route"}).Run(context.Background(), "route", Args{"to": "渋谷駅", "mode": tc.mode}, &timeline.Place{Lat: 35, Lon: 139})
		if err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	_, err := New(Config{}).For([]string{"route"}).Run(context.Background(), "route", Args{"to": "渋谷駅"}, nil)
	if err != ErrNoPlace {
		t.Fatalf("no location: %v", err)
	}
}

func TestNearbyRetriesOverloadAndSearchesRamenTag(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(504)
			fmt.Fprint(w, "busy")
			return
		}
		_ = r.ParseForm()
		if !strings.Contains(r.Form.Get("data"), `["cuisine"~"(^|;)ramen(;|$)",i]`) {
			t.Errorf("missing category: %s", r.Form.Get("data"))
		}
		fmt.Fprint(w, `{"elements":[{"type":"node","id":1,"lat":35,"lon":139,"tags":{"name":"麺屋","cuisine":"ramen","opening_hours":"11:00-20:00"}}]}`)
	}))
	defer backend.Close()
	registry := New(Config{Places: PlaceEndpoints{Overpass: backend.URL}})
	result, err := registry.For([]string{"place_search"}).Run(context.Background(), "place_search", Args{"query": "ラーメン"}, &timeline.Place{Lat: 35, Lon: 139})
	if err != nil || calls != 2 || !strings.Contains(result.Text, "麺屋") {
		t.Fatalf("%+v %v calls=%d", result, err, calls)
	}
}

func TestNearbyDoesNotReportFailureAsNoPlaces(t *testing.T) {
	for _, tc := range []struct {
		code  int
		body  string
		calls int
	}{
		{504, "busy", 2}, {400, "invalid", 1}, {200, `{"remark":"runtime error: Query timed out","elements":[]}`, 1}, {200, "<html>busy</html>", 1},
	} {
		t.Run(fmt.Sprint(tc.code, tc.body), func(t *testing.T) {
			calls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
			defer backend.Close()
			result, err := New(Config{Places: PlaceEndpoints{Overpass: backend.URL}}).For([]string{"place_search"}).Run(context.Background(), "place_search", Args{"query": "coffee"}, &timeline.Place{Lat: 35, Lon: 139})
			if err == nil || result.Text != "" || calls != tc.calls {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
		})
	}
}

func TestPlaceWithoutLocationUsesNameLookup(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "渋谷駅" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `[{"display_name":"渋谷駅"}]`)
	}))
	defer backend.Close()
	result, err := New(Config{Places: PlaceEndpoints{Nominatim: backend.URL}}).For([]string{"place_search"}).Run(context.Background(), "place_search", Args{"query": "渋谷駅"}, nil)
	if err != nil || !strings.Contains(result.Text, "位置が分からない") {
		t.Fatalf("%+v %v", result, err)
	}
}
