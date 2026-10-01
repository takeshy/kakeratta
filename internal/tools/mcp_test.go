package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func fakeMCP(t *testing.T, onCall func(name string, args map[string]any) any) *httptest.Server {
	return fakeMCPWith(t, []map[string]any{{
		"name":        "compute_commute",
		"description": "Compute travel time\n  from bundled profiles.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"origin":      map[string]any{"type": "object", "properties": map[string]any{"lat": map[string]any{"type": "number"}, "lng": map[string]any{"type": "number"}}},
				"destination": map[string]any{"type": "object", "properties": map[string]any{"lat": map[string]any{"type": "number"}, "lng": map[string]any{"type": "number"}}},
				"mode":        map[string]any{"type": "string", "enum": []string{"transit", "walk"}},
				"arrivalTime": map[string]any{"type": "string", "description": "ISO 8601 desired arrival time"},
			},
			"required": []string{"origin", "destination"},
		},
	}, {"name": "list_layers", "description": "List layers."}}, onCall)
}

func fakeMCPWith(t *testing.T, tools []map[string]any, onCall func(name string, args map[string]any) any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&message)
		var result any
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": mcp.ProtocolVersion}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			name, _ := message.Params["name"].(string)
			args, _ := message.Params["arguments"].(map[string]any)
			result = onCall(name, args)
		}
		w.Header().Set("Content-Type", "application/json")
		reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result})
		w.Write(reply)
	}))
}

func TestMCPToolsAreListedDescribedAndRun(t *testing.T) {
	var called map[string]any
	backend := fakeMCP(t, func(name string, args map[string]any) any {
		called = args
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": "約35分（乗換1回）"},
			{"type": "resource_link", "uri": "https://geo.example/map", "name": "地図"},
		}}
	})
	defer backend.Close()
	config := Config{MCP: mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")}
	registry := New(config)
	names := registry.Names()
	if strings.Join(names, ",") != "geo.compute_commute,geo.list_layers,place_search,route,schedule,schedules_list,timeline_search,web_search" {
		t.Fatalf("names: %v", names)
	}
	set := registry.For([]string{"geo.compute_commute", "route"})
	if usage := set.Describe(); !strings.Contains(usage, "geo.compute_commute — Compute travel time from bundled profiles.") {
		t.Fatalf("usage: %s", usage)
	}
	// The argument names reach the model as the server's own schema now, rather
	// than as an example built into the sentence.
	var commute Declaration
	for _, declaration := range set.Declarations() {
		if declaration.Name == "geo.compute_commute" {
			commute = declaration
		}
	}
	properties, _ := commute.Parameters["properties"].(map[string]any)
	for _, want := range []string{"origin", "destination", "mode", "arrivalTime"} {
		if _, ok := properties[want]; !ok {
			t.Fatalf("the server's schema did not reach the model: %v", commute.Parameters)
		}
	}
	if required, _ := commute.Parameters["required"].([]any); len(required) != 2 {
		t.Fatalf("required arguments lost: %v", commute.Parameters["required"])
	}
	if _, err := set.Run(context.Background(), "geo.list_layers", nil, nil); err == nil {
		t.Fatal("undeclared MCP tool ran")
	}
	if _, err := set.Run(context.Background(), "geo.compute_commute", Args{"origin": map[string]any{}}, nil); err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("missing argument not reported: %v", err)
	}
	if _, err := set.Run(context.Background(), "geo.compute_commute", Args{"origin": "x", "destination": map[string]any{}}, nil); err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("wrong type not reported: %v", err)
	}
	result, err := set.Run(context.Background(), "geo.compute_commute",
		Args{"origin": map[string]any{"lat": 35.0, "lng": 139.0}, "destination": map[string]any{"lat": 35.6, "lng": 139.7}, "mode": "transit"},
		&timeline.Place{Lat: 35, Lon: 139})
	if err != nil {
		t.Fatal(err)
	}
	if called["mode"] != "transit" {
		t.Fatalf("arguments not forwarded: %v", called)
	}
	if !strings.Contains(result.Text, "約35分") || !strings.Contains(result.Text, "地図: https://geo.example/map") {
		t.Fatalf("text: %s", result.Text)
	}
	if len(result.Sources) != 1 || result.Sources[0].URL != "https://geo.example/map" || result.Sources[0].Label != "地図" {
		t.Fatalf("sources: %+v", result.Sources)
	}
}

func TestMCPToolErrorsAndStructuredResults(t *testing.T) {
	backend := fakeMCP(t, func(name string, args map[string]any) any {
		if args["mode"] == "walk" {
			return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "no profile"}}}
		}
		return map[string]any{"content": []map[string]any{}, "structuredContent": map[string]any{"durationMinutes": 35}}
	})
	defer backend.Close()
	set := New(Config{MCP: mcp.NewCatalogue([]mcp.Server{{Name: "geo", URL: backend.URL}}, "test")}).For([]string{"geo.compute_commute"})
	args := Args{"origin": map[string]any{}, "destination": map[string]any{}}
	_, err := set.Run(context.Background(), "geo.compute_commute", Args{"origin": map[string]any{}, "destination": map[string]any{}, "mode": "walk"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("tool error: %v", err)
	}
	result, err := set.Run(context.Background(), "geo.compute_commute", args, nil)
	if err != nil || !strings.Contains(result.Text, `"durationMinutes":35`) {
		t.Fatalf("%v %s", err, result.Text)
	}
}

func TestBuiltinNamesWinOverMCPAndNoCatalogueIsFine(t *testing.T) {
	if names := New(Config{}).Names(); fmt.Sprint(names) != "[place_search route schedule schedules_list timeline_search web_search]" {
		t.Fatalf("names: %v", names)
	}
	usage := describeMCP("geo.list_layers", mcp.Tool{Name: "list_layers", Title: "Layers"})
	if usage != "geo.list_layers — Layers" {
		t.Fatalf("usage: %s", usage)
	}
}
