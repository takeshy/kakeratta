package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Firebase Hosting drops every cookie but one named __session before a request
// reaches Cloud Run, so that is the only name a session can have. Token mode
// uses the same name; the two never run in the same deployment.
const authCookieName = "__session"

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
	// ExportTarget is where one user's backup is put for them to download.
	ExportTarget   func(userKey string) store.ExportTarget
	ListWorkspaces func(context.Context) ([]string, error)
	// Tools is the operator's configuration, the same for every user.
	Tools         tools.Config
	OpenWorkspace func(userKey string, blobs store.BlobStore) (store.Workspace, error)
}

// Gateway authenticates a request and dispatches it to a completely separate
// Server. Each user therefore gets a separate store, catalogue, reaction
// queue, speech service, and event hub rather than merely filtered responses.
type Gateway struct {
	options  AuthOptions
	verifier tokenVerifier

	mu        sync.Mutex
	tenants   map[string]*Server
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
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
	g := &Gateway{
		stop: make(chan struct{}), done: make(chan struct{}),
		options: options, verifier: verifier,
		tenants: map[string]*Server{},
	}
	go g.restoreWorkspaces()
	return g
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/mcp/users/") {
		key := strings.TrimPrefix(r.URL.Path, "/api/mcp/users/")
		decoded, err := hex.DecodeString(key)
		if err != nil || len(decoded) != 32 || len(bearer(r.Header.Get("Authorization"))) != 64 {
			fail(w, http.StatusUnauthorized, "invalid MCP credential")
			return
		}
		// Only restored/existing workspaces accept MCP requests; unauthenticated input
		// must never create a tenant. The startup worker restores saved workspaces.
		g.mu.Lock()
		tenant := g.tenants[key]
		g.mu.Unlock()
		if tenant == nil {
			fail(w, http.StatusServiceUnavailable, "workspace unavailable; retry later")
			return
		}
		tenant.ServeHTTP(w, r)
		return
	}
	switch {
	case r.URL.Path == "/api/social/bluesky/client-metadata.json" && r.Method == http.MethodGet:
		blueskyMetadata(w, r)
		return
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
	if r.URL.Path == "/api/auth/identity" && r.Method == http.MethodGet {
		digest := sha256.Sum256([]byte(uid))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"key": hex.EncodeToString(digest[:]), "enabled": true})
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
	digest := sha256.Sum256([]byte(uid))
	return g.workspace(hex.EncodeToString(digest[:]))
}

func (g *Gateway) workspace(userKey string) (*Server, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing := g.tenants[userKey]; existing != nil {
		return existing, nil
	}
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
	var exports store.ExportTarget
	if g.options.ExportTarget != nil {
		exports = g.options.ExportTarget(userKey)
	}
	tenant := New(Options{
		Store: workspace, Catalogue: persona.NewCatalogue(workspace.SkillDir()),
		Version: g.options.Version, Tools: g.options.Tools, Exports: exports,
	})
	tenant.workspaceKey = userKey
	g.tenants[userKey] = tenant
	return tenant, nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (g *Gateway) Close() {
	g.closeOnce.Do(func() {
		close(g.stop)
		<-g.done
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, tenant := range g.tenants {
			tenant.Close()
		}
	})
}

func (g *Gateway) restoreWorkspaces() {
	defer close(g.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var keys []string
		var err error
		if g.options.ListWorkspaces != nil {
			keys, err = g.options.ListWorkspaces(ctx)
		} else if g.options.OpenWorkspace == nil {
			var dirs []os.DirEntry
			dirs, err = os.ReadDir(filepath.Join(g.options.Root, "users"))
			if os.IsNotExist(err) {
				err = nil
			}
			for _, dir := range dirs {
				if dir.IsDir() {
					keys = append(keys, dir.Name())
				}
			}
		}
		cancel()
		if err != nil {
			log.Printf("daily workspace discovery: %v", err)
		}
		for _, key := range keys {
			select {
			case <-g.stop:
				return
			default:
			}
			// Workspace keys are SHA-256 hashes, never paths supplied by users.
			if raw, err := hex.DecodeString(key); err != nil || len(raw) != sha256.Size {
				continue
			}
			if _, err := g.workspace(key); err != nil {
				log.Printf("daily workspace restore: %v", err)
			}
		}
		select {
		case <-g.stop:
			return
		case <-ticker.C:
		}
	}
}
