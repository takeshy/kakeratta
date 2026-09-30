package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
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

	"github.com/takeshy/kakeratta/internal/cluster"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Firebase Hosting drops every cookie but one named __session before a request
// reaches Cloud Run, so that is the only name a session can have.
const authCookieName = "__session"

type tokenVerifier interface {
	SessionCookie(context.Context, string, time.Duration) (string, error)
	VerifyIDToken(context.Context, string) (*auth.Token, error)
	VerifySessionCookie(context.Context, string) (*auth.Token, error)
	SetCustomUserClaims(context.Context, string, map[string]interface{}) error
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
	Tools            tools.Config
	OpenWorkspace    func(userKey string, blobs store.BlobStore) (store.Workspace, error)
	JudgeInviteToken string
	JudgeAPIKey      string
	JudgeExpiresAt   time.Time
	JudgeMaxUses     int
	ClaimJudgeUse    func(context.Context, string, int) (int, bool, error)
	// Cluster lets several instances serve the same users. Each workspace's
	// persona work runs on the one instance holding its lease; the others
	// answer what they can and carry the rest there. Without it this process
	// is the only one and works every workspace itself.
	Cluster cluster.Coordinator
}

// Gateway authenticates a request and dispatches it to a completely separate
// Server. Each user therefore gets a separate store, catalogue, reaction
// queue, speech service, and event hub rather than merely filtered responses.
type Gateway struct {
	options  AuthOptions
	verifier tokenVerifier

	mu      sync.Mutex
	tenants map[string]*Server
	// closing names the workspaces being deleted right now, so neither a
	// request nor the restore worker opens one again halfway through.
	closing   map[string]bool
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	// With a cluster: the workspaces this instance owns, each with the
	// cancel of its call listener; when each lease was last renewed; which
	// are being let go; and when each open workspace last served a request.
	cluster    cluster.Coordinator
	owned      map[string]context.CancelFunc
	renewed    map[string]time.Time
	demoting   map[string]bool
	used       map[string]time.Time
	leasesDone chan struct{}
	demotions  sync.WaitGroup
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
		tenants: map[string]*Server{}, closing: map[string]bool{},
		cluster: options.Cluster,
		owned:   map[string]context.CancelFunc{}, renewed: map[string]time.Time{},
		demoting: map[string]bool{}, used: map[string]time.Time{},
		leasesDone: make(chan struct{}),
	}
	go g.restoreWorkspaces()
	if g.cluster != nil {
		go g.keepLeases()
	} else {
		close(g.leasesDone)
	}
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
		if g.cluster != nil {
			// Carried to the owner if another instance has it; never taken
			// over on the strength of this request.
			g.serveOwned(w, r, key, false)
			return
		}
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
	case r.URL.Path == "/api/auth/judge" && r.Method == http.MethodPost:
		g.registerJudge(w, r)
		return
	case r.URL.Path == "/api/auth/logout" && r.Method == http.MethodPost:
		g.logout(w, r)
		return
	case !strings.HasPrefix(r.URL.Path, "/api/"):
		notFound(w, r)
		return
	}

	identity, ok := g.user(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "login required")
		return
	}
	uid := identity.UID
	if isPassword(identity) && !isJudge(identity) {
		fail(w, http.StatusForbidden, "judge registration required")
		return
	}
	if isJudge(identity) && !g.judgeActive() {
		fail(w, http.StatusForbidden, "judge access has expired")
		return
	}
	if r.URL.Path == "/api/auth/identity" && r.Method == http.MethodGet {
		digest := sha256.Sum256([]byte(uid))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"key": hex.EncodeToString(digest[:]), "enabled": true})
		return
	}
	if r.URL.Path == "/api/workspace" && r.Method == http.MethodDelete {
		g.closeAccount(w, r, uid)
		return
	}
	tenant, err := g.tenant(uid)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if isJudge(identity) {
		settings := tenant.store.ConfiguredSettings()
		// The judge key is the operator's: it is sent to Token Factory only,
		// whatever a judge's settings form says.
		if settings.APIKey != g.options.JudgeAPIKey || settings.OpenRouterMode || settings.Provider != store.ProviderNebius || settings.Endpoint() != store.NebiusBaseURL {
			settings.Provider, settings.BaseURL, settings.APIKey = store.ProviderNebius, store.NebiusBaseURL, g.options.JudgeAPIKey
			settings.OpenRouterMode = false
			if err := tenant.store.SaveSettings(settings); err != nil {
				fail(w, http.StatusInternalServerError, "judge settings unavailable")
				return
			}
		}
		if judgeUseRoute(r) && g.options.ClaimJudgeUse != nil {
			_, allowed, err := g.options.ClaimJudgeUse(r.Context(), uid, g.options.JudgeMaxUses)
			if err != nil {
				fail(w, http.StatusInternalServerError, "judge usage unavailable")
				return
			}
			if !allowed {
				fail(w, http.StatusTooManyRequests, "judge usage limit reached")
				return
			}
		}
	}
	if g.cluster != nil && ownerRoute(r) {
		g.serveOwned(w, r, workspaceKey(uid), true)
		return
	}
	tenant.ServeHTTP(w, r)
}

func (g *Gateway) registerJudge(w http.ResponseWriter, r *http.Request) {
	if !g.judgeActive() {
		fail(w, http.StatusForbidden, "judge registration is closed")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if readJSON(r, &body) != nil || subtle.ConstantTimeCompare([]byte(body.Token), []byte(g.options.JudgeInviteToken)) != 1 {
		fail(w, http.StatusForbidden, "invalid judge invitation")
		return
	}
	idToken := bearer(r.Header.Get("Authorization"))
	verified, err := g.verifier.VerifyIDToken(r.Context(), idToken)
	if err != nil || verified.UID == "" || !isPassword(verified) {
		fail(w, http.StatusUnauthorized, "password account required")
		return
	}
	if err := g.verifier.SetCustomUserClaims(r.Context(), verified.UID, map[string]interface{}{"judge": true}); err != nil {
		fail(w, http.StatusInternalServerError, "judge registration failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) judgeActive() bool {
	return g.options.JudgeInviteToken != "" && g.options.JudgeAPIKey != "" && g.options.JudgeMaxUses > 0 && time.Now().Before(g.options.JudgeExpiresAt)
}

func isPassword(token *auth.Token) bool { return token.Firebase.SignInProvider == "password" }
func isJudge(token *auth.Token) bool {
	value, _ := token.Claims["judge"].(bool)
	return value
}

func judgeUseRoute(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := r.URL.Path
	return path == "/api/post" || path == "/api/reply" || path == "/api/personas/build" ||
		strings.HasSuffix(path, "/routing/check") || strings.HasSuffix(path, "/reply/preview")
}

// userDeleter is what the Firebase auth client can do beyond verifying: forget
// the account. The fake in tests may or may not offer it.
type userDeleter interface {
	DeleteUser(context.Context, string) error
}

// closeTimeout bounds one account closure. Shutting the tenant waits for the
// reaction in hand; the deletion then walks every capture and document.
const closeTimeout = 10 * time.Minute

// closeAccount is the user leaving. The tenant is taken out of service first so
// nothing lands in a workspace that is being removed, then its data goes
// wherever it was kept, then the login itself; the cookie is cleared last. The
// request that asked is answered when all of it is done, not before - a page
// that has to wait knows more than one told to assume.
func (g *Gateway) closeAccount(w http.ResponseWriter, r *http.Request, uid string) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil || !body.Confirm {
		fail(w, http.StatusBadRequest, "確認が必要です")
		return
	}
	key := workspaceKey(uid)
	g.mu.Lock()
	if g.closing[key] {
		g.mu.Unlock()
		fail(w, http.StatusConflict, "削除の途中です")
		return
	}
	g.closing[key] = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.closing, key)
		g.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if g.cluster != nil {
		// Nobody may take the workspace from here on, and whoever works it
		// lets it go - this instance at once, another at its next renewal.
		if err := g.cluster.MarkClosing(ctx, key); err != nil {
			log.Printf("close account %s: %v", key, err)
			fail(w, http.StatusInternalServerError, "削除できませんでした。もう一度お試しください")
			return
		}
		g.demote(key)
		wait, stop := context.WithTimeout(ctx, 2*cluster.LeaseTTL)
		err := g.waitForRelease(wait, key)
		stop()
		if err != nil {
			log.Printf("close account %s: %v", key, err)
			fail(w, http.StatusServiceUnavailable, "削除できませんでした。もう一度お試しください")
			return
		}
	}
	g.mu.Lock()
	tenant := g.tenants[key]
	delete(g.tenants, key)
	delete(g.used, key)
	g.mu.Unlock()
	if tenant == nil {
		// Nothing of this user's is open, but the data may still be there.
		var err error
		if tenant, err = g.open(key); err != nil {
			log.Printf("close account %s: %v", key, err)
			fail(w, http.StatusInternalServerError, "削除できませんでした。もう一度お試しください")
			return
		}
	}
	// Queued reactions go with the workspace; only the one in hand finishes.
	tenant.Halt()
	if err := tenant.destroy(ctx); err != nil {
		log.Printf("close account %s: %v", key, err)
		fail(w, http.StatusInternalServerError, "削除できませんでした。もう一度お試しください")
		return
	}
	if g.cluster != nil {
		closeWorkspace(tenant)
		go g.sweepClosed(key)
	}
	// The data is gone either way; the login is forgotten where the verifier
	// can do that, and only logged where it cannot be, so the answer to the
	// page stays true to what happened to the workspace.
	if deleter, ok := g.verifier.(userDeleter); ok {
		if err := deleter.DeleteUser(ctx, uid); err != nil {
			log.Printf("close account %s: delete user: %v", key, err)
		}
	}
	clearSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// sweepDelay is how long after a deletion the workspace is deleted once
// more. Another instance may have had it open for a request in flight when
// it was marked closing; it closes it within a minute, and what that request
// wrote in the meantime goes with the second pass.
var sweepDelay = 90 * time.Second

func (g *Gateway) sweepClosed(key string) {
	select {
	case <-g.stop:
		return
	case <-time.After(sweepDelay):
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if tenant, err := g.open(key); err != nil {
		log.Printf("sweep closed %s: %v", key, err)
	} else {
		tenant.Halt()
		if err := tenant.destroy(ctx); err != nil {
			log.Printf("sweep closed %s: %v", key, err)
		}
		closeWorkspace(tenant)
	}
	if err := g.cluster.ClearClosing(ctx, key); err != nil {
		log.Printf("sweep closed %s: %v", key, err)
	}
}

func (g *Gateway) createSession(w http.ResponseWriter, r *http.Request) {
	idToken := bearer(r.Header.Get("Authorization"))
	if idToken == "" {
		fail(w, http.StatusUnauthorized, "Firebase ID token required")
		return
	}
	verified, err := g.verifier.VerifyIDToken(r.Context(), idToken)
	if err != nil || verified.UID == "" || (isPassword(verified) && !isJudge(verified)) || (isJudge(verified) && !g.judgeActive()) {
		fail(w, http.StatusForbidden, "account is not allowed")
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
	clearSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func workspaceKey(uid string) string {
	digest := sha256.Sum256([]byte(uid))
	return hex.EncodeToString(digest[:])
}

func (g *Gateway) user(r *http.Request) (*auth.Token, bool) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	verified, err := g.verifier.VerifySessionCookie(r.Context(), cookie.Value)
	if err != nil || verified.UID == "" {
		return nil, false
	}
	return verified, true
}

func (g *Gateway) tenant(uid string) (*Server, error) {
	return g.workspace(workspaceKey(uid))
}

func (g *Gateway) workspace(userKey string) (*Server, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.used[userKey] = time.Now()
	if existing := g.tenants[userKey]; existing != nil {
		return existing, nil
	}
	if g.closing[userKey] {
		return nil, errors.New("workspace is being deleted")
	}
	if g.cluster != nil {
		// Another instance may be deleting it.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		lease, err := g.cluster.Lease(ctx, userKey)
		cancel()
		if err != nil {
			return nil, err
		}
		if lease.Closing {
			return nil, cluster.ErrClosing
		}
	}
	tenant, err := g.open(userKey)
	if err != nil {
		return nil, err
	}
	g.tenants[userKey] = tenant
	return tenant, nil
}

// open builds the Server for one workspace without keeping it.
func (g *Gateway) open(userKey string) (*Server, error) {
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
	options := Options{
		Store: workspace, Catalogue: persona.NewCatalogue(workspace.SkillDir()),
		Version: g.options.Version, Tools: g.options.Tools, Exports: exports,
	}
	if g.cluster != nil {
		// A workspace is opened to answer requests; its persona work starts
		// only once this instance holds it (see promote).
		options.Follower = true
		options.Events = clusterBus{coordinator: g.cluster, key: userKey}
	}
	tenant := New(options)
	tenant.workspaceKey = userKey
	return tenant, nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (g *Gateway) Close() {
	g.closeOnce.Do(func() {
		close(g.stop)
		<-g.done
		<-g.leasesDone
		if g.cluster != nil {
			// Owned workspaces finish the reaction in hand and are let go at
			// once, so another instance takes them up without waiting for
			// the leases to run out.
			g.mu.Lock()
			keys := make([]string, 0, len(g.owned))
			for key := range g.owned {
				keys = append(keys, key)
			}
			g.mu.Unlock()
			var wg sync.WaitGroup
			for _, key := range keys {
				wg.Add(1)
				go func(key string) {
					defer wg.Done()
					g.demote(key)
				}(key)
			}
			wg.Wait()
			g.demotions.Wait()
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		// The rest of each queue is kept for the next start to take up.
		for _, tenant := range g.tenants {
			tenant.Halt()
			if g.cluster != nil {
				closeWorkspace(tenant)
			}
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
		valid := keys[:0]
		for _, key := range keys {
			// Workspace keys are SHA-256 hashes, never paths supplied by users.
			if raw, err := hex.DecodeString(key); err == nil && len(raw) == sha256.Size {
				valid = append(valid, key)
			}
		}
		keys = valid
		if g.cluster != nil {
			g.restoreCluster(keys)
			keys = nil
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
