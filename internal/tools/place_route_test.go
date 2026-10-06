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
