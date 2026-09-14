package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/social"
)

func blueskyMetadata(w http.ResponseWriter, r *http.Request) {
	config, _, err := social.OAuthConfig()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, 200, social.Metadata(config))
}
func (s *Server) blueskyOAuth() (*oauth.ClientApp, *social.OAuthStore, error) {
	config, key, err := social.OAuthConfig()
	if err != nil {
		return nil, nil, err
	}
	scope := s.workspaceKey
	if scope == "" {
		scope = s.store.Root()
	}
	persistence, err := social.NewOAuthStore(s.store, scope, key)
	if err != nil {
		return nil, nil, err
	}
	app := oauth.NewClientApp(&config, persistence)
	if s.oauthClient != nil {
		s.oauthClient(app)
	}
	return app, persistence, nil
}
func (s *Server) blueskyConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, persistence, err := s.blueskyOAuth()
	if err != nil {
		writeJSON(w, 200, map[string]any{"available": false, "connected": false})
		return
	}
	connection, err := persistence.Connection(r.Context())
	if err != nil && !errors.Is(err, social.ErrNotConnected) {
		fail(w, 500, "Bluesky接続情報を読み込めませんでした")
		return
	}
	writeJSON(w, 200, map[string]any{"available": true, "connected": err == nil, "did": connection.DID, "handle": connection.Handle})
}
func (s *Server) connectBluesky(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Handle     string `json:"handle"`
		ReturnRoot string `json:"returnRoot"`
	}
	if readJSONLimit(r, &body, 4<<10) != nil {
		fail(w, 400, "Blueskyのハンドルを入力してください")
		return
	}
	handle, err := syntax.ParseHandle(strings.TrimPrefix(strings.TrimSpace(body.Handle), "@"))
	if err != nil {
		fail(w, 400, "Blueskyのハンドルを入力してください（例: alice.bsky.social）")
		return
	}
	app, persistence, err := s.blueskyOAuth()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	release, err := persistence.Acquire(ctx)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	defer release()
	if old, err := persistence.Pending(ctx); err == nil {
		if err := persistence.DeleteAuthRequestInfo(ctx, old.State); err != nil {
			fail(w, 500, "接続情報を更新できませんでした")
			return
		}
	}
	// Indigo's StartAuthFlow currently ignores persistence failures; LastState
	// is set only after a successful save, so never redirect without that check.
	redirect, err := app.StartAuthFlow(ctx, handle.String())
	if err != nil || persistence.LastState == "" {
		fail(w, 502, "Blueskyの接続を開始できませんでした。ハンドルを確認して再操作してください。")
		return
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		fail(w, 500, "接続を開始できませんでした")
		return
	}
	browserSecret := hex.EncodeToString(secret[:])
	if err = persistence.SavePending(ctx, &social.PendingAuth{State: persistence.LastState, BrowserSecret: browserSecret, ReturnRoot: body.ReturnRoot}); err != nil {
		fail(w, 500, "接続情報を保存できませんでした")
		return
	}
	// Firebase Hosting only forwards __session. Bind the initiating browser with
	// an additional secret returned to its sessionStorage and sent on completion.
	writeJSON(w, 200, map[string]string{"url": redirect, "browserSecret": browserSecret})
}

// The provider callback carries no access tokens. Finish from the SPA using a
// POST so Firebase's single-cookie forwarding cannot drop browser binding.
func (s *Server) blueskyCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	params := url.Values{}
	for _, key := range []string{"state", "iss", "code", "error"} {
		if value := r.URL.Query().Get(key); len(value) <= 2048 {
			params.Set(key, value)
		}
	}
	http.Redirect(w, r, "/#bluesky-callback="+url.QueryEscape(params.Encode()), http.StatusSeeOther)
}
func (s *Server) finishBluesky(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Params        string `json:"params"`
		BrowserSecret string `json:"browserSecret"`
	}
	if readJSONLimit(r, &body, 12<<10) != nil {
		fail(w, 400, "接続結果を読み込めませんでした")
		return
	}
	params, err := url.ParseQuery(body.Params)
	if err != nil {
		fail(w, 400, "接続結果が不正です")
		return
	}
	app, persistence, err := s.blueskyOAuth()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	release, err := persistence.Acquire(ctx)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	defer release()
	pending, err := persistence.Pending(ctx)
	if err != nil || pending.State != params.Get("state") || len(body.BrowserSecret) != 64 || subtle.ConstantTimeCompare([]byte(pending.BrowserSecret), []byte(body.BrowserSecret)) != 1 {
		fail(w, 400, "接続の確認期限が切れたか、開始したブラウザーと異なります。もう一度接続してください。")
		return
	}
	if err := persistence.SavePending(ctx, nil); err != nil {
		fail(w, 500, "接続情報を更新できませんでした")
		return
	}
	session, err := app.ProcessCallback(ctx, params)
	if err != nil {
		_ = persistence.DeleteAuthRequestInfo(ctx, pending.State)
		fail(w, 400, "Blueskyとの接続が完了しませんでした。もう一度接続してください。")
		return
	}
	scopes := map[string]bool{}
	for _, scope := range session.Scopes {
		scopes[scope] = true
	}
	for _, scope := range social.Scopes {
		if !scopes[scope] {
			_ = app.Logout(ctx, session.AccountDID, session.SessionID)
			fail(w, 400, "投稿と画像添付の許可が必要です。もう一度接続してください。")
			return
		}
	}
	handle := session.AccountDID.String()
	if identity, err := app.Dir.LookupDID(ctx, session.AccountDID); err == nil {
		handle = identity.Handle.String()
	}
	previous, _ := persistence.Connection(ctx)
	connection := social.Connection{DID: session.AccountDID, Handle: handle, SessionID: session.SessionID}
	if err := persistence.SaveConnection(ctx, &connection); err != nil {
		_ = app.Logout(ctx, session.AccountDID, session.SessionID)
		fail(w, 500, "接続情報を保存できませんでした")
		return
	}
	if previous.SessionID != "" && previous.SessionID != connection.SessionID {
		_ = app.Logout(ctx, previous.DID, previous.SessionID)
	}
	writeJSON(w, 200, map[string]string{"handle": handle, "returnRoot": pending.ReturnRoot})
}
func (s *Server) disconnectBluesky(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	app, persistence, err := s.blueskyOAuth()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	release, err := persistence.Acquire(ctx)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	defer release()
	connection, err := persistence.Connection(ctx)
	if err != nil && !errors.Is(err, social.ErrNotConnected) {
		fail(w, 500, "接続情報を読み込めませんでした")
		return
	}
	if err := persistence.SaveConnection(ctx, nil); err != nil {
		fail(w, 500, "接続を解除できませんでした")
		return
	}
	if pending, err := persistence.Pending(ctx); err == nil {
		_ = persistence.DeleteAuthRequestInfo(ctx, pending.State)
	}
	if err := persistence.SavePending(ctx, nil); err != nil {
		fail(w, 500, "接続を解除できませんでした")
		return
	}
	remoteRevoked := true
	if connection.SessionID != "" {
		session, err := app.ResumeSession(ctx, connection.DID, connection.SessionID)
		if err != nil || session.Data.AuthServerRevocationEndpoint == "" {
			remoteRevoked = false
		} else if err := session.RevokeSession(ctx); err != nil {
			remoteRevoked = false
		}
		if err := persistence.DeleteSession(ctx, connection.DID, connection.SessionID); err != nil {
			fail(w, 500, "接続情報の削除を完了できませんでした")
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"disconnected": true, "remoteRevoked": remoteRevoked})
}
