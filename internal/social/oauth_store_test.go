package social

import (
	"bytes"
	"context"
	"errors"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/store"
	"testing"
	"time"
)

func TestOAuthStorageEncryptedScopedAndPersistent(t *testing.T) {
	ctx := context.Background()
	w, _ := store.Open(t.TempDir())
	secret := bytes.Repeat([]byte{1}, 32)
	s, _ := NewOAuthStore(w, "user-a", secret)
	session := oauth.ClientSessionData{AccountDID: syntax.DID("did:plc:abc"), SessionID: "one", AccessToken: "private-access-token", RefreshToken: "private-refresh-token", DPoPPrivateKeyMultibase: "private-key"}
	if err := s.SaveSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	value, _ := w.SocialValue(ctx, "session:did:plc:abc:one")
	if bytes.Contains(value.Data, []byte("private")) {
		t.Fatal("unencrypted credentials")
	}
	reopened, _ := store.Open(w.Root())
	restored, _ := NewOAuthStore(reopened, "user-a", secret)
	got, err := restored.GetSession(ctx, session.AccountDID, "one")
	if err != nil || got.RefreshToken != session.RefreshToken {
		t.Fatalf("restore: %+v %v", got, err)
	}
	other, _ := NewOAuthStore(reopened, "user-b", secret)
	if _, err := other.GetSession(ctx, session.AccountDID, "one"); err == nil {
		t.Fatal("cross-user decrypt")
	}
	if err := restored.DeleteSession(ctx, session.AccountDID, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.GetSession(ctx, session.AccountDID, "one"); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
}
func TestOAuthLeaseAndExpiredRequests(t *testing.T) {
	ctx := context.Background()
	w, _ := store.Open(t.TempDir())
	s, _ := NewOAuthStore(w, "user", bytes.Repeat([]byte{1}, 32))
	release, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	release()
	release2, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if err := s.SaveAuthRequestInfo(ctx, oauth.AuthRequestData{State: "state", PKCEVerifier: "private"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAuthRequestInfo(ctx, oauth.AuthRequestData{State: "state"}); err == nil {
		t.Fatal("overwritten request")
	}
	v, _ := w.SocialValue(ctx, "request:state")
	v.Expires = time.Now().Add(-time.Minute)
	w.SwapSocialValue(ctx, "request:state", v.Version, v)
	if _, err := s.GetAuthRequestInfo(ctx, "state"); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
}
