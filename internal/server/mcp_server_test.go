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

func TestHermesIsListedOnlyAfterMCPSetup(t *testing.T) {
	s, _ := app(t)
	hasHermes := func() bool {
		t.Helper()
		out := httptest.NewRecorder()
		s.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/personas", nil))
		if out.Code != http.StatusOK {
			t.Fatalf("personas = %d: %s", out.Code, out.Body.String())
		}
		var payload struct {
			Personas []struct {
				ID string `json:"id"`
			} `json:"personas"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, row := range payload.Personas {
			if row.ID == "hermes" {
				return true
			}
		}
		return false
	}
	if hasHermes() {
		t.Fatal("Hermes was listed before MCP setup")
	}
	if err := s.setMCPKey(context.Background(), []byte("digest")); err != nil {
		t.Fatal(err)
	}
	if !hasHermes() {
		t.Fatal("Hermes was missing after MCP setup")
	}
}

// The page is served from the site's domain and /api is rewritten to Cloud
// Run, whose container sees its own run.app host. The browser's Origin is
// the site, so it is matched against the forwarded host and the public URL,
// never only against Host.
func TestIssuingAnMCPKeyBehindHostingAcceptsTheSiteOrigin(t *testing.T) {
	s, _ := app(t)
	issue := func(origin string, set func(r *http.Request)) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "https://kakeratta-2qhzs3hw7q-de.a.run.app/api/mcp/connection", nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if set != nil {
			set(r)
		}
		out := httptest.NewRecorder()
		s.ServeHTTP(out, r)
		return out.Code
	}
	forwarded := func(r *http.Request) { r.Header.Set("X-Forwarded-Host", "kakeratta.net") }

	if code := issue("https://kakeratta.net", nil); code != http.StatusForbidden {
		t.Fatalf("an origin matching neither host nor public URL passed: %d", code)
	}
	if code := issue("https://kakeratta.net", forwarded); code != http.StatusOK {
		t.Fatalf("the forwarded host was not accepted: %d", code)
	}
	t.Setenv("KAKERATTA_PUBLIC_URL", "https://kakeratta.net/")
	if code := issue("https://kakeratta.net", nil); code != http.StatusOK {
		t.Fatalf("the public URL was not accepted: %d", code)
	}
	for _, origin := range []string{"http://kakeratta.net", "https://evil.example", "https://kakeratta.net.evil.example", "https://user@kakeratta.net", "null"} {
		if code := issue(origin, forwarded); code != http.StatusForbidden {
			t.Fatalf("origin %q passed: %d", origin, code)
		}
	}
	// The container's own host still matches, and a request with no Origin
	// is not a browser's.
	if code := issue("https://kakeratta-2qhzs3hw7q-de.a.run.app", nil); code != http.StatusOK {
		t.Fatalf("the service's own origin was refused: %d", code)
	}
	if code := issue("", nil); code != http.StatusOK {
		t.Fatalf("a request without an Origin was refused: %d", code)
	}
}
