package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPAccessToggleAndKeyRevocation(t *testing.T) {
	s, w := app(t)
	key := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(key))
	if err := s.setMCPKey(context.Background(), digest[:]); err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/mcp/users/"+s.mcpWorkspace(), strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		out := httptest.NewRecorder()
		s.ServeHTTP(out, r)
		return out
	}
	read := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_timeline","arguments":{}}}`
	for _, active := range []bool{true, false, true} {
		settings := w.Settings()
		settings.Active = nil
		if active {
			settings.Active = []string{"hermes"}
		}
		if err := w.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		out := call(read)
		if out.Code != http.StatusOK {
			t.Fatalf("response = %d: %s", out.Code, out.Body.String())
		}
		var response struct {
			Result struct {
				IsError bool            `json:"isError"`
				Data    json.RawMessage `json:"structuredContent"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.IsError == active {
			t.Fatalf("active = %v, response = %s", active, out.Body.String())
		}
		if !active && len(response.Result.Data) > 0 {
			t.Fatal("disabled persona exposed data")
		}
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/mcp/connection", nil)
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != http.StatusOK {
		t.Fatalf("revoke = %d: %s", out.Code, out.Body.String())
	}
	for _, body := range []string{read, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`} {
		out = call(body)
		if out.Code != http.StatusUnauthorized {
			t.Fatalf("revoked key accepted: %d: %s", out.Code, out.Body.String())
		}
	}
}
