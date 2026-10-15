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
