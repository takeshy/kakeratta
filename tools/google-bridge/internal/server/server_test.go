package googlebridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	googletools "github.com/takeshy/kakeratta/tools/google-bridge/internal/google"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

const adminKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCalendarImportSettingsRequireOwnerManagementSession(t *testing.T) {
	s, cookie := app(t)
	for _, path := range []string{"/api/google/calendar-inbox", "/api/google/calendar-inbox/settings", "/api/google/calendar-inbox/sync"} {
		method := "GET"
		if strings.HasSuffix(path, "settings") {
			method = "PUT"
		}
		if strings.HasSuffix(path, "sync") {
			method = "POST"
		}
		if out := web(s, nil, method, path, `{}`); out.Code == 200 {
			t.Fatal("Unauthenticated Calendar management", path)
		}
	}
	if out := web(s, cookie, "GET", "/api/google/calendar-inbox", ""); out.Code != 200 || !strings.Contains(out.Body.String(), `"enabled":false`) {
		t.Fatal(out.Body.String())
	}
	ownerKey := key(t, s, cookie)
	other, _ := app(t)
	if out := mcp(other, ownerKey, "tools/call", "calendar_sync", `{}`); out.Code == 200 {
		t.Fatal("MCP key crossed owner boundary")
	}
	if out := mcp(s, ownerKey, "tools/call", "calendar_sync", `{"owner":"other"}`); !strings.Contains(out.Body.String(), `"isError":true`) {
		t.Fatal("Calendar sync accepted owner selection", out.Body.String())
	}
}

func app(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(w, Config{AdminToken: adminKey, Google: googletools.Config{Origin: "https://app.example", CurrentKey: "v1", Keys: map[string][]byte{"v1": []byte(strings.Repeat("k", 32))}, Client: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host + r.URL.Path {
		case "oauth2.googleapis.com/token":
			return reply(200, `{"access_token":"private-access","refresh_token":"private-refresh","token_type":"Bearer","expires_in":3600,"scope":"openid email https://www.googleapis.com/auth/gmail.readonly"}`), nil
		case "openidconnect.googleapis.com/v1/userinfo":
			return reply(200, `{"sub":"google-owner","email":"me@example.com"}`), nil
		case "gmail.googleapis.com/gmail/v1/users/me/messages":
			if r.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("incorrect access token")
			}
			return reply(200, `{"messages":[{"id":"message-one","threadId":"thread-one"}]}`), nil
		}
		t.Errorf("unexpected endpoint %s", r.URL.Host+r.URL.Path)
		return reply(500, `{}`), nil
	})}}})
	if err != nil {
		t.Fatal(err)
	}
	out := web(s, nil, "POST", "/api/session", `{"token":"`+adminKey+`"}`)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	return s, out.Result().Cookies()[0]
}
func web(s *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://app.example")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func mcp(s *Server, key, method, name, args string) *httptest.ResponseRecorder {
	params := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if method == "initialize" {
		params["params"] = map[string]string{"protocolVersion": "2025-11-25"}
	}
	if method == "tools/call" {
		params["params"] = map[string]any{"name": name, "arguments": json.RawMessage(args)}
	}
	data, _ := json.Marshal(params)
	r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	return out
}
func key(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	out := web(s, cookie, "POST", "/api/mcp/credential", `{}`)
	var v struct {
		Key string `json:"key"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &v) != nil {
		t.Fatal(out.Body.String())
	}
	return v.Key
}

func TestIndependentWebOAuthAndMCPIsolation(t *testing.T) {
	a, ca := app(t)
	b, cb := app(t)
	ka, kb := key(t, a, ca), key(t, b, cb)
	start := web(a, ca, "POST", "/api/google/connect", `{"service":"gmail","projectId":"my-project","clientId":"123-personal.apps.googleusercontent.com","clientSecret":"private-client","redirectUri":"https://app.example/api/google/callback","modelConsent":true}`)
	if start.Code != 200 {
		t.Fatal(start.Body.String())
	}
	var flow googletools.StartResult
	json.Unmarshal(start.Body.Bytes(), &flow)
	u, _ := url.Parse(flow.URL)
	body, _ := json.Marshal(googletools.FinishInput{Service: "gmail", Code: "code", State: u.Query().Get("state"), BrowserSecret: flow.BrowserSecret})
	if out := web(b, cb, "POST", "/api/google/finish", string(body)); out.Code != 400 {
		t.Fatal("cross-instance OAuth accepted")
	}
	if out := web(a, ca, "POST", "/api/google/finish", string(body)); out.Code != 200 || strings.Contains(out.Body.String(), "message-one") {
		t.Fatal("finish", out.Code, out.Body.String())
	}
	if out := mcp(a, ka, "tools/call", "gmail_search", `{"query":"is:unread"}`); !strings.Contains(out.Body.String(), "message-one") || strings.Contains(out.Body.String(), "private-") {
		t.Fatal(out.Body.String())
	}
	if out := mcp(b, kb, "tools/call", "gmail_search", `{}`); !strings.Contains(out.Body.String(), `"isError":true`) {
		t.Fatal("unconnected instance read")
	}
	if out := mcp(a, kb, "tools/call", "gmail_search", `{}`); out.Code != 401 {
		t.Fatal("other instance's key accepted")
	}
	for _, args := range []string{`{"owner":"other"}`, `{"query":null}`, `{"maxResults":51}`, `{"query":true}`} {
		out := mcp(a, ka, "tools/call", "gmail_search", args)
		if !strings.Contains(out.Body.String(), `"isError":true`) {
			t.Fatal("bad args accepted", out.Body.String())
		}
	}
	for _, name := range []string{"gmail_send", "calendar_create"} {
		if out := mcp(a, ka, "tools/call", name, `{}`); !strings.Contains(out.Body.String(), "Unknown tool") {
			t.Fatal("write tool exposed")
		}
	}
	status := web(a, ca, "GET", "/api/google/connection", "")
	for _, secret := range []string{"private-client", "private-access", "private-refresh"} {
		if strings.Contains(status.Body.String(), secret) {
			t.Fatal("credential in status")
		}
	}
	if out := web(a, ca, "DELETE", "/api/google/gmail/connection", ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out := mcp(a, ka, "tools/call", "gmail_search", `{}`); !strings.Contains(out.Body.String(), `"isError":true`) {
		t.Fatal("read after disconnect")
	}
}
func TestManagementAndMCPHaveSeparateAuthentication(t *testing.T) {
	s, c := app(t)
	k := key(t, s, c)
	for _, path := range []string{"/api/session", "/api/google/connection", "/api/mcp/credential"} {
		if out := web(s, nil, "GET", path, ""); out.Code != 401 {
			t.Fatal("unguarded management API", path)
		}
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe management cookie")
	}
	if out := mcp(s, adminKey, "tools/call", "google_connection_status", `{}`); out.Code != 401 {
		t.Fatal("management key authorized MCP")
	}
	if out := web(s, nil, "POST", "/api/session", `{"token":"`+k+`"}`); out.Code != 401 {
		t.Fatal("MCP key authorized management")
	}
	if out := mcp(s, k, "initialize", "", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "personal-google-bridge") {
		t.Fatal(out.Body.String())
	}
	if out := mcp(s, k, "tools/list", "", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "gmail_get") {
		t.Fatal(out.Body.String())
	}
	newKey := key(t, s, c)
	if newKey == k {
		t.Fatal("key was not rotated")
	}
	if out := mcp(s, k, "ping", "", ""); out.Code != 401 {
		t.Fatal("old key still valid")
	}
	if out := web(s, c, "DELETE", "/api/mcp/credential", ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out := mcp(s, newKey, "ping", "", ""); out.Code != 401 {
		t.Fatal("revoked key still valid")
	}
	if out := web(s, c, "DELETE", "/api/session", ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out := web(s, c, "GET", "/api/google/connection", ""); out.Code != 401 {
		t.Fatal("logout did not end session")
	}
}
func TestOriginHostExpiryAndCallbackGuards(t *testing.T) {
	s, c := app(t)
	for _, origin := range []string{"https://evil.example", ""} {
		r := httptest.NewRequest("POST", "https://app.example/api/mcp/credential", strings.NewReader(`{}`))
		r.AddCookie(c)
		r.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		s.ServeHTTP(out, r)
		if out.Code != 403 {
			t.Fatal("cross-site write accepted")
		}
	}
	r := httptest.NewRequest("GET", "https://evil.example/", nil)
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 421 {
		t.Fatal("unexpected Host accepted")
	}
	out = web(s, nil, "GET", "/api/google/callback?state=s&code=c&token=private", "")
	if out.Code != 303 || strings.Contains(out.Header().Get("Location"), "private") || out.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("unsafe callback")
	}
	v, _ := s.store.SocialValue(context.Background(), sessionKey)
	s.store.SwapSocialValue(context.Background(), sessionKey, v.Version, store.SocialValue{Data: v.Data, Expires: time.Now().Add(-time.Second)})
	if out := web(s, c, "GET", "/api/google/connection", ""); out.Code != 401 {
		t.Fatal("expired session accepted")
	}
	if out := web(s, nil, "GET", "/", ""); out.Code != 200 || !strings.Contains(out.Body.String(), "Google Bridge") || !strings.Contains(out.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("standalone UI not served safely")
	}
}

func TestManagementKeyChangeInvalidatesExistingSession(t *testing.T) {
	s, c := app(t)
	s.adminDigest = sha256.Sum256([]byte(strings.Repeat("b", 64)))
	if out := web(s, c, "GET", "/api/session", ""); out.Code != 401 {
		t.Fatal("old management session survived key change")
	}
}

func TestDirectRequestRunsOnceWithoutWebApproval(t *testing.T) {
	s, cookie := app(t)
	// Use the same workspace and keys, with a fake provider that grants send.
	var writes int
	g, err := googletools.New(s.store, googletools.Config{Origin: s.origin, CurrentKey: "v1", Keys: map[string][]byte{"v1": []byte(strings.Repeat("k", 32))}, Client: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host + r.URL.Path {
		case "oauth2.googleapis.com/token":
			return reply(200, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600,"scope":"openid email https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send"}`), nil
		case "openidconnect.googleapis.com/v1/userinfo":
			return reply(200, `{"sub":"owner","email":"me@example.com"}`), nil
		case "gmail.googleapis.com/gmail/v1/users/me/messages/send":
			writes++
			return reply(200, `{"id":"sent1"}`), nil
		}
		return reply(200, `{"messages":[]}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s.google = g
	flow, err := g.Start(context.Background(), googletools.StartInput{Service: "gmail", ProjectID: "my-project", ClientID: "123.apps.googleusercontent.com", ClientSecret: "client-secret", RedirectURI: s.origin + googletools.CallbackPath, ModelConsent: true, GmailSend: true})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(flow.URL)
	if _, err = g.Finish(context.Background(), googletools.FinishInput{Service: "gmail", State: u.Query().Get("state"), Code: "code", BrowserSecret: flow.BrowserSecret}); err != nil {
		t.Fatal(err)
	}
	credential := key(t, s, cookie)
	for _, name := range []string{"google_action_propose", "google_action_execute", "google_action_approve"} {
		if out := mcp(s, credential, "tools/call", name, `{}`); !strings.Contains(out.Body.String(), "Unknown tool") {
			t.Fatal("legacy approval tool exposed", name, out.Body.String())
		}
	}
	mail := `"type":"gmail_send","idempotencyKey":"direct-test-operation","mail":{"to":["actual@example.com"],"subject":"hi","body":"outgoing"}`
	if out := mcp(s, credential, "tools/call", "google_action_run", `{`+mail+`}`); !strings.Contains(out.Body.String(), `"isError":true`) || writes != 0 {
		t.Fatal("run without the owner's request", out.Body.String())
	}
	args := `{` + mail + `,"userRequest":"actual@example.comにhiと送って"}`
	for range 2 {
		out := mcp(s, credential, "tools/call", "google_action_run", args)
		if !strings.Contains(out.Body.String(), `"status":"succeeded"`) {
			t.Fatal(out.Body.String())
		}
	}
	if writes != 1 {
		t.Fatal("duplicate writes", writes)
	}
	out := web(s, cookie, "GET", "/api/google/actions", "")
	if strings.Contains(out.Body.String(), "outgoing") || !strings.Contains(out.Body.String(), `"decidedBy":"direct"`) || !strings.Contains(out.Body.String(), "actual@example.comにhiと送って") {
		t.Fatal(out.Body.String())
	}
	other, _ := app(t)
	if out = mcp(other, credential, "tools/call", "google_action_run", args); out.Code != 401 {
		t.Fatal("cross instance execution", out.Code)
	}
	for _, bad := range []string{`{"type":"gmail_send","idempotencyKey":"direct-test-other-key","userRequest":"x","approved":true,"mail":{"to":["a@example.com"],"subject":"hi","body":"x"}}`, `{"type":"gmail_send","idempotencyKey":"direct-test-other-key","userRequest":"x","mail":{"to":["a@example.com"],"subject":"hi","body":"x","bcc":["evil@example.com"]}}`} {
		out = mcp(s, credential, "tools/call", "google_action_run", bad)
		if !strings.Contains(out.Body.String(), `"isError":true`) {
			t.Fatal(out.Body.String())
		}
	}
	if writes != 1 {
		t.Fatal("invalid arguments wrote", writes)
	}
}

func TestLoopbackKeylessAndKakerattaKeyMCP(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(w, Config{AdminToken: adminKey, Google: googletools.Config{Origin: "http://localhost:8132", CurrentKey: "v1", Keys: map[string][]byte{"v1": []byte(strings.Repeat("k", 32))}}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost:8132/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("loopback keyless MCP rejected", out.Code, out.Body.String())
	}
	kakerattaKey := strings.Repeat("c", 64)
	if _, err := s.google.SetTimelineTarget(context.Background(), googletools.TimelineTargetInput{Enabled: true, URL: "https://kakeratta.example/api/mcp/users/me", Key: kakerattaKey}); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:8132/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	r.Header.Set("Authorization", "Bearer "+kakerattaKey)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	out = httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("kakeratta timeline key rejected on MCP", out.Code, out.Body.String())
	}
}
