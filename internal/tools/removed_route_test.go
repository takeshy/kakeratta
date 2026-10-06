package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestRemovedRouteCannotCallPublicFallback(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("removed route contacted a map service")
	}))
	defer backend.Close()
	registry := New(Config{Places: PlaceEndpoints{Nominatim: backend.URL, Overpass: backend.URL}})
	if slices.Contains(registry.Names(), "route") {
		t.Fatal("route is still listed")
	}
	set := registry.For([]string{"route", "place_search"})
	if set.Allows("route") || set.ReadAvailable("route", &timeline.Place{}) {
		t.Fatal("route is still available")
	}
	for _, at := range []*timeline.Place{nil, {Lat: 35, Lon: 139}} {
		if _, err := set.Run(context.Background(), "route", Args{"to": "渋谷駅"}, at); err == nil {
			t.Fatal("removed route ran")
		}
	}
	if calls != 0 {
		t.Fatalf("network calls: %d", calls)
	}
}
