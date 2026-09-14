package social

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/store"
)

var ErrNotConnected = errors.New("Blueskyとの接続が必要です")
var ErrBusy = errors.New("Blueskyの処理が進行中です。少し待ってから操作してください")

type OAuthStore struct {
	Workspace store.Workspace
	Scope     string
	LastState string
	cipher    cipher.AEAD
}

func NewOAuthStore(w store.Workspace, scope string, secret []byte) (*OAuthStore, error) {
	key := sha256.Sum256(append([]byte("kakeratta-bluesky-storage-v1:"), secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &OAuthStore{Workspace: w, Scope: scope, cipher: aead}, nil
}
func (s *OAuthStore) read(ctx context.Context, key string, out any) error {
	v, err := s.Workspace.SocialValue(ctx, key)
	if err != nil {
		return err
	}
	if len(v.Data) < s.cipher.NonceSize() || (!v.Expires.IsZero() && !time.Now().Before(v.Expires)) {
		return ErrNotConnected
	}
	raw, err := s.cipher.Open(nil, v.Data[:s.cipher.NonceSize()], v.Data[s.cipher.NonceSize():], []byte(s.Scope+":"+key))
	if err != nil {
		return errors.New("接続情報を復号できませんでした")
	}
	return json.Unmarshal(raw, out)
}
func (s *OAuthStore) write(ctx context.Context, key string, value any, expires time.Time, create bool) error {
	var encrypted []byte
	if value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		nonce := make([]byte, s.cipher.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		encrypted = s.cipher.Seal(nonce, nonce, raw, []byte(s.Scope+":"+key))
	}
	old, err := s.Workspace.SocialValue(ctx, key)
	if err != nil {
		return err
	}
	if create && len(old.Data) > 0 {
		return errors.New("接続リクエストが重複しました")
	}
	ok, err := s.Workspace.SwapSocialValue(ctx, key, old.Version, store.SocialValue{Data: encrypted, Expires: expires})
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	return nil
}
func (s *OAuthStore) GetSession(ctx context.Context, did syntax.DID, id string) (*oauth.ClientSessionData, error) {
	var v oauth.ClientSessionData
	err := s.read(ctx, "session:"+did.String()+":"+id, &v)
	return &v, err
}
func (s *OAuthStore) SaveSession(ctx context.Context, v oauth.ClientSessionData) error {
	return s.write(ctx, "session:"+v.AccountDID.String()+":"+v.SessionID, v, time.Time{}, false)
}
func (s *OAuthStore) DeleteSession(ctx context.Context, did syntax.DID, id string) error {
	return s.write(ctx, "session:"+did.String()+":"+id, nil, time.Now(), false)
}
func (s *OAuthStore) GetAuthRequestInfo(ctx context.Context, state string) (*oauth.AuthRequestData, error) {
	var v oauth.AuthRequestData
	err := s.read(ctx, "request:"+state, &v)
	return &v, err
}
func (s *OAuthStore) SaveAuthRequestInfo(ctx context.Context, v oauth.AuthRequestData) error {
	err := s.write(ctx, "request:"+v.State, v, time.Now().Add(10*time.Minute), true)
	if err == nil {
		s.LastState = v.State
	}
	return err
}
func (s *OAuthStore) DeleteAuthRequestInfo(ctx context.Context, state string) error {
	return s.write(ctx, "request:"+state, nil, time.Now(), false)
}

type Connection struct {
	DID       syntax.DID `json:"did"`
	Handle    string     `json:"handle"`
	SessionID string     `json:"sessionId"`
}

func (s *OAuthStore) Connection(ctx context.Context) (Connection, error) {
	var v Connection
	err := s.read(ctx, "connection", &v)
	return v, err
}
func (s *OAuthStore) SaveConnection(ctx context.Context, v *Connection) error {
	if v == nil {
		return s.write(ctx, "connection", nil, time.Now(), false)
	}
	return s.write(ctx, "connection", v, time.Time{}, false)
}

// One outstanding browser flow per workspace. A browser secret binds it to
// the initiating tab; the workspace binds it to the logged-in kakeratta user.
type PendingAuth struct {
	State         string
	BrowserSecret string
	ReturnRoot    string
}

func (s *OAuthStore) Pending(ctx context.Context) (PendingAuth, error) {
	var v PendingAuth
	err := s.read(ctx, "pending", &v)
	return v, err
}
func (s *OAuthStore) SavePending(ctx context.Context, v *PendingAuth) error {
	if v == nil {
		return s.write(ctx, "pending", nil, time.Now(), false)
	}
	return s.write(ctx, "pending", v, time.Now().Add(10*time.Minute), false)
}

// Serializes refresh, callback, disconnect and publishing across Cloud Run
// instances. The request deadline is shorter than the lease.
func (s *OAuthStore) Acquire(ctx context.Context) (func(), error) {
	old, err := s.Workspace.SocialValue(ctx, "lease")
	if err != nil {
		return nil, err
	}
	if time.Now().Before(old.Expires) {
		return nil, ErrBusy
	}
	owner := store.NewID("lease")
	ok, err := s.Workspace.SwapSocialValue(ctx, "lease", old.Version, store.SocialValue{Data: []byte(owner), Expires: time.Now().Add(5 * time.Minute)})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		current, err := s.Workspace.SocialValue(ctx, "lease")
		if err == nil && string(current.Data) == owner {
			_, _ = s.Workspace.SwapSocialValue(ctx, "lease", current.Version, store.SocialValue{})
		}
	}, nil
}

var _ oauth.ClientAuthStore = (*OAuthStore)(nil)
