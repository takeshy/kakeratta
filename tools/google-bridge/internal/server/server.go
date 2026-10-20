// Package googlebridge hosts the personal Google companion application. It
// does not start kakeratta, its personas, timeline or background workers.
package googlebridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	googletools "github.com/takeshy/kakeratta/tools/google-bridge/internal/google"
)

//go:embed web/*
var assets embed.FS

type Config struct {
	Google     googletools.Config
	AdminToken string
}
type Server struct {
	google          *googletools.Service
	store           store.Workspace
	origin, host    string
	altLoopbackHost string
	loopback        bool
	adminDigest     [32]byte
	secure          bool
	mux             *http.ServeMux
}

func New(w store.Workspace, cfg Config) (*Server, error) {
	admin, err := hex.DecodeString(cfg.AdminToken)
	if err != nil || len(admin) != 32 {
		return nil, errors.New("adminToken must be a random 64-character hex value")
	}
	g, err := googletools.New(w, cfg.Google)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(cfg.Google.Origin)
	var altHost string
	loopback := u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
	if loopback && u.Port() != "" {
		switch u.Hostname() {
		case "localhost":
			altHost = "127.0.0.1:" + u.Port()
		case "127.0.0.1":
			altHost = "localhost:" + u.Port()
		}
	}
	s := &Server{google: g, store: w, origin: cfg.Google.Origin, host: u.Host, altLoopbackHost: altHost, loopback: loopback, secure: u.Scheme == "https", adminDigest: sha256.Sum256([]byte(cfg.AdminToken)), mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/session", s.login)
	s.mux.HandleFunc("GET /api/session", s.guard(s.session))
	s.mux.HandleFunc("DELETE /api/session", s.guard(s.logout))
	s.mux.HandleFunc("GET /api/google/callback", googleCallback)
	if g.CallbackPath() != "/api/google/callback" {
		s.mux.HandleFunc("GET "+g.CallbackPath(), googleCallback)
	}
	s.mux.HandleFunc("GET /api/google/connection", s.guard(s.googleStatus))
	s.mux.HandleFunc("POST /api/google/connect", s.guard(s.googleStart))
	s.mux.HandleFunc("POST /api/google/finish", s.guard(s.googleFinish))
	s.mux.HandleFunc("POST /api/google/{service}/test", s.guard(s.googleTest))
	s.mux.HandleFunc("DELETE /api/google/{service}/connection", s.guard(s.googleDisconnect))
	s.mux.HandleFunc("GET /api/mcp/credential", s.guard(s.mcpStatus))
	s.mux.HandleFunc("POST /api/mcp/credential", s.guard(s.issueMCP))
	s.mux.HandleFunc("DELETE /api/mcp/credential", s.guard(s.revokeMCP))
	s.mux.HandleFunc("GET /api/timeline-target", s.guard(s.timelineTargetStatus))
	s.mux.HandleFunc("PUT /api/timeline-target", s.guard(s.saveTimelineTarget))
	s.mux.HandleFunc("DELETE /api/timeline-target", s.guard(s.clearTimelineTarget))
	s.mux.HandleFunc("GET /api/google/calendar-inbox", s.guard(s.calendarInbox))
	s.mux.HandleFunc("PUT /api/google/calendar-inbox/settings", s.guard(s.calendarInboxSettings))
	s.mux.HandleFunc("POST /api/google/calendar-inbox/sync", s.guard(s.calendarInboxSync))
	s.mux.HandleFunc("DELETE /api/google/calendar-inbox", s.guard(s.calendarInboxClear))
	s.mux.HandleFunc("GET /api/google/inbox", s.guard(s.googleInbox))
	s.mux.HandleFunc("PUT /api/google/inbox/settings", s.guard(s.googleInboxSettings))
	s.mux.HandleFunc("POST /api/google/inbox/sync", s.guard(s.googleInboxSync))
	s.mux.HandleFunc("POST /api/google/inbox/feedback", s.guard(s.googleInboxFeedback))
	s.mux.HandleFunc("DELETE /api/google/inbox", s.guard(s.googleInboxClear))
	s.mux.HandleFunc("GET /api/google/actions", s.guard(s.googleActions))
	s.mux.HandleFunc("POST /api/google/actions/{id}/decision", s.guard(s.googleDecision))
	s.mux.HandleFunc("POST /api/google/actions/{id}/execute", s.guard(s.googleExecute))
	s.mux.HandleFunc("/mcp", s.serveMCP)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Not found") })
	web, _ := fs.Sub(assets, "web")
	s.mux.Handle("/", http.FileServer(http.FS(web)))
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	// Ignore forwarded host/origin claims. A reverse proxy must preserve the
	// explicitly configured public Host. This also prevents DNS rebinding.
	if !strings.EqualFold(r.Host, s.host) {
		if s.altLoopbackHost != "" && strings.EqualFold(r.Host, s.altLoopbackHost) {
			if r.Method == "GET" || r.Method == "HEAD" {
				http.Redirect(w, r, s.origin+r.URL.RequestURI(), http.StatusSeeOther)
				return
			}
			if r.URL.Path != "/mcp" {
				fail(w, 421, "Unexpected host")
				return
			}
		} else if r.URL.Path == "/mcp" && isLocalOrLoopback(r.RemoteAddr) {
			// Local or container loopback MCP access permitted
		} else {
			fail(w, 421, "Unexpected host")
			return
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		fail(w, 403, "Cross-site request refused")
		return
	}
	if r.URL.Path != "/mcp" && r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != s.origin {
		fail(w, 403, "Origin required")
		return
	}
	s.mux.ServeHTTP(w, r)
}
func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func readJSONLimit(r *http.Request, out any, limit int64) error {
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; media != "application/json" {
		return errors.New("application/json required")
	}
	d := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid JSON")
	}
	return nil
}
func secret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
func digestMatches(secret string, digest []byte) bool {
	if len(secret) != 64 || len(digest) != 32 {
		return false
	}
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], digest) == 1
}

const sessionKey = "google-bridge-admin-session"
const mcpKey = "google-bridge-mcp-credential"
const cookieName = "google_bridge_session"

func (s *Server) swap(ctx context.Context, key string, data []byte, expires time.Time) error {
	old, err := s.store.SocialValue(ctx, key)
	if err != nil {
		return errors.New("Credential store unavailable")
	}
	ok, err := s.store.SwapSocialValue(ctx, key, old.Version, store.SocialValue{Data: data, Expires: expires})
	if err != nil || !ok {
		return errors.New("Credential changed; try again")
	}
	return nil
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if readJSONLimit(r, &in, 256) != nil || !digestMatches(in.Token, s.adminDigest[:]) {
		fail(w, 401, "Invalid management key")
		return
	}
	token := secret()
	if token == "" {
		fail(w, 503, "Could not create session")
		return
	}
	sum := sha256.Sum256([]byte(token))
	data := append(sum[:], s.adminDigest[:]...)
	if err := s.swap(r.Context(), sessionKey, data, time.Now().Add(8*time.Hour)); err != nil {
		fail(w, 503, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, 401, "Sign in required")
			return
		}
		v, err := s.store.SocialValue(r.Context(), sessionKey)
		if err != nil {
			fail(w, 503, "Credential store unavailable")
			return
		}
		if len(v.Data) != 64 || !time.Now().Before(v.Expires) || !digestMatches(cookie.Value, v.Data[:32]) || subtle.ConstantTimeCompare(v.Data[32:], s.adminDigest[:]) != 1 {
			fail(w, 401, "Sign in required")
			return
		}
		next(w, r)
	}
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"authenticated": true, "mcpUrl": s.origin + "/mcp"})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.swap(r.Context(), sessionKey, nil, time.Time{}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"authenticated": false})
}
func (s *Server) mcpStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.SocialValue(r.Context(), mcpKey)
	if err != nil {
		fail(w, 503, "Credential store unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"configured": len(v.Data) == 32, "loopback": s.loopback, "url": s.origin + "/mcp"})
}
func (s *Server) issueMCP(w http.ResponseWriter, r *http.Request) {
	token := secret()
	if token == "" {
		fail(w, 503, "Could not create key")
		return
	}
	sum := sha256.Sum256([]byte(token))
	if err := s.swap(r.Context(), mcpKey, sum[:], time.Time{}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"key": token, "url": s.origin + "/mcp"})
}
func (s *Server) revokeMCP(w http.ResponseWriter, r *http.Request) {
	if err := s.swap(r.Context(), mcpKey, nil, time.Time{}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": false})
}
func (s *Server) BootstrapLocalDefaults(ctx context.Context, timelineURL, timelineKey string) {
	if s.loopback {
		s.google.BootstrapLocalDefaults(ctx, timelineURL, timelineKey)
	}
}
