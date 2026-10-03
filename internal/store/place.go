package store

import (
	"cloud.google.com/go/firestore"
	"context"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UpdatePlace changes only the location, provided it has not changed during lookup.
// Other edits survive and deleted entries are never recreated.
func (s *Store) UpdatePlace(ctx context.Context, id string, before, after timeline.Place) (timeline.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return timeline.Entry{}, false, err
	}
	entry, exists, err := s.entryLocked(id)
	if err != nil || !exists {
		return entry, false, err
	}
	if entry.Place == nil || *entry.Place != before {
		return entry, false, nil
	}
	entry.Place = &after
	err = s.appendLocked(entry)
	return entry, err == nil, err
}

func (w *FirestoreWorkspace) UpdatePlace(ctx context.Context, id string, before, after timeline.Place) (timeline.Entry, bool, error) {
	var entry timeline.Entry
	changed := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		changed = false
		doc, err := tx.Get(w.timeline().Doc(id))
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if err := doc.DataTo(&entry); err != nil {
			return err
		}
		if entry.Place == nil || *entry.Place != before {
			return nil
		}
		entry.Place = &after
		changed = true
		return tx.Update(doc.Ref, []firestore.Update{{Path: "Place", Value: after}})
	})
	return entry, changed && err == nil, err
}
