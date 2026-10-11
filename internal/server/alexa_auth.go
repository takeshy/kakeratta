package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

const alexaCredential = "alexa-credential"

// One revocable Alexa connection per workspace. Only credential hashes persist.
type alexaCredentials struct {
	Code, Access, Refresh                string
	Redirect                             string
	CodeUntil, AccessUntil, RefreshUntil time.Time
}

func alexaHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func alexaMatches(value, digest string) bool {
	return value != "" && digest != "" && subtle.ConstantTimeCompare([]byte(alexaHash(value)), []byte(digest)) == 1
}

func alexaTokenWorkspace(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ""
	}
	for _, part := range parts {
		if b, err := hex.DecodeString(part); err != nil || len(b) != 32 {
			return ""
		}
	}
	return parts[0]
}

func (s *Server) newAlexaToken() string {
	return s.mcpWorkspace() + "." + hex.EncodeToString(randomAlexaBytes())
}
func randomAlexaBytes() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func (s *Server) alexaConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	v, err := s.store.SocialValue(r.Context(), alexaCredential)
	if err != nil {
		fail(w, 503, "Alexa連携を読み込めませんでした")
		return
	}
	var saved alexaCredentials
	if len(v.Data) > 0 && json.Unmarshal(v.Data, &saved) != nil {
		fail(w, 503, "Alexa連携を読み込めませんでした")
		return
	}
	if r.Method == http.MethodDelete {
		ok, err := s.store.SwapSocialValue(r.Context(), alexaCredential, v.Version, store.SocialValue{})
		if err != nil || !ok {
			fail(w, 409, "Alexa連携を解除できませんでした。再試行してください")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": s.alexa.Enabled(), "connected": saved.Refresh != "" && time.Now().Before(saved.RefreshUntil)})
}

func (s *Server) authorizeAlexa(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.alexa.Enabled() {
		fail(w, 404, "Alexa連携は設定されていません")
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		Redirect     string `json:"redirect_uri"`
		State        string `json:"state"`
		ResponseType string `json:"response_type"`
		Scope        string `json:"scope"`
	}
	if readJSON(r, &body) != nil || body.ClientID != s.alexa.ClientID || body.ResponseType != "code" || body.State == "" || len(body.State) > 4096 || !s.alexa.AllowsRedirect(body.Redirect) || (body.Scope != "" && body.Scope != "alexa") {
		fail(w, 400, "Alexaアプリから連携を開始してください")
		return
	}
	v, err := s.store.SocialValue(r.Context(), alexaCredential)
	if err != nil {
		fail(w, 503, "Alexa連携を保存できませんでした")
		return
	}
	// Preserve the current connection until the new authorization is exchanged.
	var saved alexaCredentials
	if len(v.Data) > 0 && json.Unmarshal(v.Data, &saved) != nil {
		fail(w, 503, "Alexa連携を読み込めませんでした")
		return
	}
	code := s.newAlexaToken()
	saved.Code, saved.CodeUntil, saved.Redirect = alexaHash(code), time.Now().Add(5*time.Minute), body.Redirect
	data, _ := json.Marshal(saved)
	ok, err := s.store.SwapSocialValue(r.Context(), alexaCredential, v.Version, store.SocialValue{Data: data})
	if err != nil || !ok {
		fail(w, 409, "連携が更新されています。再試行してください")
		return
	}
	u, _ := url.Parse(body.Redirect)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", body.State)
	u.RawQuery = q.Encode()
	writeJSON(w, 200, map[string]string{"redirect": u.String()})
}

func (s *Server) exchangeAlexa(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	v, err := s.store.SocialValue(r.Context(), alexaCredential)
	var saved alexaCredentials
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	if json.Unmarshal(v.Data, &saved) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	valid := false
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		valid = alexaMatches(r.Form.Get("code"), saved.Code) && now.Before(saved.CodeUntil) && r.Form.Get("redirect_uri") == saved.Redirect
	case "refresh_token":
		valid = alexaMatches(r.Form.Get("refresh_token"), saved.Refresh) && now.Before(saved.RefreshUntil)
	}
	if !valid {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	access := s.newAlexaToken()
	refresh := r.Form.Get("refresh_token")
	if r.Form.Get("grant_type") == "authorization_code" {
		refresh = s.newAlexaToken()
		saved.Refresh, saved.RefreshUntil = alexaHash(refresh), now.Add(365*24*time.Hour)
		saved.Code, saved.Redirect = "", ""
	}
	saved.Access, saved.AccessUntil = alexaHash(access), now.Add(time.Hour)
	data, _ := json.Marshal(saved)
	ok, err := s.store.SwapSocialValue(r.Context(), alexaCredential, v.Version, store.SocialValue{Data: data})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600, "scope": "alexa"})
}

// Public endpoints resolve only existing tenants; neither tokens nor Alexa IDs
// can create a workspace or authenticate the ordinary browser/MCP APIs.
func (g *Gateway) existingAlexaWorkspace(r *http.Request, token string) *Server {
	key := alexaTokenWorkspace(token)
	if key == "" {
		return nil
	}
	g.mu.Lock()
	tenant := g.tenants[key]
	g.mu.Unlock()
	if tenant == nil {
		if g.shared != nil {
			ok, err := g.shared.Exists(r.Context(), key)
			if err != nil || !ok {
				return nil
			}
		} else {
			// File deployments can resolve persisted links after a restart.
			if g.options.OpenWorkspace != nil {
				return nil
			}
			info, err := os.Stat(filepath.Join(g.options.Root, "users", key))
			if err != nil || !info.IsDir() {
				return nil
			}
		}
	}
	tenant, _ = g.workspace(key)
	return tenant
}

func (g *Gateway) alexaToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c := g.options.Alexa
	if !c.Enabled() {
		notFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	client, secret, basic := r.BasicAuth()
	if basic {
		client, _ = url.QueryUnescape(client)
		secret, _ = url.QueryUnescape(secret)
	} else {
		client, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if client != c.ClientID || subtle.ConstantTimeCompare([]byte(secret), []byte(c.ClientSecret)) != 1 {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	r.Form = r.PostForm
	token := r.Form.Get("code")
	if r.Form.Get("grant_type") == "refresh_token" {
		token = r.Form.Get("refresh_token")
	}
	tenant := g.existingAlexaWorkspace(r, token)
	if tenant == nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	tenant.exchangeAlexa(w, r)
}
