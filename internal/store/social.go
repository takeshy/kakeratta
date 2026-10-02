package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Publication is independent of timeline entries: saving a remote reference
// must never overwrite a concurrent deletion or image analysis.
type Publication struct {
	State   string `json:"state"`
	URI     string `json:"uri,omitempty"`
	CID     string `json:"cid,omitempty"`
	ID      string `json:"id,omitempty"`
	URL     string `json:"url,omitempty"`
	RootURI string `json:"rootUri,omitempty"`
	RootCID string `json:"rootCid,omitempty"`
}

func publicationKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func (s *Store) Publication(key string) (Publication, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var p Publication
	err := readJSON(filepath.Join(s.root, "publications", publicationKey(key)+".json"), &p)
	if os.IsNotExist(err) {
		err = nil
	}
	return p, err
}
func (s *Store) ClaimPublication(key string, p Publication) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "publications", publicationKey(key)+".json")
	var old Publication
	if err := readJSON(path, &old); err == nil {
		if old.State == "retryable" {
			return true, writeJSON(path, p)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return true, writeJSON(path, p)
}
func (s *Store) SavePublication(key string, p Publication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.root, "publications", publicationKey(key)+".json"), p)
}
func (w *FirestoreWorkspace) Publication(key string) (Publication, error) {
	var p Publication
	doc, err := w.user.Collection("publications").Doc(publicationKey(key)).Get(context.Background())
	if status.Code(err) == codes.NotFound {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = doc.DataTo(&p)
	return p, err
}
func (w *FirestoreWorkspace) ClaimPublication(key string, p Publication) (bool, error) {
	claimed := false
	ref := w.user.Collection("publications").Doc(publicationKey(key))
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		claimed = false
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			var old Publication
			if err := doc.DataTo(&old); err != nil {
				return err
			}
			if old.State != "retryable" {
				return nil
			}
		}
		if err := tx.Set(ref, p); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}
func (w *FirestoreWorkspace) SavePublication(key string, p Publication) error {
	if p.State == "" {
		return errors.New("publication state required")
	}
	_, err := w.user.Collection("publications").Doc(publicationKey(key)).Set(context.Background(), p)
	return err
}
