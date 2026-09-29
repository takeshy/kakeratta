package store

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/api/iterator"
)

// SearchTimeline reads all saved entries, honoring cancellation for Firestore.
// This is used for keyword search, not native vector search.
func SearchTimeline(ctx context.Context, workspace Workspace) ([]timeline.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, ok := workspace.(*FirestoreWorkspace)
	if !ok {
		return workspace.Timeline()
	}
	iter := w.timeline().OrderBy("At", firestore.Asc).Documents(ctx)
	defer iter.Stop()
	var entries []timeline.Entry
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		var entry timeline.Entry
		if err := doc.DataTo(&entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
}

var ErrSearchThreadMissing = errors.New("投稿が見つかりません")

// SearchThread fetches only the selected conversation in indexed stores. Polling an
// old search result must not repeatedly download the user's entire timeline.
func SearchThread(ctx context.Context, workspace Workspace, id string) ([]timeline.Entry, error) {
	if local, ok := workspace.(interface {
		SearchThread(context.Context, string) ([]timeline.Entry, error)
	}); ok {
		return local.SearchThread(ctx, id)
	}
	if w, ok := workspace.(*FirestoreWorkspace); ok {
		entry, exists, err := w.CurrentSearchEntry(ctx, id)
		if err != nil {
			return nil, err
		}
		if !exists || timeline.RootOf(entry) == "" {
			return nil, ErrSearchThreadMissing
		}
		rootID := timeline.RootOf(entry)
		docs, err := w.timeline().Where("Root", "==", rootID).Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		root, exists, err := w.CurrentSearchEntry(ctx, rootID)
		if err != nil {
			return nil, err
		}
		if !exists || root.Kind != timeline.KindPost {
			return nil, ErrSearchThreadMissing
		}
		thread := []timeline.Entry{root}
		for _, doc := range docs {
			var reply timeline.Entry
			if err := doc.DataTo(&reply); err != nil {
				return nil, err
			}
			if !reply.Deleted && reply.ID != rootID {
				thread = append(thread, reply)
			}
		}
		sortEntries(thread)
		return thread, nil
	}
	entries, err := SearchTimeline(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ID != id || entry.Deleted {
			continue
		}
		thread := timeline.Thread(entries, timeline.RootOf(entry))
		for _, root := range thread {
			if root.ID == timeline.RootOf(entry) && root.Kind == timeline.KindPost {
				return thread, nil
			}
		}
	}
	return nil, ErrSearchThreadMissing
}

// SearchVector is an immutable, content-addressed embedding. It contains no
// source text; search always checks the current timeline before returning a hit.
type SearchVector struct {
	Vector []float64 `json:"vector"`
}

func searchVectorPath(root, key string) (string, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid embedding key")
	}
	return filepath.Join(root, "search-embeddings", key+".json"), nil
}

func (s *Store) SearchVector(ctx context.Context, key string) (SearchVector, error) {
	if err := ctx.Err(); err != nil {
		return SearchVector{}, err
	}
	path, err := searchVectorPath(s.root, key)
	if err != nil {
		return SearchVector{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var value SearchVector
	err = readJSON(path, &value)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return value, err
}

func (s *Store) SaveSearchVector(ctx context.Context, key string, value SearchVector) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := searchVectorPath(s.root, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeJSON(path, value)
}

// SearchTimelineWindow reads only relevant local dates, including reply parents.
func SearchTimelineWindow(ctx context.Context, workspace Workspace, from, to time.Time) ([]timeline.Entry, error) {
	if local, ok := workspace.(interface {
		TimelineWindow(context.Context, time.Time, time.Time) ([]timeline.Entry, error)
	}); ok {
		return local.TimelineWindow(ctx, from, to)
	}
	return SearchTimeline(ctx, workspace)
}
