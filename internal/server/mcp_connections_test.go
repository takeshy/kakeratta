package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPConnectionsSwitchAndRevokeIndependently(t *testing.T) {
	s, _ := app(t)
	legacy := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(legacy))
	if err := s.setMCPKey(context.Background(), digest[:]); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	authenticate := func(key string) int {
		r := httptest.NewRequest("POST", "/api/mcp/users/"+s.mcpWorkspace(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if authenticate(legacy) != http.StatusOK {
		t.Fatal("legacy key rejected")
	}
	issued := request("POST", "/api/mcp/connection", `{"name":"Research agent"}`)
	var result struct{ ID, Key string }
	if issued.Code != 200 || json.Unmarshal(issued.Body.Bytes(), &result) != nil || result.ID == "" || len(result.Key) != 64 {
		t.Fatal(issued.Body.String())
	}
	if !s.runner.AgentLastSeen().IsZero() {
		t.Fatal("new connection inherited presence")
	}
	if authenticate(legacy) != 401 || authenticate(result.Key) != 200 {
		t.Fatal("only selected key should authenticate")
	}
	listed := request("GET", "/api/mcp/connection", "")
	if strings.Contains(listed.Body.String(), result.Key) || strings.Contains(listed.Body.String(), "digest") {
		t.Fatal("credential leaked")
	}
	if !strings.Contains(listed.Body.String(), "Hermes") || !strings.Contains(listed.Body.String(), "Research agent") {
		t.Fatal(listed.Body.String())
	}
	selected := request("PUT", "/api/mcp/connection", `{"id":"default"}`)
	if selected.Code != 200 || !s.runner.AgentLastSeen().IsZero() {
		t.Fatal(selected.Body.String())
	}
	if authenticate(legacy) != 200 || authenticate(result.Key) != 401 {
		t.Fatal("switch did not isolate credentials")
	}
	if request("PUT", "/api/mcp/connection", `{"id":"missing"}`).Code != 404 {
		t.Fatal("unknown connection accepted")
	}
	if request("DELETE", "/api/mcp/connection?id="+result.ID, "").Code != 200 || authenticate(legacy) != 200 {
		t.Fatal("revoking inactive connection broke active key")
	}
	if request("PUT", "/api/mcp/connection", `{"id":""}`).Code != 200 || authenticate(legacy) != 401 {
		t.Fatal("disabled connection still usable")
	}
}

func TestMCPConnectionWaitsBelongToEachConnection(t *testing.T) {
	s, _ := app(t)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	var hermes, research struct{ ID string }
	json.Unmarshal(request("POST", "/api/mcp/connection", `{"name":"Hermes"}`).Body.Bytes(), &hermes)
	json.Unmarshal(request("POST", "/api/mcp/connection", `{"name":"Research agent"}`).Body.Bytes(), &research)
	// A connection without waits of its own waits as the settings say.
	if claim, plan := s.activeMCPWaits(); claim != 0 || plan != 0 {
		t.Fatal(claim, plan)
	}
	if w := request("PATCH", "/api/mcp/connection", `{"id":"`+research.ID+`","claimMinutes":10,"planMinutes":60}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"claimMinutes":10`) {
		t.Fatal(w.Body.String())
	}
	if claim, plan := s.activeMCPWaits(); claim != 10*time.Minute || plan != 60*time.Minute {
		t.Fatal(claim, plan)
	}
	// Replacing the key keeps them; another connection has its own.
	request("POST", "/api/mcp/connection", `{"id":"`+research.ID+`"}`)
	if claim, _ := s.activeMCPWaits(); claim != 10*time.Minute {
		t.Fatal("the waits were lost with the old key")
	}
	request("PUT", "/api/mcp/connection", `{"id":"`+hermes.ID+`"}`)
	if claim, plan := s.activeMCPWaits(); claim != 0 || plan != 0 {
		t.Fatal(claim, plan)
	}
	if request("PATCH", "/api/mcp/connection", `{"id":"missing","claimMinutes":5}`).Code != 404 {
		t.Fatal("unknown connection accepted")
	}
}

func TestMCPIsClosedWhileTheAgentIsSwitchedOff(t *testing.T) {
	s, st := app(t)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	call := func(key string) int {
		r := httptest.NewRequest("POST", "/api/mcp/users/"+s.mcpWorkspace(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	var first, second struct{ ID, Key string }
	json.Unmarshal(request("POST", "/api/mcp/connection", `{"name":"Hermes"}`).Body.Bytes(), &first)
	json.Unmarshal(request("POST", "/api/mcp/connection", `{"name":"Research agent"}`).Body.Bytes(), &second)
	// An agent quiet for longer than the claim wait is still let in.
	if call(second.Key) != 200 {
		t.Fatal("an agent coming back was refused")
	}
	settings := st.ConfiguredSettings()
	settings.ExternalAgent.Paused = true
	body, _ := json.Marshal(settings)
	if w := request("PUT", "/api/settings", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if call(second.Key) != 403 {
		t.Fatal("a switched-off agent was let in")
	}
	if !strings.Contains(request("GET", "/api/mcp/connection", "").Body.String(), `"paused":true`) {
		t.Fatal("the page is not told the agent is switched off")
	}
	// The page says so out loud, as the switch does.
	request("PUT", "/api/settings", `{"externalAgent":{"paused":false}}`)
	if call(second.Key) != 200 {
		t.Fatal("switching back on did not let the agent in")
	}
	// Deleting the connection in use leaves another in use, never none.
	request("DELETE", "/api/mcp/connection?id="+second.ID, "")
	if call(first.Key) != 200 {
		t.Fatal("the remaining connection was not taken into use")
	}
}
