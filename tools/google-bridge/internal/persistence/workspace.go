// Package persistence adds durable checkpoints to the existing file workspace.
package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"

	"github.com/takeshy/kakeratta/internal/store"
)

type Workspace struct {
	store.Workspace
	mu       sync.Mutex
	syncPath func(string) error
}

func New(w store.Workspace) *Workspace { return &Workspace{Workspace: w, syncPath: syncPath} }
func syncPath(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// The shared store atomically renames JSON but does not fsync it. A consumed
// approval must reach stable storage before the HTTP write, including rename
// metadata. Keep this stricter behavior local to the standalone companion.
func (w *Workspace) SwapSocialValue(ctx context.Context, key, version string, next store.SocialValue) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ok, e := w.Workspace.SwapSocialValue(ctx, key, version, next)
	if e != nil || !ok {
		return ok, e
	}
	hash := sha256.Sum256([]byte(key))
	dir := filepath.Join(w.Root(), "social-state")
	path := filepath.Join(dir, hex.EncodeToString(hash[:])+".json")
	if e = os.Chmod(dir, 0700); e != nil {
		return false, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		return false, e
	}
	for _, p := range []string{path, dir, w.Root()} {
		if e = w.syncPath(p); e != nil {
			return false, e
		}
	}
	return true, nil
}
