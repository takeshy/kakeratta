package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestDurableCASAndCheckpointFailure(t *testing.T) {
	base, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	w := New(base)
	ctx := context.Background()
	if ok, e := w.SwapSocialValue(ctx, "approval", "", store.SocialValue{Data: []byte("ciphertext")}); !ok || e != nil {
		t.Fatal(ok, e)
	}
	v, e := w.SocialValue(ctx, "approval")
	if e != nil || string(v.Data) != "ciphertext" {
		t.Fatal(v, e)
	}
	entries, e := os.ReadDir(filepath.Join(w.Root(), "social-state"))
	if e != nil {
		t.Fatal(e)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0600 {
		t.Fatal("journal permissions")
	}
	if ok, e := w.SwapSocialValue(ctx, "approval", "stale", store.SocialValue{}); ok || e != nil {
		t.Fatal("stale CAS", ok, e)
	}
	calls := 0
	w.syncPath = func(string) error { calls++; return errors.New("disk checkpoint unavailable") }
	if ok, e := w.SwapSocialValue(ctx, "approval", v.Version, store.SocialValue{Data: []byte("consumed")}); ok || e == nil || calls != 1 {
		t.Fatal("checkpoint failure did not fail closed", ok, e, calls)
	}
}
