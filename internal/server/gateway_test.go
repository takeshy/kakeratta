package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"firebase.google.com/go/v4/auth"
)

type fakeVerifier struct{}

func (fakeVerifier) SessionCookie(_ context.Context, token string, _ time.Duration) (string, error) {
	return "session:" + token, nil
}

func (fakeVerifier) VerifySessionCookie(_ context.Context, cookie string) (*auth.Token, error) {
	return &auth.Token{UID: strings.TrimPrefix(cookie, "session:")}, nil
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
		{"POST", "/api/capture"},
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
