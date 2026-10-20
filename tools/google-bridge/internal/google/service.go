// Package google provides workspace-bound Google reads and owner-approved writes. Credentials never
// leave this package; MCP receives only explicitly selected user data.
package google

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"golang.org/x/oauth2"
)

const CallbackPath = "/api/google/callback"
const scopeBase = "https://www.googleapis.com/auth/"

var (
	ErrStorage      = errors.New("Google credential storage unavailable")
	ErrInput        = errors.New("Invalid Google connection input")
	ErrFlow         = errors.New("Google authorization expired or belongs to another browser; reconnect")
	ErrDisconnected = errors.New("Google service is not connected")
	ErrReauth       = errors.New("Google authorization expired or was revoked; reconnect")
	ErrScope        = errors.New("Google permission is missing; reconnect with the required permission")
	ErrConsent      = errors.New("Consent to share Google data with your agent and model is required")
	ErrProvider     = errors.New("Google request failed; check API activation and account permissions")
	ErrTemporary    = errors.New("Google is temporarily unavailable or rate limited; try later")
	ErrAccount      = errors.New("Connect Gmail and Calendar to the same Google account, or disconnect the other service first")
)

type Config struct {
	Origin       string
	CallbackPath string
	CurrentKey   string
	Keys         map[string][]byte
	Client       *http.Client
}

type Service struct {
	w    store.Workspace
	cfg  Config
	http *http.Client
}

func (s *Service) CallbackPath() string { return s.cfg.CallbackPath }

func New(w store.Workspace, cfg Config) (*Service, error) {
	if cfg.CallbackPath == "" {
		cfg.CallbackPath = CallbackPath
	}
	if !strings.HasPrefix(cfg.CallbackPath, "/") || strings.ContainsAny(cfg.CallbackPath, "?#\r\n\x00") {
		return nil, ErrStorage
	}
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return nil, ErrStorage
	}
	if len(cfg.Keys[cfg.CurrentKey]) != 32 {
		return nil, ErrStorage
	}
	for k, v := range cfg.Keys {
		if k == "" || len(v) != 32 {
			return nil, ErrStorage
		}
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if cfg.Client != nil {
		*client = *cfg.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 20 * time.Second
	s := &Service{w: w, cfg: cfg, http: client}
	// Remove credentials from the former direct-to-kakeratta transport, even
	// when its old encryption key is no longer configured. No decrypt needed.
	ctx, unlock, err := s.lock(context.Background())
	if err != nil {
		return nil, ErrStorage
	}
	defer unlock()
	legacy, err := w.SocialValue(ctx, s.storageKey("timeline-target"))
	if err != nil {
		return nil, ErrStorage
	}
	if len(legacy.Data) != 0 {
		if err = s.save(ctx, "timeline-target", record{}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

type clientConfig struct {
	ProjectID    string `json:"projectId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}
type connection struct {
	ID           string
	Generation   string
	Client       clientConfig
	Token        *oauth2.Token
	Scopes       []string
	Subject      string
	Email        string
	Status       string
	ModelConsent bool
	VerifiedAt   time.Time
}
type pending struct {
	Client                         clientConfig
	State, BrowserSecret, Verifier string
	Scopes                         []string
	Expires                        time.Time
}
type record struct {
	CalendarInbox *calendarSyncState
	Connection    *connection
	Pending       *pending
	Ledger        *actionLedger
	Sync          *gmailSyncState
}
type envelope struct {
	KeyVersion        string
	Nonce, Ciphertext []byte
}

func validService(name string) bool { return name == "gmail" || name == "calendar" }
func otherService(name string) string {
	if name == "gmail" {
		return "calendar"
	}
	return "gmail"
}
func (s *Service) storageKey(name string) string { return "google-v1:" + name }
func (s *Service) aad(name string) []byte {
	return []byte("kakeratta:google:v1:" + s.w.ID() + ":" + name)
}
func aead(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrStorage
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrStorage
	}
	return cipher.NewGCM(block)
}
func (s *Service) read(ctx context.Context, name string) (record, error) {
	var out record
	v, err := s.w.SocialValue(ctx, s.storageKey(name))
	if err != nil {
		return out, ErrStorage
	}
	if len(v.Data) == 0 {
		return out, nil
	}
	var e envelope
	if json.Unmarshal(v.Data, &e) != nil {
		return out, ErrStorage
	}
	a, err := aead(s.cfg.Keys[e.KeyVersion])
	if err != nil || len(e.Nonce) != a.NonceSize() {
		return out, ErrStorage
	}
	plain, err := a.Open(nil, e.Nonce, e.Ciphertext, s.aad(name))
	if err != nil || json.Unmarshal(plain, &out) != nil {
		return record{}, ErrStorage
	}
	return out, nil
}

// Callers hold the workspace's renewable google-connections lock throughout
// refresh, callback, reads and disconnect. CAS also detects stale writers.
func (s *Service) save(ctx context.Context, name string, r record) error {
	v, err := s.w.SocialValue(ctx, s.storageKey(name))
	if err != nil {
		return ErrStorage
	}
	var data []byte
	if r.Connection != nil || r.Pending != nil || r.Ledger != nil || r.Sync != nil || r.CalendarInbox != nil {
		plain, err := json.Marshal(r)
		if err != nil {
			return ErrStorage
		}
		a, err := aead(s.cfg.Keys[s.cfg.CurrentKey])
		if err != nil {
			return ErrStorage
		}
		nonce := make([]byte, a.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return ErrStorage
		}
		data, err = json.Marshal(envelope{s.cfg.CurrentKey, nonce, a.Seal(nil, nonce, plain, s.aad(name))})
		if err != nil {
			return ErrStorage
		}
	}
	ok, err := s.w.SwapSocialValue(ctx, s.storageKey(name), v.Version, store.SocialValue{Data: data})
	if err != nil || !ok {
		return ErrStorage
	}
	return nil
}
func (s *Service) lock(ctx context.Context) (context.Context, func(), error) {
	return s.w.Lock(ctx, "google-connections")
}

type Status struct {
	Service        string     `json:"service"`
	Connected      bool       `json:"connected"`
	Status         string     `json:"status"`
	ConnectionID   string     `json:"connectionId,omitempty"`
	ProjectID      string     `json:"projectId,omitempty"`
	Email          string     `json:"email,omitempty"`
	Scopes         []string   `json:"scopes"`
	ModelConsent   bool       `json:"modelConsent"`
	LastVerifiedAt *time.Time `json:"lastVerifiedAt,omitempty"`
}

func public(name string, r record) Status {
	out := Status{Service: name, Status: "disconnected", Scopes: []string{}}
	if c := r.Connection; c != nil {
		out.ConnectionID = c.ID
		out.ProjectID = c.Client.ProjectID
		out.Email = c.Email
		out.Status = c.Status
		out.Connected = c.Status == "connected"
		out.Scopes = c.Scopes
		out.ModelConsent = c.ModelConsent
		if !c.VerifiedAt.IsZero() {
			t := c.VerifiedAt
			out.LastVerifiedAt = &t
		}
	}
	return out
}
func (s *Service) Status(ctx context.Context) ([]Status, error) {
	ctx, release, err := s.lock(ctx)
	if err != nil {
		return nil, ErrStorage
	}
	defer release()
	rows := []Status{}
	for _, name := range []string{"gmail", "calendar"} {
		r, err := s.read(ctx, name)
		if err != nil {
			return nil, err
		}
		if r.Pending != nil && !time.Now().Before(r.Pending.Expires) {
			r.Pending = nil
			if err := s.save(ctx, name, r); err != nil {
				return nil, err
			}
		}
		rows = append(rows, public(name, r))
	}
	return rows, nil
}

type StartInput struct {
	Service       string `json:"service"`
	ProjectID     string `json:"projectId"`
	ClientID      string `json:"clientId"`
	ClientSecret  string `json:"clientSecret"`
	RedirectURI   string `json:"redirectUri"`
	ModelConsent  bool   `json:"modelConsent"`
	CalendarList  bool   `json:"calendarList"`
	GmailSend     bool   `json:"gmailSend"`
	GmailDrafts   bool   `json:"gmailDrafts"`
	CalendarWrite bool   `json:"calendarWrite"`
}
type StartResult struct {
	URL           string `json:"url"`
	BrowserSecret string `json:"browserSecret"`
}

var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
var clientPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$`)

func requiredScope(name string) string {
	if name == "gmail" {
		return scopeBase + "gmail.readonly"
	}
	return scopeBase + "calendar.events.readonly"
}
func (s *Service) oauth(c clientConfig, scopes []string) *oauth2.Config {
	return &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: s.cfg.Origin + s.CallbackPath(), Scopes: scopes, Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}
}
func random() string {
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		return ""
	}
	return hex.EncodeToString(v)
}
func targetServices(name string) ([]string, bool) {
	switch name {
	case "gmail":
		return []string{"gmail"}, true
	case "calendar":
		return []string{"calendar"}, true
	case "both":
		return []string{"gmail", "calendar"}, true
	default:
		return nil, false
	}
}
func (s *Service) Start(ctx context.Context, in StartInput) (StartResult, error) {
	services, ok := targetServices(in.Service)
	if !ok || in.RedirectURI != s.cfg.Origin+s.CallbackPath() {
		return StartResult{}, ErrInput
	}
	if !in.ModelConsent {
		return StartResult{}, ErrConsent
	}
	ctx, release, err := s.lock(ctx)
	if err != nil {
		return StartResult{}, ErrStorage
	}
	defer release()
	r, err := s.read(ctx, services[0])
	if err != nil {
		return StartResult{}, err
	}
	c := clientConfig{strings.TrimSpace(in.ProjectID), strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret)}
	if c.ProjectID == "" && c.ClientID == "" && c.ClientSecret == "" {
		if r.Connection != nil {
			c = r.Connection.Client
		} else if other, err := s.read(ctx, otherService(services[0])); err == nil && other.Connection != nil {
			c = other.Connection.Client
		}
	}
	if !projectPattern.MatchString(c.ProjectID) || len(c.ClientID) > 256 || !clientPattern.MatchString(c.ClientID) || len(c.ClientSecret) < 8 || len(c.ClientSecret) > 1024 || strings.ContainsAny(c.ClientSecret, "\r\n\x00") {
		return StartResult{}, ErrInput
	}
	scopes := []string{"openid", "email"}
	for _, svc := range services {
		scopes = append(scopes, requiredScope(svc))
	}
	if has(services, "calendar") && (in.CalendarList || in.CalendarWrite) {
		scopes = append(scopes, scopeBase+"calendar.calendarlist.readonly")
	}
	if has(services, "gmail") && in.GmailSend {
		scopes = append(scopes, scopeBase+"gmail.send")
	}
	if has(services, "gmail") && in.GmailDrafts {
		scopes = append(scopes, scopeBase+"gmail.compose")
	}
	if has(services, "calendar") && in.CalendarWrite {
		scopes = append(scopes, scopeBase+"calendar.events")
	}
	p := &pending{Client: c, State: random(), BrowserSecret: random(), Verifier: oauth2.GenerateVerifier(), Scopes: scopes, Expires: time.Now().Add(10 * time.Minute)}
	if p.State == "" || p.BrowserSecret == "" {
		return StartResult{}, ErrStorage
	}
	r.Pending = p
	if err = s.save(ctx, services[0], r); err != nil {
		return StartResult{}, err
	}
	return StartResult{s.oauth(c, scopes).AuthCodeURL(p.State, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(p.Verifier)), p.BrowserSecret}, nil
}

type FinishInput struct {
	Service       string `json:"service"`
	State         string `json:"state"`
	Code          string `json:"code"`
	BrowserSecret string `json:"browserSecret"`
}

func equal(a, b string) bool {
	return len(a) == 64 && len(b) == 64 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func has(scopes []string, scope string) bool {
	for _, v := range scopes {
		if v == scope {
			return true
		}
	}
	return false
}
func (s *Service) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.http)
}
func tokenSafe(t *oauth2.Token) bool {
	return t != nil && t.AccessToken != "" && len(t.AccessToken) <= 8192 && len(t.RefreshToken) <= 8192 && strings.EqualFold(t.TokenType, "Bearer") && !strings.ContainsAny(t.AccessToken+t.RefreshToken, "\r\n\x00")
}
func (s *Service) Finish(ctx context.Context, in FinishInput) (Status, error) {
	services, ok := targetServices(in.Service)
	if !ok || len(in.Code) == 0 || len(in.Code) > 4096 {
		return Status{}, ErrFlow
	}
	ctx, release, err := s.lock(ctx)
	if err != nil {
		return Status{}, ErrStorage
	}
	defer release()
	r, err := s.read(ctx, services[0])
	if err != nil {
		return Status{}, err
	}
	p := r.Pending
	if p == nil || time.Now().After(p.Expires) || !equal(p.State, in.State) || !equal(p.BrowserSecret, in.BrowserSecret) {
		return Status{}, ErrFlow
	}
	r.Pending = nil
	if err = s.save(ctx, services[0], r); err != nil {
		return Status{}, err
	}
	tok, err := s.oauth(p.Client, p.Scopes).Exchange(s.oauthContext(ctx), in.Code, oauth2.VerifierOption(p.Verifier))
	if err != nil || !tokenSafe(tok) {
		return Status{}, ErrReauth
	}
	scope, _ := tok.Extra("scope").(string)
	scopes := strings.Fields(scope)
	if !has(scopes, "openid") {
		return Status{}, ErrScope
	}
	for _, svc := range services {
		if !has(scopes, requiredScope(svc)) {
			return Status{}, ErrScope
		}
	}
	var identity struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err = s.get(ctx, tok, "https://openidconnect.googleapis.com/v1/userinfo", &identity); err != nil {
		return Status{}, err
	}
	if identity.Sub == "" || len(identity.Sub) > 256 || len(identity.Email) > 320 {
		return Status{}, ErrProvider
	}
	if len(services) == 1 {
		other, err := s.read(ctx, otherService(services[0]))
		if err != nil {
			return Status{}, err
		}
		if other.Connection != nil && other.Connection.Subject != identity.Sub {
			return Status{}, ErrAccount
		}
	}
	var first Status
	for i, svc := range services {
		rec := r
		if i > 0 {
			rec, err = s.read(ctx, svc)
			if err != nil {
				return Status{}, err
			}
			rec.Pending = nil
		}
		tokCopy := *tok
		if tokCopy.RefreshToken == "" {
			if rec.Connection != nil && rec.Connection.Token != nil && rec.Connection.Subject == identity.Sub && rec.Connection.Client == p.Client {
				tokCopy.RefreshToken = rec.Connection.Token.RefreshToken
			} else if r.Connection != nil && r.Connection.Token != nil && r.Connection.Subject == identity.Sub && r.Connection.Client == p.Client {
				tokCopy.RefreshToken = r.Connection.Token.RefreshToken
			} else {
				return Status{}, ErrReauth
			}
		}
		if tokCopy.RefreshToken == "" {
			return Status{}, ErrReauth
		}
		id := random()
		if rec.Connection != nil && rec.Connection.Subject == identity.Sub {
			id = rec.Connection.ID
		}
		if id == "" {
			return Status{}, ErrStorage
		}
		rec.Connection = &connection{ID: id, Generation: random(), Client: p.Client, Token: &tokCopy, Scopes: scopes, Subject: identity.Sub, Email: identity.Email, Status: "connected", ModelConsent: true}
		if err = s.save(ctx, svc, rec); err != nil {
			return Status{}, err
		}
		if i == 0 {
			r = rec
			first = public(svc, rec)
		}
	}
	return first, nil
}

func (s *Service) access(ctx context.Context, name string, r *record, force bool) error {
	c := r.Connection
	if c == nil {
		return ErrDisconnected
	}
	if c.Token == nil {
		return ErrStorage
	}
	if c.Status == "needs_reauth" {
		return ErrReauth
	}
	if !c.ModelConsent {
		return ErrConsent
	}
	if !has(c.Scopes, requiredScope(name)) {
		return ErrScope
	}
	old := *c.Token
	if force {
		old.Expiry = time.Now().Add(-time.Hour)
	}
	tok, err := s.oauth(c.Client, c.Scopes).TokenSource(s.oauthContext(ctx), &old).Token()
	if err != nil {
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) && retrieve.ErrorCode == "invalid_grant" {
			c.Status = "needs_reauth"
			c.Token.AccessToken = ""
			if e := s.save(ctx, name, *r); e != nil {
				return e
			}
			return ErrReauth
		}
		return ErrTemporary
	}
	if !tokenSafe(tok) {
		return ErrReauth
	}
	if scope, ok := tok.Extra("scope").(string); ok && scope != "" {
		c.Scopes = strings.Fields(scope)
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = c.Token.RefreshToken
	}
	c.Token = tok
	if err = s.save(ctx, name, *r); err != nil {
		return err
	}
	if !has(c.Scopes, requiredScope(name)) {
		return ErrScope
	}
	return nil
}

var errUnauthorized = errors.New("google unauthorized")
var errNotFound = errors.New("Google item no longer exists")

func (s *Service) get(ctx context.Context, tok *oauth2.Token, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ErrInput
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := s.http.Do(req)
	if err != nil {
		return ErrTemporary
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		return errUnauthorized
	}
	if res.StatusCode == 404 {
		return errNotFound
	}
	if res.StatusCode == 403 {
		return ErrScope
	}
	if res.StatusCode == 429 || res.StatusCode >= 500 {
		return ErrTemporary
	}
	if res.StatusCode != 200 {
		return ErrProvider
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, out) != nil {
		return ErrProvider
	}
	return nil
}
func (s *Service) Disconnect(ctx context.Context, name string, revoke bool) (bool, error) {
	if !validService(name) {
		return false, ErrInput
	}
	ctx, release, err := s.lock(ctx)
	if err != nil {
		return false, ErrStorage
	}
	defer release()
	r, err := s.read(ctx, name)
	if err != nil {
		return false, err
	}
	// Remove the local capability and pending codes even if revocation fails.
	if err = s.save(ctx, name, record{}); err != nil {
		return false, err
	}
	if name == "gmail" || name == "calendar" {
		if err = s.save(ctx, name+"-inbox", record{}); err != nil {
			return false, err
		}
	}
	if err = s.clearActionData(ctx, name); err != nil {
		return false, err
	}
	if !revoke || r.Connection == nil || r.Connection.Token == nil {
		return false, nil
	}
	token := r.Connection.Token.RefreshToken
	if token == "" {
		token = r.Connection.Token.AccessToken
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/revoke", strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return false, nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.http.Do(req)
	if err != nil {
		return false, nil
	}
	res.Body.Close()
	return res.StatusCode == 200, nil
}
