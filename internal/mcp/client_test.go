package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeServer speaks just enough Streamable HTTP for the client: it can answer
// in plain JSON or as an SSE stream, and can hand out a session.
type fakeServer struct {
	sse        bool
	session    string
	calls      atomic.Int32
	inits      atomic.Int32
	lastArgs   map[string]any
	lastHeader http.Header
}

func (f *fakeServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.lastHeader = r.Header.Clone()
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if f.session != "" && message.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != f.session {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var result any
		switch message.Method {
		case "initialize":
			f.inits.Add(1)
			if f.session != "" {
				w.Header().Set("Mcp-Session-Id", f.session)
			}
			result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			if cursor, _ := message.Params["cursor"].(string); cursor == "" {
				result = map[string]any{"tools": []map[string]any{{"name": "compute_commute", "description": "所要時間", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"origin": map[string]any{"type": "object"}}, "required": []string{"origin"}}}}, "nextCursor": "p2"}
			} else {
				result = map[string]any{"tools": []map[string]any{{"name": "list_layers", "description": "レイヤー"}}}
			}
		case "tools/call":
			f.calls.Add(1)
			f.lastArgs, _ = message.Params["arguments"].(map[string]any)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "約35分"}, {"type": "resource_link", "uri": "https://example.com/route", "name": "Route"}}}
		default:
			t.Errorf("unexpected method %s", message.Method)
		}
		reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result})
		if f.sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", `{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", reply)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(reply)
	}
}

func TestClientListsAndCallsOverJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sse=%v", sse), func(t *testing.T) {
			fake := &fakeServer{sse: sse, session: "s1"}
			backend := httptest.NewServer(fake.handler(t))
			defer backend.Close()
			client := NewClient(Server{Name: "geo", URL: backend.URL, Headers: map[string]string{"Authorization": "Bearer x"}}, "test")
			tools, err := client.ListTools(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) != 2 || tools[0].Name != "compute_commute" || tools[1].Name != "list_layers" {
				t.Fatalf("tools: %+v", tools)
			}
			if fake.lastHeader.Get("Authorization") != "Bearer x" || fake.lastHeader.Get("Mcp-Session-Id") != "s1" || fake.lastHeader.Get("MCP-Protocol-Version") != ProtocolVersion {
				t.Fatalf("headers: %v", fake.lastHeader)
			}
			result, err := client.CallTool(context.Background(), "compute_commute", map[string]any{"origin": map[string]any{"lat": 35.0}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) != 2 || result.Content[0].Text != "約35分" || result.Content[1].URI != "https://example.com/route" {
				t.Fatalf("result: %+v", result)
			}
			if fake.inits.Load() != 1 {
				t.Fatalf("initialised %d times", fake.inits.Load())
			}
		})
	}
}

func TestClientReopensAForgottenSession(t *testing.T) {
	fake := &fakeServer{session: "s1"}
	backend := httptest.NewServer(fake.handler(t))
	defer backend.Close()
	client := NewClient(Server{Name: "geo", URL: backend.URL}, "test")
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.session = "s2" // the server restarted and no longer knows s1
	if _, err := client.CallTool(context.Background(), "compute_commute", nil); err != nil {
		t.Fatal(err)
	}
	if fake.inits.Load() != 2 || fake.calls.Load() != 1 {
		t.Fatalf("inits %d calls %d", fake.inits.Load(), fake.calls.Load())
	}
}

func TestClientReportsToolErrors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&message)
		w.Header().Set("Content-Type", "application/json")
		switch message.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"%s"}}`, message.ID, ProtocolVersion)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"Unknown tool"}}`, message.ID)
		}
	}))
	defer backend.Close()
	_, err := NewClient(Server{Name: "geo", URL: backend.URL}, "test").CallTool(context.Background(), "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "Unknown tool") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseServers(t *testing.T) {
	servers, err := ParseServers(`[{"name":"geo","url":"https://geo.example/mcp","headers":{"Authorization":"Bearer x"}}]`)
	if err != nil || len(servers) != 1 || servers[0].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("%v %+v", err, servers)
	}
	if servers, err := ParseServers("  "); err != nil || servers != nil {
		t.Fatalf("blank: %v %+v", err, servers)
	}
	for _, bad := range []string{
		`[{"name":"Geo","url":"https://x"}]`,
		`[{"name":"a.b","url":"https://x"}]`,
		`[{"name":"geo","url":"ftp://x"}]`,
		`[{"name":"geo","url":"https://x"},{"name":"geo","url":"https://y"}]`,
		`{"name":"geo"}`,
	} {
		if _, err := ParseServers(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestCatalogueQualifiesAndKeepsLastList(t *testing.T) {
	fake := &fakeServer{}
	backend := httptest.NewServer(fake.handler(t))
	catalogue := NewCatalogue([]Server{{Name: "geo", URL: backend.URL}}, "test")
	tools := catalogue.Tools()
	if len(tools) != 2 || tools[0].QualifiedName() != "geo.compute_commute" {
		t.Fatalf("tools: %+v", tools)
	}
	result, err := catalogue.Call(context.Background(), "geo.compute_commute", map[string]any{"origin": "x"})
	if err != nil || result.Content[0].Text != "約35分" {
		t.Fatalf("%v %+v", err, result)
	}
	if _, err := catalogue.Call(context.Background(), "other.tool", nil); err == nil {
		t.Fatal("unknown server accepted")
	}
	if _, err := catalogue.Call(context.Background(), "place_search", nil); err == nil {
		t.Fatal("unqualified name accepted")
	}
	backend.Close()
	catalogue.mu.Lock()
	catalogue.entries["geo"].fetchedAt = catalogue.entries["geo"].fetchedAt.Add(-catalogue.ttl)
	catalogue.mu.Unlock()
	if tools := catalogue.Tools(); len(tools) != 2 {
		t.Fatalf("lost the list after an outage: %+v", tools)
	}
	if server, tool, ok := Split("geo.compute_commute"); !ok || server != "geo" || tool != "compute_commute" {
		t.Fatal("split")
	}
	if _, _, ok := Split("route"); ok {
		t.Fatal("built-in name split")
	}
}
