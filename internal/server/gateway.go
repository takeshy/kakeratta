package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

const authCookieName = "kakeratta_session"

type tokenVerifier interface {
	SessionCookie(context.Context, string, time.Duration) (string, error)
	VerifySessionCookie(context.Context, string) (*auth.Token, error)
}

type AuthOptions struct {
	Root               string
	Version            string
	FirebaseProjectID  string
	FirebaseAPIKey     string
	FirebaseAuthDomain string
	BlobStore          func(userKey string) store.BlobStore
	OpenWorkspace      func(userKey string, blobs store.BlobStore) (store.Workspace, error)
}

// Gateway authenticates a request and dispatches it to a completely separate
// Server. Each user therefore gets a separate store, catalogue, reaction
// queue, speech service, and event hub rather than merely filtered responses.
type Gateway struct {
	options  AuthOptions
	verifier tokenVerifier

	mu      sync.Mutex
	tenants map[string]*Server
}

func NewGateway(ctx context.Context, options AuthOptions) (*Gateway, error) {
	if options.FirebaseProjectID == "" || options.FirebaseAPIKey == "" || options.FirebaseAuthDomain == "" {
		return nil, errors.New("Firebase project ID, API key, and auth domain are required")
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: options.FirebaseProjectID})
	if err != nil {
		return nil, err
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, err
	}
	return newGateway(options, client), nil
}

func newGateway(options AuthOptions, verifier tokenVerifier) *Gateway {
	return &Gateway{
		options: options, verifier: verifier,
		tenants: map[string]*Server{},
	}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/auth/config" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":    true,
			"projectId":  g.options.FirebaseProjectID,
			"apiKey":     g.options.FirebaseAPIKey,
			"authDomain": g.options.FirebaseAuthDomain,
		})
		return
	case r.URL.Path == "/api/auth/session" && r.Method == http.MethodPost:
		g.createSession(w, r)
		return
	case r.URL.Path == "/api/auth/logout" && r.Method == http.MethodPost:
		g.logout(w, r)
		return
	case !strings.HasPrefix(r.URL.Path, "/api/"):
		notFound(w, r)
		return
	}

	uid, ok := g.user(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "login required")
		return
	}
	tenant, err := g.tenant(uid)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tenant.ServeHTTP(w, r)
}

func (g *Gateway) createSession(w http.ResponseWriter, r *http.Request) {
	idToken := bearer(r.Header.Get("Authorization"))
	if idToken == "" {
		fail(w, http.StatusUnauthorized, "Firebase ID token required")
		return
	}
	const duration = 14 * 24 * time.Hour
	sessionCookie, err := g.verifier.SessionCookie(r.Context(), idToken, duration)
	if err != nil || sessionCookie == "" {
		fail(w, http.StatusUnauthorized, "invalid Firebase ID token")
		return
	}
	expires := time.Now().Add(duration)
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: sessionCookie, Path: "/", HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
		Expires: expires, MaxAge: int(duration.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) user(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	verified, err := g.verifier.VerifySessionCookie(r.Context(), cookie.Value)
	if err != nil || verified.UID == "" {
		return "", false
	}
	return verified.UID, true
}

func (g *Gateway) tenant(uid string) (*Server, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing := g.tenants[uid]; existing != nil {
		return existing, nil
	}
	digest := sha256.Sum256([]byte(uid))
	userKey := hex.EncodeToString(digest[:])
	var blobs store.BlobStore
	if g.options.BlobStore != nil {
		blobs = g.options.BlobStore(userKey)
	}
	var workspace store.Workspace
	var err error
	if g.options.OpenWorkspace != nil {
		workspace, err = g.options.OpenWorkspace(userKey, blobs)
	} else {
		root := filepath.Join(g.options.Root, "users", userKey)
		var local *store.Store
		local, err = store.Open(root)
		if err == nil && blobs != nil {
			local.SetBlobStore(blobs)
		}
		workspace = local
	}
	if err != nil {
		return nil, err
	}
	tenant := New(Options{
		Store: workspace, Catalogue: persona.NewCatalogue(workspace.SkillDir()),
		Version: g.options.Version,
	})
	g.tenants[uid] = tenant
	return tenant, nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (g *Gateway) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, tenant := range g.tenants {
		tenant.Close()
	}
}
