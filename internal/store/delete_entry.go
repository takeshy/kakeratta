package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrEntryMissing = errors.New("投稿が見つかりません")

func deletionEntries(entry timeline.Entry, entries []timeline.Entry) []timeline.Entry {
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	if entry.Kind == timeline.KindPost {
		return thread
	}
	return append([]timeline.Entry{entry}, timeline.Replies(thread, entry.ID)...)
}

// DeleteEntry removes the selected entry and descendants from durable storage,
// with the captures they carried.
func (s *Store) DeleteEntry(ctx context.Context, id string) ([]timeline.Entry, error) {
	return s.DeleteEntryChecked(ctx, id, "")
}

func (s *Store) DeleteEntryChecked(ctx context.Context, id, expected string) ([]timeline.Entry, error) {
	gone, err := s.deleteEntryLocked(ctx, id, expected)
	if err == nil {
		dropBlobs(s.DeleteBlob, gone)
	}
	return gone, err
}

func (s *Store) deleteEntryLocked(ctx context.Context, id, expected string) ([]timeline.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists, err := s.entryLocked(id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrEntryMissing
	}
	days := map[string]bool{}
	for member := range s.timelineThreads[timeline.RootOf(entry)] {
		days[s.timelineRefs[member].day] = true
	}
	entries, err := s.timelineDaysLocked(ctx, days)
	if err != nil {
		return nil, err
	}
	gone := deletionEntries(entry, entries)
	if expected != "" && DeletionRevision(gone) != expected {
		return nil, ErrEntryConflict
	}
	ids, affected := map[string]bool{}, map[string]bool{}
	for _, entry := range gone {
		ids[entry.ID] = true
		affected[s.timelineRefs[entry.ID].day] = true
	}
	for day := range affected {
		kept := []timeline.Entry{}
		for _, entry := range entries {
			if s.timelineRefs[entry.ID].day == day && !ids[entry.ID] {
				kept = append(kept, entry)
			}
		}
		path := s.path(timelineDir, filepath.FromSlash(day))
		if len(kept) == 0 {
			err = os.Remove(path)
		} else {
			err = writeTimelineDay(path, kept)
		}
		if err != nil {
			return nil, err
		}
		for id := range ids {
			ref, ok := s.timelineRefs[id]
			if !ok || ref.day != day {
				continue
			}
			root := timeline.RootOf(ref.entry)
			delete(s.timelineThreads[root], id)
			if len(s.timelineThreads[root]) == 0 {
				delete(s.timelineThreads, root)
			}
			delete(s.timelineRefs, id)
		}
	}
	return gone, nil
}

// UpdateEntry never creates a missing entry, so late model work cannot revive it.
func (s *Store) UpdateEntry(entry timeline.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.timelineRefs[entry.ID]; !exists {
		return ErrEntryMissing
	}
	return s.appendLocked(entry)
}

func (w *FirestoreWorkspace) UpdateEntry(entry timeline.Entry) error {
	if entry.Deleted {
		return errors.New("deletion notifications cannot be stored")
	}
	entry.SearchDirty = true
	return w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(w.timeline().Doc(entry.ID)); status.Code(err) == codes.NotFound {
			return ErrEntryMissing
		} else if err != nil {
			return err
		}
		return tx.Set(w.timeline().Doc(entry.ID), entry)
	})
}

func (w *FirestoreWorkspace) DeleteEntry(ctx context.Context, id string) ([]timeline.Entry, error) {
	return w.DeleteEntryChecked(ctx, id, "")
}

func (w *FirestoreWorkspace) DeleteEntryChecked(ctx context.Context, id, expected string) ([]timeline.Entry, error) {
	var gone []timeline.Entry
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(w.timeline().Doc(id))
		if status.Code(err) == codes.NotFound {
			return ErrEntryMissing
		}
		if err != nil {
			return err
		}
		var entry timeline.Entry
		if err := doc.DataTo(&entry); err != nil {
			return err
		}
		root := timeline.RootOf(entry)
		docs, err := tx.Documents(w.timeline().Where("Root", "==", root)).GetAll()
		if err != nil {
			return err
		}
		parent, err := tx.Get(w.timeline().Doc(root))
		if err != nil {
			return err
		}
		var post timeline.Entry
		if err := parent.DataTo(&post); err != nil {
			return err
		}
		entries := []timeline.Entry{post}
		for _, doc := range docs {
			var reply timeline.Entry
			if err := doc.DataTo(&reply); err != nil {
				return err
			}
			if reply.ID != root {
				entries = append(entries, reply)
			}
		}
		gone = deletionEntries(entry, entries)
		if expected != "" && DeletionRevision(gone) != expected {
			return ErrEntryConflict
		}
		// Read passages before any writes. Their source document gates new indexing.
		passages := []*firestore.DocumentSnapshot{}
		for _, deleted := range gone {
			docs, err := tx.Documents(w.searchPassages().Where("entryId", "==", deleted.ID)).GetAll()
			if err != nil {
				return err
			}
			passages = append(passages, docs...)
		}
		for _, deleted := range gone {
			if err := tx.Delete(w.timeline().Doc(deleted.ID)); err != nil {
				return err
			}
		}
		for _, passage := range passages {
			if err := tx.Delete(passage.Ref); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		dropBlobs(w.DeleteBlob, gone)
	}
	return gone, err
}

// A reply may only be saved while both its root and immediate parent exist.
func (w *FirestoreWorkspace) checkEntryParents(tx *firestore.Transaction, entry timeline.Entry) error {
	if entry.Kind == timeline.KindPost {
		return nil
	}
	for _, id := range []string{timeline.RootOf(entry), entry.ReplyTo} {
		if id == "" {
			continue
		}
		if _, err := tx.Get(w.timeline().Doc(id)); status.Code(err) == codes.NotFound {
			return ErrEntryMissing
		} else if err != nil {
			return err
		}
	}
	return nil
}
