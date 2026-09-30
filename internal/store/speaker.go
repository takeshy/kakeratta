package store

import (
	"context"
	"errors"
	"sort"

	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/api/iterator"
)

// SpeakerEntries is everything one speaker put on the timeline - the user, or a
// persona by its id - newest first. It reads only that speaker's entries: the
// file store the days its index says the speaker spoke on, Firestore by an
// equality query on the speaker, which needs no index of its own.
func (s *Store) SpeakerEntries(ctx context.Context, speaker string) ([]timeline.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	// The index knows who said each entry, so only the days the speaker spoke
	// on are read.
	days := map[string]bool{}
	for _, ref := range s.timelineRefs {
		if ref.entry.Speaker == speaker && !ref.entry.Deleted {
			days[ref.day] = true
		}
	}
	all, err := s.timelineDaysLocked(ctx, days)
	if err != nil {
		return nil, err
	}
	var entries []timeline.Entry
	for _, entry := range all {
		if entry.Speaker == speaker && !entry.Deleted {
			entries = append(entries, entry)
		}
	}
	newestFirst(entries)
	return entries, nil
}

func (w *FirestoreWorkspace) SpeakerEntries(ctx context.Context, speaker string) ([]timeline.Entry, error) {
	iter := w.timeline().Where("Speaker", "==", speaker).Documents(ctx)
	defer iter.Stop()
	var entries []timeline.Entry
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var entry timeline.Entry
		if err := doc.DataTo(&entry); err != nil {
			return nil, err
		}
		if !entry.Deleted {
			entries = append(entries, entry)
		}
	}
	newestFirst(entries)
	return entries, nil
}

func newestFirst(entries []timeline.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].At.Equal(entries[j].At) {
			return entries[i].At.After(entries[j].At)
		}
		return entries[i].ID > entries[j].ID
	})
}
