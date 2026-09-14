package search

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type nativeIndex interface {
	PendingSearchEntries(context.Context, string) ([]store.SearchIndexEntry, error)
	IndexedSearchPassages(context.Context, string) ([]store.SearchPassage, error)
	ReplaceSearchPassages(context.Context, store.SearchIndexEntry, []store.SearchPassage) error
	NearestSearchPassages(context.Context, string, []float64, int) ([]store.SearchPassage, error)
	CurrentSearchEntry(context.Context, string) (timeline.Entry, bool, error)
	PurgeSearchThread(context.Context, string) error
}

func contentHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

func (s *Service) nativeSearch(ctx context.Context, index nativeIndex, query string, limit, offset int) (Result, error) {
	if err := s.enter(ctx); err != nil {
		return Result{}, err
	}
	defer s.leave()
	settings := s.workspace.Settings()
	modelKey := contentHash(settings.BaseURL + "\x00" + Model + "\x001024\x00passages-v1")
	if settings.APIKey == "" {
		return Result{}, errors.New("設定でAIのAPIキーを入力してください")
	}
	// Check the index before paying for backfill. Firestore returns the same
	// missing-index error even when there are no passages yet.
	queryVector, err := embed(ctx, settings, []string{"Instruct: Retrieve saved personal posts relevant to the search query.\nQuery: " + query})
	if err != nil {
		return Result{}, err
	}
	if _, err := index.NearestSearchPassages(ctx, modelKey, queryVector[0], 1); err != nil {
		return Result{}, err
	}
	pending, err := index.PendingSearchEntries(ctx, modelKey)
	if err != nil {
		return Result{}, err
	}
	for _, candidate := range pending {
		entry := candidate.Entry
		if entry.Deleted && entry.Kind == timeline.KindPost {
			if err := index.PurgeSearchThread(ctx, entry.ID); err != nil {
				return Result{}, err
			}
		}
		rootExists := entry.Kind == timeline.KindPost
		if !rootExists && timeline.RootOf(entry) != "" {
			root, exists, err := index.CurrentSearchEntry(ctx, timeline.RootOf(entry))
			if err != nil {
				return Result{}, err
			}
			rootExists = exists && root.Kind == timeline.KindPost
		}
		var passages []store.SearchPassage
		if !entry.Deleted && rootExists && Text(entry) != "" {
			old, err := index.IndexedSearchPassages(ctx, entry.ID)
			if err != nil {
				return Result{}, err
			}
			cached := map[string]firestore.Vector64{}
			for _, p := range old {
				if p.ModelKey == modelKey && validVector(p.Embedding) {
					cached[p.Text] = p.Embedding
				}
			}
			// Larger chunks keep even a near-limit Firestore document within
			// a single atomic replacement transaction.
			parts := nativeChunks(Text(entry))
			for _, part := range parts {
				vector := cached[part]
				if vector == nil {
					generated, err := embed(ctx, settings, []string{part})
					if err != nil {
						return Result{}, err
					}
					vector = generated[0]
				}
				passages = append(passages, store.SearchPassage{EntryID: entry.ID, RootID: timeline.RootOf(entry), ModelKey: modelKey, Hash: contentHash(Text(entry)), Text: part, Embedding: vector})
			}
		}
		if err := index.ReplaceSearchPassages(ctx, candidate, passages); err != nil {
			return Result{}, err
		}
	}
	nearest, err := index.NearestSearchPassages(ctx, modelKey, queryVector[0], 100)
	if err != nil {
		return Result{}, err
	}
	best := map[string]Hit{}
	current := map[string]timeline.Entry{}
	read := map[string]bool{}
	get := func(id string) (timeline.Entry, error) {
		if !read[id] {
			entry, exists, err := index.CurrentSearchEntry(ctx, id)
			if err != nil {
				return timeline.Entry{}, err
			}
			read[id] = true
			if exists {
				current[id] = entry
			}
		}
		return current[id], nil
	}
	for _, p := range nearest {
		entry, err := get(p.EntryID)
		if err != nil {
			return Result{}, err
		}
		if entry.ID == "" || contentHash(Text(entry)) != p.Hash {
			continue
		}
		root, err := get(timeline.RootOf(entry))
		if err != nil {
			return Result{}, err
		}
		if root.ID == "" || root.Kind != timeline.KindPost {
			continue
		}
		hit := Hit{Entry: entry, Snippet: excerpt(p.Text, ""), Score: 1 - p.Distance, Passage: p.Text}
		if previous, ok := best[entry.ID]; !ok || hit.Score > previous.Score {
			best[entry.ID] = hit
		}
	}
	hits := make([]Hit, 0, len(best))
	for _, hit := range best {
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Entry.At.Equal(hits[j].Entry.At) {
			return hits[i].Entry.At.After(hits[j].Entry.At)
		}
		return hits[i].Entry.ID > hits[j].Entry.ID
	})
	result := Result{Hits: []Hit{}, Total: len(hits)}
	if offset < len(hits) {
		result.Hits = hits[offset:min(len(hits), offset+limit)]
	}
	return result, nil
}

func nativeChunks(text string) []string {
	runes := []rune(text)
	var out []string
	for start := 0; start < len(runes); start += 7800 {
		end := min(start+8000, len(runes))
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return out
}
