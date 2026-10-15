package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"firebase.google.com/go/v4/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJudgeRegistrationConfiguresSharedMCPIdempotently(t *testing.T) {
	root := t.TempDir()
	key := strings.Repeat("j", 64)
	g := newGateway(AuthOptions{
		Root: root, FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		JudgeInviteToken: "invite", JudgeAPIKey: "model-key", JudgeMCPKey: key,
		JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 10,
	}, &judgeVerifier{})
	var recorded []string
	g.options.RecordJudgeWorkspace = func(_ context.Context, key string) error {
		for _, k := range recorded {
			if k == key {
				return nil
			}
		}
		recorded = append(recorded, key)
		return nil
	}
	// A stale entry (deleted or never set up) is listed but not reported.
	g.options.JudgeWorkspaces = func(context.Context) ([]string, error) {
		return append([]string{workspaceKey("ordinary")}, recorded...), nil
	}
	t.Cleanup(g.Close)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/judge", strings.NewReader(`{"token":"invite"}`))
		req.Header.Set("Authorization", "Bearer password:alice")
		res := httptest.NewRecorder()
		g.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("register %d: %d %s", i, res.Code, res.Body.String())
		}
	}
	tenant, err := g.tenant("alice")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tenant.mcpConnections(context.Background())
	if err != nil || len(rows) != 1 || rows[0].ID != judgeConnectionID || !rows[0].Active {
		t.Fatalf("connections = %+v, %v", rows, err)
	}
	callMCP := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp/users/"+workspaceKey("alice"), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res := httptest.NewRecorder()
		g.ServeHTTP(res, req)
		return res.Code
	}
	if code := callMCP(); code != http.StatusOK {
		t.Fatalf("shared MCP = %d", code)
	}
	for _, id := range tenant.store.Settings().Active {
		if tenant.store.ConfiguredSettings().Planners[id] != "external" {
			t.Fatalf("planner %s not external", id)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/judge-workspaces", nil)
	req.Host = "kakeratta.example"
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.Header.Set("Authorization", "Bearer "+key)
	res := httptest.NewRecorder()
	g.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Workspaces []map[string]string `json:"workspaces"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Workspaces) != 1 || body.Workspaces[0]["workspaceId"] != workspaceKey("alice") ||
		body.Workspaces[0]["mcpUrl"] != "http://kakeratta.example/api/mcp/users/"+workspaceKey("alice") ||
		body.Workspaces[0]["mcpPath"] != "/api/mcp/users/"+workspaceKey("alice") {
		t.Fatalf("body = %s, %v", res.Body.String(), err)
	}

	bad := httptest.NewRecorder()
	g.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/internal/judge-workspaces", nil))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", bad.Code)
	}
	g.options.JudgeExpiresAt = time.Now().Add(-time.Minute)
	if code := callMCP(); code != http.StatusUnauthorized {
		t.Fatalf("expired shared MCP = %d", code)
	}
}

func TestJudgeRegistrationKeepsTheJudgesOwnConnection(t *testing.T) {
	key := strings.Repeat("j", 64)
	g := newGateway(AuthOptions{
		Root: t.TempDir(), FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		JudgeInviteToken: "invite", JudgeAPIKey: "model-key", JudgeMCPKey: key,
		JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 10,
	}, &judgeVerifier{})
	t.Cleanup(g.Close)
	tenant, err := g.tenant("alice")
	if err != nil {
		t.Fatal(err)
	}
	own := sha256.Sum256([]byte(strings.Repeat("o", 64)))
	if err := tenant.changeMCPConnection(context.Background(), "own", "Mine", own[:], true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/judge", strings.NewReader(`{"token":"invite"}`))
		req.Header.Set("Authorization", "Bearer password:alice")
		res := httptest.NewRecorder()
		g.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("register %d: %d %s", i, res.Code, res.Body.String())
		}
	}
	rows, err := tenant.mcpConnections(context.Background())
	if err != nil || len(rows) != 2 || rows[0].ID != "own" || !rows[0].Active || rows[1].ID != judgeConnectionID || rows[1].Active {
		t.Fatalf("connections = %+v, %v", rows, err)
	}
}

func TestJudgeWorkspacesIsNotServedByWorkers(t *testing.T) {
	key := strings.Repeat("j", 64)
	g := newGateway(AuthOptions{
		Root: t.TempDir(), Mode: "worker", FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		JudgeInviteToken: "invite", JudgeAPIKey: "model-key", JudgeMCPKey: key,
		JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 10,
	}, &judgeVerifier{})
	t.Cleanup(g.Close)
	req := httptest.NewRequest(http.MethodGet, "/internal/judge-workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res := httptest.NewRecorder()
	g.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("worker judge-workspaces = %d", res.Code)
	}
}

// Lifecycle checks use live Firebase state, not just the persisted marker.
type lifecycleJudgeVerifier struct {
	judgeVerifier
	users       map[string]*auth.UserRecord
	unavailable bool
}

func (v *lifecycleJudgeVerifier) GetUser(_ context.Context, uid string) (*auth.UserRecord, error) {
	if v.unavailable {
		return nil, errors.New("unavailable")
	}
	u := v.users[uid]
	if u == nil {
		return nil, errors.New("deleted")
	}
	return u, nil
}
func TestSharedJudgeMCPHonorsFirebaseLifecycle(t *testing.T) {
	key := strings.Repeat("j", 64)
	verifier := &lifecycleJudgeVerifier{users: map[string]*auth.UserRecord{}}
	g := newGateway(AuthOptions{Root: t.TempDir(), Mode: "web",
		JudgeInviteToken: "invite", JudgeAPIKey: "model", JudgeMCPKey: key,
		JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 10}, verifier)
	t.Cleanup(g.Close)
	t.Setenv("KAKERATTA_PUBLIC_URL", "https://kakeratta.example")
	for _, uid := range []string{"alice", "bob"} {
		verifier.users[uid] = &auth.UserRecord{UserInfo: &auth.UserInfo{UID: uid}, CustomClaims: map[string]interface{}{"judge": true}}
		if err := g.setUpJudgeWorkspace(context.Background(), uid); err != nil {
			t.Fatal(err)
		}
	}
	request := func(path, token string) *httptest.ResponseRecorder {
		method := http.MethodGet
		if strings.HasPrefix(path, "/api/mcp/") {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	for _, state := range []string{"active", "disabled", "claim removed", "deleted", "auth unavailable", "rotated", "revoked", "paused"} {
		t.Run(state, func(t *testing.T) {
			user := &auth.UserRecord{UserInfo: &auth.UserInfo{UID: "alice"}, CustomClaims: map[string]interface{}{"judge": true}}
			verifier.users["alice"] = user
			verifier.unavailable = false
			g.options.JudgeMCPKey = key
			alice, _ := g.tenant("alice")
			alice.judgeMCPKey = key
			settings := alice.store.ConfiguredSettings()
			settings.ExternalAgent.Paused = state == "paused"
			if err := alice.store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "disabled":
				user.Disabled = true
			case "claim removed":
				user.CustomClaims = nil
			case "deleted":
				delete(verifier.users, "alice")
			case "auth unavailable":
				verifier.unavailable = true
			case "rotated":
				g.options.JudgeMCPKey = strings.Repeat("r", 64)
				alice.judgeMCPKey = g.options.JudgeMCPKey
			case "revoked":
				g.options.JudgeMCPKey = ""
				alice.judgeMCPKey = ""
			}
			res := request("/api/mcp/users/"+workspaceKey("alice"), key)
			if state == "active" && res.Code != 200 {
				t.Fatalf("active MCP: %d %s", res.Code, res.Body.String())
			}
			if state != "active" && res.Code < 400 {
				t.Fatalf("inactive MCP accepted: %s", state)
			}
			discoveryKey := g.options.JudgeMCPKey
			res = request("/internal/judge-workspaces", discoveryKey)
			if state == "revoked" {
				if res.Code != 401 {
					t.Fatalf("revoked discovery: %d", res.Code)
				}
				return
			}
			if res.Code != 200 {
				t.Fatalf("discovery: %d %s", res.Code, res.Body.String())
			}
			var body struct {
				Workspaces []map[string]string `json:"workspaces"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, row := range body.Workspaces {
				if state != "active" && row["workspaceId"] == workspaceKey("alice") {
					t.Fatalf("inactive judge listed: %s", state)
				}
			}
			if state == "active" && len(body.Workspaces) != 2 {
				t.Fatalf("expected two judges: %s", res.Body.String())
			}
		})
	}
}
