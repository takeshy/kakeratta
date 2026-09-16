package store

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Wakes remembers which personas were woken for a post.
//
// It is how deleting a post reaches the personas that may have written it down,
// without the app ever reading a note: the app knows who it woke, and each
// persona decides for itself whether anything of its memory rested on it.
func wakesPath(root string) string { return filepath.Join(root, "wakes.json") }

func (s *Store) readWakes() (map[string][]string, error) {
	woke := map[string][]string{}
	if err := readJSON(wakesPath(s.root), &woke); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return woke, nil
}

// RecordWake notes that one persona was woken for one post.
func (s *Store) RecordWake(entryID, skill string) error {
	if entryID == "" || skill == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	woke, err := s.readWakes()
	if err != nil {
		return err
	}
	for _, id := range woke[entryID] {
		if id == skill {
			return nil
		}
	}
	woke[entryID] = append(woke[entryID], skill)
	return writeJSON(wakesPath(s.root), woke)
}

// Woke lists the personas woken for one post.
func (s *Store) Woke(entryID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	woke, err := s.readWakes()
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), woke[entryID]...)
	sort.Strings(out)
	return out, nil
}

// ForgetWakes drops the record for one post, once it has been acted on.
func (s *Store) ForgetWakes(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	woke, err := s.readWakes()
	if err != nil {
		return err
	}
	if _, ok := woke[entryID]; !ok {
		return nil
	}
	delete(woke, entryID)
	return writeJSON(wakesPath(s.root), woke)
}

type wakeRecord struct {
	Skills []string `json:"skills"`
}

func (w *FirestoreWorkspace) RecordWake(entryID, skill string) error {
	if entryID == "" || skill == "" {
		return nil
	}
	ref := w.user.Collection("wakes").Doc(sanitise(entryID))
	return w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		var record wakeRecord
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			if err := doc.DataTo(&record); err != nil {
				return err
			}
		}
		for _, id := range record.Skills {
			if id == skill {
				return nil
			}
		}
		record.Skills = append(record.Skills, skill)
		return tx.Set(ref, record)
	})
}

func (w *FirestoreWorkspace) Woke(entryID string) ([]string, error) {
	doc, err := w.user.Collection("wakes").Doc(sanitise(entryID)).Get(context.Background())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	var record wakeRecord
	if err := doc.DataTo(&record); err != nil {
		return nil, err
	}
	sort.Strings(record.Skills)
	return record.Skills, nil
}

func (w *FirestoreWorkspace) ForgetWakes(entryID string) error {
	_, err := w.user.Collection("wakes").Doc(sanitise(entryID)).Delete(context.Background())
	return err
}
