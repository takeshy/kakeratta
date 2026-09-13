package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func workspace(t *testing.T) *store.Store {
	t.Helper()
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func save(t *testing.T, w *store.Store, entries ...timeline.Entry) {
	t.Helper()
	for _, e := range entries {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeywordSearchIncludesOlderMediaAndRepliesButNotDeletedThreads(t *testing.T) {
	w := workspace(t)
	old := timeline.Entry{ID: "old", Kind: timeline.KindPost, Text: "Ｃａｆｅ", At: time.Unix(1, 0), Media: &timeline.Media{Text: "赤ワイン"}}
	save(t, w, old,
		timeline.Entry{ID: "reply", Kind: timeline.KindReply, Root: "old", Text: "Cafe 赤ワイン おいしい", At: time.Unix(2, 0)},
		timeline.Entry{ID: "deleted", Kind: timeline.KindPost, Text: "Cafe 赤ワイン", Deleted: true},
		timeline.Entry{ID: "hidden", Kind: timeline.KindReply, Root: "deleted", Text: "Cafe 赤ワイン"},
		timeline.Entry{ID: "orphan", Kind: timeline.KindReply, Root: "missing", Text: "Cafe 赤ワイン"})
	for i := 0; i < 25; i++ {
		save(t, w, timeline.Entry{ID: store.NewID("p"), Kind: timeline.KindPost, Text: "最近の投稿", At: time.Now()})
	}
	s := New(w)
	got, err := s.Search(context.Background(), "cafe 赤ワイン", "keyword", 1, 0)
	if err != nil || got.Total != 2 || len(got.Hits) != 1 || got.Hits[0].Entry.ID != "reply" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.Search(context.Background(), "cafe 赤ワイン", "keyword", 1, 1)
	if err != nil || len(got.Hits) != 1 || got.Hits[0].Entry.ID != "old" {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.Contains(got.Hits[0].Snippet, "Ｃａｆｅ") {
		t.Fatal("excerpt changed original text")
	}
	other := New(workspace(t))
	got, err = other.Search(context.Background(), "cafe", "keyword", 20, 0)
	if err != nil || got.Total != 0 {
		t.Fatalf("cross-workspace results: %+v %v", got, err)
	}
	for _, query := range []string{"", "　 ", strings.Repeat("あ", 501)} {
		if _, err := s.Search(context.Background(), query, "keyword", 20, 0); !errors.Is(err, ErrQuery) {
			t.Fatalf("query accepted: %q %v", query, err)
		}
	}
}

func TestSemanticRanksParaphraseAndPersistsCacheAcrossRestart(t *testing.T) {
	w := workspace(t)
	var documentInputs atomic.Int32
	var queryInputs atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("wrong endpoint or auth")
		}
		var body struct {
			Model      string
			Input      []string
			Dimensions int
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != Model || body.Dimensions != Dimensions {
			t.Error("wrong model configuration")
		}
		rows := []map[string]any{}
		for i, input := range body.Input {
			v := make([]float64, Dimensions)
			if strings.HasPrefix(input, "Instruct:") {
				queryInputs.Add(1)
				v[0] = 1
			} else {
				documentInputs.Add(1)
				if strings.Contains(input, "ピノ") {
					v[0] = 1
				} else {
					v[1] = 1
				}
			}
			// Reverse response order to exercise the API's explicit index mapping.
			rows = append([]map[string]any{{"index": i, "embedding": v}}, rows...)
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	save(t, w, timeline.Entry{ID: "wine", Kind: timeline.KindPost, Text: "ピノ・ノワールが気に入った"},
		timeline.Entry{ID: "tech", Kind: timeline.KindPost, Text: "Goの実装"})
	for i := 0; i < 2; i++ {
		got, err := New(w).Search(context.Background(), "軽い赤のお酒", "semantic", 1, 0)
		if err != nil || len(got.Hits) != 1 || got.Hits[0].Entry.ID != "wine" {
			t.Fatalf("%+v %v", got, err)
		}
	}
	if documentInputs.Load() != 2 || queryInputs.Load() != 2 {
		t.Fatalf("cache miss: documents=%d queries=%d", documentInputs.Load(), queryInputs.Load())
	}
	save(t, w, timeline.Entry{ID: "wine", Kind: timeline.KindPost, Text: "ピノ・ノワールが気に入った", Media: &timeline.Media{Describe: "ラベル"}})
	if _, err := New(w).Search(context.Background(), "軽い赤のお酒", "semantic", 1, 0); err != nil {
		t.Fatal(err)
	}
	if documentInputs.Load() != 3 {
		t.Fatal("updated media did not invalidate cache")
	}
}

func TestEmbeddingRejectsMalformedResponses(t *testing.T) {
	for _, response := range []string{
		`{"data":[]}`, `{"data":[{"index":0,"embedding":[1]}]}`, `{"data":[{"index":2,"embedding":[1]}]}`, "not json",
	} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
		_, err := embed(context.Background(), store.Settings{BaseURL: provider.URL}, []string{"query"})
		provider.Close()
		if err == nil {
			t.Fatalf("accepted malformed response: %s", response)
		}
	}
}

func TestSemanticDoesNotReturnPostDeletedDuringIndexing(t *testing.T) {
	w := workspace(t)
	post := timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: "ワイン"}
	save(t, w, post)
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		post.Deleted = true
		if err := w.Append(post); err != nil {
			t.Error(err)
		}
		v := make([]float64, Dimensions)
		v[0] = 1
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	got, err := New(w).Search(context.Background(), "ワイン", "semantic", 20, 0)
	if err != nil || got.Total != 0 {
		t.Fatalf("deleted during indexing: %+v %v", got, err)
	}
}

func TestSemanticReportsBusyInsteadOfQueueingBehindIndexing(t *testing.T) {
	w := workspace(t)
	save(t, w, timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: "ワイン"})
	s := New(w)
	s.gateWait = 20 * time.Millisecond
	s.gate <- struct{}{} // another search is indexing
	started := time.Now()
	if _, err := s.Search(context.Background(), "ワイン", "semantic", 20, 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("waited behind the indexing pass")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, "ワイン", "semantic", 20, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	if _, err := s.Search(context.Background(), "ワイン", "keyword", 20, 0); err != nil {
		t.Fatalf("keyword search must not need the gate: %v", err)
	}
}

func TestChunkingRetainsTailOfLongJapaneseText(t *testing.T) {
	text := strings.Repeat("あ", 6000) + "最後のワイン"
	parts := chunks(text)
	if len(parts) != 4 || !strings.HasSuffix(parts[len(parts)-1], "最後のワイン") {
		t.Fatal("tail missing")
	}
	for _, part := range parts {
		if len([]rune(part)) > 2000 {
			t.Fatal("oversized chunk")
		}
	}
}
