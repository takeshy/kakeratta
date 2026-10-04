package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestSettingsEndpointRequiresExplicitKey(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		want          int
		endpoint, key string
	}{
		{"omitted", `{"baseUrl":"https://custom.example/v1"}`, 400, store.NebiusBaseURL, "secret"},
		{"null", `{"baseUrl":"https://custom.example/v1","apiKey":null}`, 400, store.NebiusBaseURL, "secret"},
		{"masked", `{"baseUrl":"https://custom.example/v1","apiKey":"********"}`, 400, store.NebiusBaseURL, "secret"},
		{"explicit", `{"baseUrl":"https://custom.example/v1","apiKey":"secret"}`, 200, "https://custom.example/v1", "secret"},
		{"cleared", `{"baseUrl":"https://custom.example/v1","apiKey":""}`, 200, "https://custom.example/v1", ""},
		{"unchanged omitted", `{"middleModel":"other"}`, 200, store.NebiusBaseURL, "secret"},
		{"unchanged null", `{"apiKey":null}`, 200, store.NebiusBaseURL, "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, s := app(t)
			settings := s.ConfiguredSettings()
			settings.APIKey = "secret"
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(tc.body)))
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
			got := s.ConfiguredSettings()
			if got.Endpoint() != tc.endpoint || got.APIKey != tc.key {
				t.Fatal("endpoint or saved key changed unexpectedly")
			}
		})
	}
}

func TestCachedWorkspaceChecksClosingOnEveryRequest(t *testing.T) {
	for _, mode := range []string{"local", "shared", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			key := workspaceKey("alice")
			called := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(http.StatusNoContent) })
			g := &Gateway{verifier: fakeVerifier{}, stop: make(chan struct{}), tenants: map[string]*Server{key: {mux: mux}}, used: map[string]time.Time{}, closing: map[string]bool{}}
			blocked := false
			if mode != "local" {
				g.shared = &Shared{Closing: func(context.Context, string) (bool, error) {
					if blocked && mode == "unavailable" {
						return false, errors.New("closing state unavailable")
					}
					return blocked, nil
				}}
			}
			request := func(path string) int {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"text":"test"}`))
				r.AddCookie(&http.Cookie{Name: authCookieName, Value: "session:alice"})
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				return w.Code
			}
			paths := []string{"/api/post", "/api/mcp/users/" + key}
			for _, path := range paths {
				if status := request(path); status != 204 {
					t.Fatalf("open workspace: %s = %d", path, status)
				}
			}
			blocked = true
			g.closing[key] = mode == "local"
			for _, path := range paths {
				if status := request(path); status < 400 {
					t.Fatalf("closing workspace accepted: %s = %d", path, status)
				}
			}
			if called != len(paths) {
				t.Fatalf("blocked requests reached tenant: calls = %d", called)
			}
		})
	}
}

// A masked key never follows a changed endpoint: whoever moves the address
// types the key again.
func TestSavedKeyDoesNotMoveToAnotherEndpoint(t *testing.T) {
	server, s := app(t)
	settings := s.ConfiguredSettings()
	settings.APIKey = "secret"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	put := func(change func(*store.Settings)) int {
		next := s.ConfiguredSettings()
		next.APIKey = "********"
		change(&next)
		body, _ := json.Marshal(next)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
		return recorder.Code
	}
	if code := put(func(next *store.Settings) { next.BaseURL = "https://attacker.example/v1" }); code != http.StatusBadRequest {
		t.Fatalf("masked key moved to a new endpoint: %d", code)
	}
	if got := s.ConfiguredSettings(); got.Endpoint() != store.NebiusBaseURL || got.APIKey != "secret" {
		t.Fatalf("settings changed: %+v", got)
	}
	if code := put(func(next *store.Settings) { next.MiddleModel = "other" }); code != http.StatusOK {
		t.Fatalf("unrelated change refused: %d", code)
	}
	if code := put(func(next *store.Settings) { next.BaseURL = "https://custom.example/v1"; next.APIKey = "secret" }); code != http.StatusOK {
		t.Fatalf("retyped key refused: %d", code)
	}
}

// A judge's settings form cannot point the operator's key anywhere but Token
// Factory.
func TestJudgeKeyStaysOnTokenFactory(t *testing.T) {
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		JudgeInviteToken: "invite", JudgeAPIKey: "judge-key", JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 200,
	}, fakeVerifier{})
	t.Cleanup(gateway.Close)
	cookie := sessionCookie(t, gateway, "judge:bob")
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		gateway.ServeHTTP(recorder, req)
		return recorder
	}
	if got := request(http.MethodGet, "/api/settings", nil); got.Code != http.StatusOK {
		t.Fatalf("settings = %d", got.Code)
	}
	tenant, err := gateway.tenant("bob")
	if err != nil {
		t.Fatal(err)
	}
	// Even a stored endpoint change, with the judge key in place, is put back
	// before the next request reaches the workspace.
	settings := tenant.store.ConfiguredSettings()
	settings.Provider, settings.BaseURL = store.ProviderOpenAI, "https://attacker.example/v1"
	if err := tenant.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, "/api/settings", nil)
	if got := tenant.store.ConfiguredSettings(); got.Endpoint() != store.NebiusBaseURL || got.APIKey != "judge-key" {
		t.Fatalf("judge settings = %s %q", got.Endpoint(), got.APIKey)
	}
}

// Another site's page cannot post into a local app that has no login.
func TestCrossSiteChangesAreRefused(t *testing.T) {
	server, s := app(t)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/post", strings.NewReader(`{"text":"planted"}`))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-site post = %d", recorder.Code)
	}
	if entries, _ := s.Timeline(); len(entries) != 0 {
		t.Fatalf("cross-site post stored: %+v", entries)
	}
	same := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/post", strings.NewReader(`{"text":"mine"}`))
	same.Header.Set("Origin", "http://127.0.0.1:8787")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, same)
	if recorder.Code != http.StatusOK {
		t.Fatalf("same-origin post = %d %s", recorder.Code, recorder.Body.String())
	}
}

// Only images are kept, and a blob is never served as something the browser
// would run.
func TestOnlyImagesAreStoredAndServedInert(t *testing.T) {
	server, s := app(t)
	page := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>"))
	body, _ := json.Marshal(postBody{Image: page})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/post", bytes.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("html capture = %d", recorder.Code)
	}
	// Object storage keeps whatever type a blob was saved with.
	s.SetBlobStore(typedBlobs{"old.svg": "image/svg+xml"})
	old := "old.svg"
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/blob/"+old, nil))
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("stored svg served as %q", got)
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers = %v", recorder.Header())
	}
}

// Two tabs sending one queued post at once store it once, and the losing
// request's image is not left behind.
func TestConcurrentResendsStoreOnePost(t *testing.T) {
	server, s := app(t)
	image := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
	body, _ := json.Marshal(postBody{ClientID: "local-12345678-1234-1234-1234-123456789abc", ClientAt: time.Now().UTC(), Image: image})
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/post", bytes.NewReader(body)))
			codes[i] = recorder.Code
		}(i)
	}
	wg.Wait()
	for _, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("codes = %v", codes)
		}
	}
	entries, _ := s.Timeline()
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	files, err := os.ReadDir(filepath.Join(s.Root(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	var blobs []string
	for _, file := range files {
		blobs = append(blobs, file.Name())
	}
	if len(blobs) != 1 || blobs[0] != entries[0].Media.BlobID {
		t.Fatalf("blobs = %v, entry blob = %s", blobs, entries[0].Media.BlobID)
	}
}

// Deleting a post removes its image too.
func TestDeletingAPostRemovesItsImage(t *testing.T) {
	server, s := app(t)
	image := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
	body, _ := json.Marshal(postBody{Image: image})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/post", bytes.NewReader(body)))
	var entry timeline.Entry
	if err := json.Unmarshal(recorder.Body.Bytes(), &entry); err != nil || entry.Media == nil {
		t.Fatalf("post = %s", recorder.Body.String())
	}
	if _, err := s.DeleteEntry(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Blob(entry.Media.BlobID); err == nil {
		t.Fatal("deleted post's image remains")
	}
}

type typedBlobs map[string]string

func (b typedBlobs) Put(string, []byte, string) error { return nil }
func (b typedBlobs) Get(name string) ([]byte, string, error) {
	return []byte("<svg onload=alert(1)>"), b[name], nil
}
func (b typedBlobs) Delete(string) error     { return nil }
func (b typedBlobs) List() ([]string, error) { return nil, nil }
func (b typedBlobs) Purge() error            { return nil }
