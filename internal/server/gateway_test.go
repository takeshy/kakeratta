package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"firebase.google.com/go/v4/auth"
)

type fakeVerifier struct{}

func (fakeVerifier) SessionCookie(_ context.Context, token string, _ time.Duration) (string, error) {
	return "session:" + token, nil
}

func (fakeVerifier) VerifySessionCookie(_ context.Context, cookie string) (*auth.Token, error) {
	return fakeToken(strings.TrimPrefix(cookie, "session:")), nil
}

func (fakeVerifier) VerifyIDToken(_ context.Context, token string) (*auth.Token, error) {
	return fakeToken(token), nil
}

func (fakeVerifier) SetCustomUserClaims(context.Context, string, map[string]interface{}) error {
	return nil
}

func fakeToken(value string) *auth.Token {
	provider := "google.com"
	claims := map[string]interface{}{}
	uid := value
	if strings.HasPrefix(value, "judge:") {
		provider, uid, claims["judge"] = "password", strings.TrimPrefix(value, "judge:"), true
	} else if strings.HasPrefix(value, "password:") {
		provider, uid = "password", strings.TrimPrefix(value, "password:")
	}
	return &auth.Token{UID: uid, Firebase: auth.FirebaseInfo{SignInProvider: provider}, Claims: claims}
}

func testGateway(t *testing.T) *Gateway {
	t.Helper()
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), Version: "test",
		FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
	}, fakeVerifier{})
	t.Cleanup(gateway.Close)
	return gateway
}

func sessionCookie(t *testing.T, gateway *Gateway, uid string) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/session", nil)
	request.Header.Set("Authorization", "Bearer "+uid)
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("session: %d %s", response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}

func TestGatewayRequiresLoginAndSeparatesUsers(t *testing.T) {
	gateway := testGateway(t)
	unauthenticated := httptest.NewRecorder()
	gateway.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/timeline", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", unauthenticated.Code)
	}

	for _, uid := range []string{"alice", "bob"} {
		cookie := sessionCookie(t, gateway, uid)
		post := httptest.NewRequest(http.MethodPost, "/api/post", strings.NewReader(`{"text":"`+uid+`"}`))
		post.Header.Set("Content-Type", "application/json")
		post.AddCookie(cookie)
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, post)
		if response.Code != http.StatusOK {
			t.Fatalf("post %s: %d %s", uid, response.Code, response.Body.String())
		}
	}

	if len(gateway.tenants) != 2 {
		t.Fatalf("tenants = %d", len(gateway.tenants))
	}
	alice, _ := gateway.tenant("alice")
	bob, _ := gateway.tenant("bob")
	if alice.store.Root() == bob.store.Root() {
		t.Fatal("users share a workspace")
	}
}

type judgeVerifier struct {
	fakeVerifier
	claimed string
}

func (v *judgeVerifier) SetCustomUserClaims(_ context.Context, uid string, claims map[string]interface{}) error {
	if claims["judge"] == true {
		v.claimed = uid
	}
	return nil
}

func TestJudgeRegistrationRequiresInviteAndPasswordThenAllowsClaimedSession(t *testing.T) {
	verifier := &judgeVerifier{}
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		JudgeInviteToken: "secret-invite", JudgeAPIKey: "judge-key", JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 200,
	}, verifier)
	t.Cleanup(gateway.Close)

	register := func(token, invite string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/judge", strings.NewReader(`{"token":"`+invite+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		gateway.ServeHTTP(res, req)
		return res
	}
	if got := register("password:alice", "wrong"); got.Code != http.StatusForbidden {
		t.Fatalf("wrong invite = %d", got.Code)
	}
	if got := register("alice", "secret-invite"); got.Code != http.StatusUnauthorized {
		t.Fatalf("google registration = %d", got.Code)
	}
	if got := register("password:alice", "secret-invite"); got.Code != http.StatusNoContent || verifier.claimed != "alice" {
		t.Fatalf("registration = %d, claim = %q", got.Code, verifier.claimed)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", nil)
	req.Header.Set("Authorization", "Bearer password:alice")
	gateway.ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unclaimed password session = %d", denied.Code)
	}
	if cookie := sessionCookie(t, gateway, "judge:alice"); cookie.Name != authCookieName {
		t.Fatalf("judge cookie = %+v", cookie)
	}
}

func TestJudgeUseRoutesAreLimitedToAIStartingActions(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		charged      bool
	}{
		{http.MethodPost, "/api/post", true}, {http.MethodPost, "/api/reply", true},
		{http.MethodPost, "/api/personas/build", true}, {http.MethodPost, "/api/entry/x/routing/check", true},
		{http.MethodPost, "/api/entry/x/reply/preview", true}, {http.MethodGet, "/api/timeline", false},
		{http.MethodPut, "/api/settings", false},
	} {
		if got := judgeUseRoute(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.charged {
			t.Errorf("%s %s = %v", tc.method, tc.path, got)
		}
	}
}

func TestGatewayProtectsAPIsWithoutGoogleSession(t *testing.T) {
	gateway := testGateway(t)
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/export"},
		{"POST", "/api/timeline/search"},
		{"GET", "/api/thread/post"},
		{"DELETE", "/api/personas/gourmet/memory/note?path=private.md"},
		{"PUT", "/api/personas/gourmet/memory/note?path=private.md"},
		{"GET", "/api/entry/post/routing"},
		{"POST", "/api/entry/post/routing/check"},
		{"POST", "/api/entry/post/reply/preview"},
		{"POST", "/api/social/preview"},
		{"POST", "/api/social/publish"},
	} {
		t.Run(endpoint.method+" "+endpoint.path, func(t *testing.T) {
			request := httptest.NewRequest(endpoint.method, endpoint.path, nil)
			request.Header.Set("Authorization", "Bearer former-shared-token")
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("without Google session: %d", response.Code)
			}
		})
	}
}

func TestLocalServerNeedsNoLoginAndHasNoTokenLogin(t *testing.T) {
	server, _ := app(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/timeline", nil))
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 0 {
		t.Fatalf("local access: %d, cookies: %v", response.Code, response.Result().Cookies())
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/auth/token?token=obsolete", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("removed login endpoint: %d", response.Code)
	}
}

// forgettingVerifier is the fake with the one thing the real auth client can
// also do: delete the account.
type forgettingVerifier struct {
	fakeVerifier
	mu      sync.Mutex
	deleted []string
}

func (v *forgettingVerifier) DeleteUser(_ context.Context, uid string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deleted = append(v.deleted, uid)
	return nil
}

// Leaving takes everything with it: the tenant, the directory it served, the
// login, the cookie. Someone else's workspace is untouched, and the next
// request under the old cookie finds nothing of what was there.
func TestGatewayClosesAnAccountAndRemovesItsWorkspace(t *testing.T) {
	verifier := &forgettingVerifier{}
	root := t.TempDir()
	gateway := newGateway(AuthOptions{
		Root: root, Version: "test",
		FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
	}, verifier)
	t.Cleanup(gateway.Close)

	for _, uid := range []string{"alice", "bob"} {
		post := httptest.NewRequest(http.MethodPost, "/api/post", strings.NewReader(`{"text":"`+uid+`"}`))
		post.Header.Set("Content-Type", "application/json")
		post.AddCookie(sessionCookie(t, gateway, uid))
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, post)
		if response.Code != http.StatusOK {
			t.Fatalf("post %s: %d %s", uid, response.Code, response.Body.String())
		}
	}
	alice := filepath.Join(root, "users", workspaceKey("alice"))
	if _, err := os.Stat(filepath.Join(alice, "timeline.jsonl")); err != nil {
		t.Fatalf("alice's timeline was not written: %v", err)
	}

	unconfirmed := httptest.NewRequest(http.MethodDelete, "/api/workspace", strings.NewReader(`{}`))
	unconfirmed.AddCookie(sessionCookie(t, gateway, "alice"))
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, unconfirmed)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed = %d %s", response.Code, response.Body.String())
	}

	closing := httptest.NewRequest(http.MethodDelete, "/api/workspace", strings.NewReader(`{"confirm":true}`))
	closing.Header.Set("Content-Type", "application/json")
	cookie := sessionCookie(t, gateway, "alice")
	closing.AddCookie(cookie)
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, closing)
	if response.Code != http.StatusNoContent {
		t.Fatalf("close = %d %s", response.Code, response.Body.String())
	}
	cleared := response.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != authCookieName || cleared[0].MaxAge != -1 {
		t.Fatalf("the session cookie was not cleared: %+v", cleared)
	}
	if _, err := os.Stat(alice); !os.IsNotExist(err) {
		t.Fatalf("alice's workspace remains: %v", err)
	}
	if verifier.deleted == nil || verifier.deleted[0] != "alice" {
		t.Fatalf("the login was not deleted: %v", verifier.deleted)
	}
	gateway.mu.Lock()
	_, kept := gateway.tenants[workspaceKey("alice")]
	_, closingStill := gateway.closing[workspaceKey("alice")]
	gateway.mu.Unlock()
	if kept || closingStill {
		t.Fatalf("tenant kept = %v, still closing = %v", kept, closingStill)
	}

	// Bob is still there.
	bob := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	bob.AddCookie(sessionCookie(t, gateway, "bob"))
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, bob)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"bob"`) {
		t.Fatalf("bob's timeline = %d %s", response.Code, response.Body.String())
	}

	// The old cookie, if the verifier still honoured it, opens an empty workspace.
	again := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	again.AddCookie(cookie)
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, again)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"alice"`) {
		t.Fatalf("after closing = %d %s", response.Code, response.Body.String())
	}
}

// The single-user server has nobody to sign out and cannot stop itself: the
// directory is the user's to remove, and the answer says so.
func TestSingleUserServerDoesNotCloseAccounts(t *testing.T) {
	server, _ := app(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/workspace", strings.NewReader(`{"confirm":true}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}
