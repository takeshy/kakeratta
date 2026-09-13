package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type nativeWorkspace struct {
	*store.Store
	pending      []store.SearchIndexEntry
	passages     []store.SearchPassage
	entries      map[string]timeline.Entry
	nearestCalls int
	replacements int
}

func (w *nativeWorkspace) Timeline() ([]timeline.Entry, error) {
	return nil, errors.New("native semantic search must not scan the timeline")
}
func (w *nativeWorkspace) PendingSearchEntries(context.Context, string) ([]store.SearchIndexEntry, error) {
	return w.pending, nil
}
func (w *nativeWorkspace) IndexedSearchPassages(context.Context, string) ([]store.SearchPassage, error) {
	return nil, nil
}
func (w *nativeWorkspace) ReplaceSearchPassages(_ context.Context, _ store.SearchIndexEntry, passages []store.SearchPassage) error {
	w.replacements++
	w.passages = append(w.passages, passages...)
	return nil
}
func (w *nativeWorkspace) NearestSearchPassages(_ context.Context, _ string, vector []float64, limit int) ([]store.SearchPassage, error) {
	w.nearestCalls++
	if len(vector) != Dimensions {
		return nil, errors.New("wrong query dimensions")
	}
	return w.passages[:min(limit, len(w.passages))], nil
}
func (w *nativeWorkspace) CurrentSearchEntry(_ context.Context, id string) (timeline.Entry, bool, error) {
	entry, exists := w.entries[id]
	return entry, exists && !entry.Deleted, nil
}
func (w *nativeWorkspace) PurgeSearchThread(context.Context, string) error { return nil }

func TestNativeSearchUsesDatabaseDistancesWithoutScanningAndChecksSources(t *testing.T) {
	local := workspace(t)
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		v := make([]float64, Dimensions)
		v[0] = 1
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer provider.Close()
	if err := local.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	post := timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: "ピノ・ノワール"}
	deleted := timeline.Entry{ID: "deleted", Kind: timeline.KindPost, Text: "ワイン", Deleted: true}
	child := timeline.Entry{ID: "child", Kind: timeline.KindReply, Root: "deleted", Text: "おいしい"}
	w := &nativeWorkspace{Store: local, entries: map[string]timeline.Entry{"p": post, "deleted": deleted, "child": child}, passages: []store.SearchPassage{
		{EntryID: "deleted", Hash: contentHash(Text(deleted)), Text: deleted.Text, Distance: 0},
		{EntryID: "child", Hash: contentHash(Text(child)), Text: child.Text, Distance: 0},
		{EntryID: "p", Hash: "outdated-hash", Text: "古い記録", Distance: 0},
		{EntryID: "p", Hash: contentHash(Text(post)), Text: post.Text, Distance: 0.25},
	}}
	got, err := New(w).Search(context.Background(), "軽い赤ワイン", "semantic", 20, 0)
	if err != nil || got.Total != 1 || got.Hits[0].Score != 0.75 || got.Hits[0].Entry.ID != "p" {
		t.Fatalf("%+v %v", got, err)
	}
	if w.nearestCalls != 2 || w.replacements != 0 {
		t.Fatalf("unexpected indexing: %+v", w)
	}
	// Only an explicitly pending entry is embedded and replaced.
	w.pending = []store.SearchIndexEntry{{Entry: post}}
	if _, err := New(w).Search(context.Background(), "軽い赤ワイン", "semantic", 20, 0); err != nil {
		t.Fatal(err)
	}
	if w.replacements != 1 {
		t.Fatal("pending entry not indexed")
	}
}
