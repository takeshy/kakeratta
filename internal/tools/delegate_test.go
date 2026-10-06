package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func geoPlaceTools() []map[string]any {
	schema := func(names ...string) map[string]any {
		properties := map[string]any{}
		for _, name := range names {
			properties[name] = map[string]any{}
		}
		return map[string]any{"type": "object", "properties": properties}
	}
	return []map[string]any{
		{"name": "place_search", "inputSchema": schema("query", "lat", "lng", "radius")},
	}
}

func TestBuiltinPlaceToolsGoToMCPWithThePosition(t *testing.T) {
	var calls []string
	var last map[string]any
	backend := fakeMCPWith(t, geoPlaceTools(), func(name string, args map[string]any) any {
		calls = append(calls, name)
		last = args
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": "geoの答え"},
			{"type": "resource_link", "uri": "https://www.openstreetmap.org/x", "name": "OpenStreetMap"},
		}}
	})
	defer backend.Close()
	set := New(Config{MCP: mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")}).For([]string{"place_search"})
	at := &timeline.Place{Lat: 35.69, Lon: 139.70}

	result, err := set.Run(context.Background(), "place_search", Args{"query": "カフェ", "radius": 800.0}, at)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(last) != "map[lat:35.69 lng:139.7 query:カフェ radius:800]" {
		t.Fatalf("place args: %v", last)
	}
	if result.Text != "geoの答え\n- OpenStreetMap: https://www.openstreetmap.org/x" || len(result.Sources) != 1 {
		t.Fatalf("result: %+v", result)
	}

	if _, err := set.Run(context.Background(), "place_search", Args{"query": "カフェ", "radius": 9000.0}, nil); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(last) != "map[query:カフェ]" {
		t.Fatalf("place_search without a position: %v", last)
	}

	if strings.Join(calls, ",") != "place_search,place_search" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestBuiltinPlaceToolsFallBackWhenMCPIsUnreachable(t *testing.T) {
	osm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			fmt.Fprint(w, `[{"lat":"35.6580","lon":"139.7016","display_name":"渋谷駅"}]`)
			return
		}
		t.Errorf("unexpected fallback endpoint: %s", r.URL.Path)
	}))
	defer osm.Close()
	geo := fakeMCPWith(t, geoPlaceTools(), func(string, map[string]any) any {
		t.Error("closed server was called")
		return nil
	})
	registry := New(Config{
		Places: PlaceEndpoints{Nominatim: osm.URL},
		MCP:    mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: geo.URL}}, "test"),
	})
	geo.Close()

	result, err := registry.For([]string{"place_search"}).Run(context.Background(), "place_search", Args{"query": "渋谷駅"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "渋谷駅") {
		t.Fatalf("built-in did not answer: %s", result.Text)
	}
}

func TestMCPErrorResultIsReportedNotRetriedBuiltin(t *testing.T) {
	osm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("built-in was used: %s", r.URL)
	}))
	defer osm.Close()
	geo := fakeMCPWith(t, geoPlaceTools(), func(string, map[string]any) any {
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "周辺検索が完了しませんでした"}}}
	})
	defer geo.Close()
	set := New(Config{
		Places: PlaceEndpoints{Overpass: osm.URL, Nominatim: osm.URL},
		MCP:    mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: geo.URL}}, "test"),
	}).For([]string{"place_search"})

	_, err := set.Run(context.Background(), "place_search", Args{"query": "カフェ"}, &timeline.Place{Lat: 35, Lon: 139})
	if err == nil || !strings.Contains(err.Error(), "周辺検索が完了しませんでした") {
		t.Fatalf("error: %v", err)
	}
}

func TestSameNamedToolWithOtherArgumentsIsNotUsed(t *testing.T) {
	tool := preferMCP(placeSearch, []mcp.Listed{{Server: "other", Tool: mcp.Tool{Name: "place_search", InputSchema: []byte(`{"properties":{"from":{},"to":{}}}`)}}})
	if fmt.Sprintf("%p", tool.run) != fmt.Sprintf("%p", placeSearch.run) {
		t.Fatal("a place tool with incompatible parameters replaced the built-in")
	}
}
