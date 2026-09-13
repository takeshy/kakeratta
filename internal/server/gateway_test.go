package server

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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
	assets := fs.FS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), Assets: assets, Version: "test",
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
	if gateway.tenants["alice"].store.Root() == gateway.tenants["bob"].store.Root() {
		t.Fatal("users share a workspace")
	}
}
