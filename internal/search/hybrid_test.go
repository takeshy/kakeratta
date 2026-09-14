package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestUnifiedJapaneseNaturalQueryAndOfflineFallback(t *testing.T) {
	w := workspace(t)
	save(t, w,
		timeline.Entry{ID: "pinot", Kind: timeline.KindPost, Text: "ピノ・ノワール。渋みが少ない。"},
		timeline.Entry{ID: "red", Kind: timeline.KindPost, Text: "赤い果実の香り"},
		timeline.Entry{ID: "both", Kind: timeline.KindPost, Text: "ピノの軽い赤を飲んだ"},
		timeline.Entry{ID: "unrelated", Kind: timeline.KindPost, Text: "サーバーの実装"},
		timeline.Entry{ID: "deleted", Kind: timeline.KindPost, Text: "ピノの軽い赤", Deleted: true},
		timeline.Entry{ID: "hidden", Root: "deleted", Kind: timeline.KindReply, Text: "ピノの軽い赤"})
	s := New(w)
	got, err := s.Search(context.Background(), "前に飲んだピノの軽い赤", "", 1, 0)
	if err != nil || !got.SemanticUnavailable || got.Total != 3 || len(got.Hits) != 1 || got.Hits[0].Entry.ID != "both" {
		t.Fatalf("%+v %v", got, err)
	}
	page, err := s.Search(context.Background(), "前に飲んだピノの軽い赤", "", 1, 1)
	if err != nil || page.Total != got.Total || page.Hits[0].Entry.ID == "both" {
		t.Fatalf("%+v %v", page, err)
	}
	for _, id := range []string{"deleted", "hidden", "unrelated"} {
		all, err := s.Search(context.Background(), "ピノ", "", 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range all.Hits {
			if h.Entry.ID == id {
				t.Fatal("unrelated/deleted result", id)
			}
		}
	}
}

func TestUnifiedFusionPreservesLexicalMatchesBelowSemanticFloor(t *testing.T) {
	w := workspace(t)
	save(t, w,
		timeline.Entry{ID: "exact", Kind: timeline.KindPost, Text: "ピノの銘柄"},
		timeline.Entry{ID: "paraphrase", Kind: timeline.KindPost, Text: "渋みが少なく飲みやすい"},
		timeline.Entry{ID: "both", Kind: timeline.KindPost, Text: "ピノ。飲みやすい"},
		timeline.Entry{ID: "weak", Kind: timeline.KindPost, Text: "サーバー"})
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		rows := []map[string]any{}
		for i, input := range body.Input {
			v := make([]float64, Dimensions)
			if strings.HasPrefix(input, "Instruct:") || strings.Contains(input, "飲みやすい") {
				v[0] = 1
			} else {
				v[1] = 1
			}
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	got, err := New(w).Unified(context.Background(), "ピノ", 20, 0, 0.5)
	if err != nil || got.SemanticUnavailable || got.Total != 3 || got.Hits[0].Entry.ID != "both" {
		t.Fatalf("%+v %v", got, err)
	}
	ids := map[string]bool{}
	for _, h := range got.Hits {
		if ids[h.Entry.ID] {
			t.Fatal("duplicate", h.Entry.ID)
		}
		ids[h.Entry.ID] = true
	}
	if !ids["exact"] || !ids["paraphrase"] || ids["weak"] {
		t.Fatal(ids)
	}
}

func TestUnifiedProviderFailureAndCancellation(t *testing.T) {
	w := workspace(t)
	save(t, w, timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: "Ｃａｆｅでピノ"})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	s := New(w)
	got, err := s.Unified(context.Background(), "cafe ピノ", 20, 0, 0.5)
	if err != nil || !got.SemanticUnavailable || got.Total != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Unified(ctx, "cafe", 20, 0, 0.5); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Unified(context.Background(), " ", 20, 0, 0.5); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
}

func TestLexicalContextAndRareTerms(t *testing.T) {
	entries := []timeline.Entry{
		{ID: "rare", Text: strings.Repeat("日記。", 800) + "ピノ・ノワール"},
		{ID: "common1", Text: "赤い飲み物"},
		{ID: "common2", Text: "赤い飲み物"},
	}
	hits := lexicalHits(entries, "ピノ 赤")
	if len(hits) != 3 || hits[0].Entry.ID != "rare" || !strings.Contains(hits[0].Passage, "ピノ") || !strings.Contains(hits[0].Snippet, "ピノ") {
		t.Fatal(hits)
	}
	if len(queryTerms("の は the a")) != 0 {
		t.Fatal("stop words retained")
	}
	if hits := lexicalHits([]timeline.Entry{{ID: "sushi", Text: "すしがおいしい"}}, "すし"); len(hits) != 1 {
		t.Fatal("short hiragana name lost")
	}
}

type hybridNativeWorkspace struct{ *nativeWorkspace }

func (w hybridNativeWorkspace) Timeline() ([]timeline.Entry, error) { return w.Store.Timeline() }

func TestUnifiedNativeBranchRevalidatesAndKeepsTail(t *testing.T) {
	local := workspace(t)
	post := timeline.Entry{ID: "p", Kind: timeline.KindPost, Text: strings.Repeat("日記。", 500) + "ピノ"}
	save(t, local, post)
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		v := make([]float64, Dimensions)
		v[0] = 1
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer provider.Close()
	if err := local.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	w := &nativeWorkspace{Store: local, entries: map[string]timeline.Entry{"p": post}, passages: []store.SearchPassage{{EntryID: "p", Hash: contentHash(Text(post)), Text: post.Text, Distance: 0.2}}}
	got, err := New(hybridNativeWorkspace{w}).Unified(context.Background(), "軽いお酒", 20, 0, 0.5)
	if err != nil || got.Total != 1 || !strings.Contains(got.Hits[0].Passage, "ピノ") {
		t.Fatalf("%+v %v", got, err)
	}
}
