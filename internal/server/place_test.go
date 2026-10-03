package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestPostPlaceAndRetry(t *testing.T) {
	for _, language := range []string{"ja", "en", ""} {
		t.Run("language="+language, func(t *testing.T) { testPostPlaceAndRetry(t, language) })
	}
}

func testPostPlaceAndRetry(t *testing.T, language string) {
	expectedLanguage := language
	if expectedLanguage == "" {
		expectedLanguage = "ja"
	}

	expectedLabel := "横浜市港北区大豆戸町付近"
	name, city, district := "大豆戸町", "横浜市", "港北区"
	if expectedLanguage == "en" {
		name, city, district = "Mamedo", "Yokohama", "Kohoku"
		expectedLabel = "Yokohama, Kohoku, Mamedo付近"
	}
	s, st := app(t)
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage
			Method string
			Params struct {
				Name      string
				Arguments map[string]any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": mcp.ProtocolVersion}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "reverse_geocode", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			calls++
			if req.Params.Name != "reverse_geocode" || req.Params.Arguments["lat"] != float64(35) || req.Params.Arguments["lng"] != float64(139) || req.Params.Arguments["language"] != expectedLanguage {
				t.Errorf("wrong call: %+v", req.Params)
			}
			result = map[string]any{"structuredContent": map[string]any{"found": true, "place": map[string]any{"name": name, "countryCode": "JP", "geonameId": 1850147, "administrativeAreas": []any{map[string]any{"level": 1, "name": "神奈川県"}, map[string]any{"level": 2, "name": city}, map[string]any{"level": 3, "name": district}}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer backend.Close()
	s.tools.MCP = mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")
	body, _ := json.Marshal(postBody{ClientID: "local-12345678-1234-1234-1234-123456789abc", ClientAt: time.Now().UTC(), Text: "hello", Language: language, Place: &timeline.Place{Lat: 35, Lon: 139}})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/post", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var entry timeline.Entry
		if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Place == nil || entry.Place.Describe() != expectedLabel || entry.Place.GeonameID != 1850147 {
			t.Fatalf("place: %+v", entry.Place)
		}
		saved, ok := st.Entry(entry.ID)
		if !ok || saved.Place.Label != entry.Place.Label {
			t.Fatal("place not persisted")
		}
	}
	if calls != 1 {
		t.Fatalf("retried lookup: %d", calls)
	}
}

func TestResolvePlaceFallback(t *testing.T) {
	s, _ := app(t)
	if s.resolvePlace(context.Background(), nil, "ja") != nil {
		t.Fatal("invented place")
	}
	if s.resolvePlace(context.Background(), &timeline.Place{Lat: 91}, "ja") != nil {
		t.Fatal("invalid coordinates")
	}
	place := &timeline.Place{Lat: 0, Lon: 0}
	got := s.resolvePlace(context.Background(), place, "ja")
	if got == nil || got.Lat != 0 || got.Label != "" {
		t.Fatalf("fallback: %+v", got)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer backend.Close()
	s.tools.MCP = mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")
	got = s.resolvePlace(context.Background(), place, "ja")
	if got == nil || got.Label != "" {
		t.Fatalf("outage fallback: %+v", got)
	}
}
