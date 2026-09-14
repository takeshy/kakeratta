package store

import (
	"cloud.google.com/go/firestore"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"time"
)

// SocialValue stores encrypted OAuth data and short-lived distributed locks.
// Version provides compare-and-swap for callbacks and rotating refresh tokens.
type SocialValue struct {
	Version string
	Data    []byte
	Expires time.Time
}

func (s *Store) SocialValue(ctx context.Context, key string) (SocialValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var v SocialValue
	err := readJSON(filepath.Join(s.root, "social-state", publicationKey(key)+".json"), &v)
	if os.IsNotExist(err) {
		err = nil
	}
	return v, err
}
func (s *Store) SwapSocialValue(ctx context.Context, key, version string, next SocialValue) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "social-state", publicationKey(key)+".json")
	var old SocialValue
	if err := readJSON(path, &old); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if old.Version != version {
		return false, nil
	}
	next.Version = NewID("revision")
	return true, writeJSON(path, next)
}
func (w *FirestoreWorkspace) SocialValue(ctx context.Context, key string) (SocialValue, error) {
	var v SocialValue
	doc, err := w.user.Collection("socialState").Doc(publicationKey(key)).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = doc.DataTo(&v)
	return v, err
}
func (w *FirestoreWorkspace) SwapSocialValue(ctx context.Context, key, version string, next SocialValue) (bool, error) {
	swapped := false
	ref := w.user.Collection("socialState").Doc(publicationKey(key))
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		swapped = false
		doc, err := tx.Get(ref)
		var old SocialValue
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			if err := doc.DataTo(&old); err != nil {
				return err
			}
		}
		if old.Version != version {
			return nil
		}
		next.Version = NewID("revision")
		if err := tx.Set(ref, next); err != nil {
			return err
		}
		swapped = true
		return nil
	})
	return swapped, err
}
