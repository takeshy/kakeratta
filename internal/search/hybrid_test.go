package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	got, err := New(w).Unified(context.Background(), "ピノ", Window{}, 20, 0, 0.5)
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
	got, err := s.Unified(context.Background(), "cafe ピノ", Window{}, 20, 0, 0.5)
	if err != nil || !got.SemanticUnavailable || got.Total != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Unified(ctx, "cafe", Window{}, 20, 0, 0.5); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Unified(context.Background(), " ", Window{}, 20, 0, 0.5); !errors.Is(err, ErrQuery) {
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
	got, err := New(hybridNativeWorkspace{w}).Unified(context.Background(), "軽いお酒", Window{}, 20, 0, 0.5)
	if err != nil || got.Total != 1 || !strings.Contains(got.Hits[0].Passage, "ピノ") {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUnifiedWindowNarrowsWordsAndStandsAlone(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, entry := range []timeline.Entry{
		{ID: "early", Kind: timeline.KindPost, Speaker: timeline.User, Text: "薬を飲んだ", At: day.Add(7 * time.Hour)},
		{ID: "late", Kind: timeline.KindPost, Speaker: timeline.User, Text: "薬を飲んだ", At: day.Add(20 * time.Hour)},
		{ID: "other", Kind: timeline.KindPost, Speaker: timeline.User, Text: "散歩", At: day.Add(8 * time.Hour)},
		{ID: "reply", Kind: timeline.KindRemark, Speaker: "routine", Root: "other", ReplyTo: "other", Text: "いいですね", At: day.Add(9 * time.Hour)},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	s := New(w)
	morning := Window{From: day.Add(5 * time.Hour), To: day.Add(12 * time.Hour)}
	got, err := s.Unified(context.Background(), "薬", morning, 20, 0, 0.5)
	if err != nil || got.Total != 1 || got.Hits[0].Entry.ID != "early" {
		t.Fatalf("%+v %v", got, err)
	}
	// The window alone lists the morning, newest first, replies included,
	// without touching the embedding provider.
	got, err = s.Unified(context.Background(), "", morning, 20, 0, 0.5)
	if err != nil || got.SemanticUnavailable || got.Total != 3 || got.Hits[0].Entry.ID != "reply" || got.Hits[2].Entry.ID != "early" || got.Hits[2].Passage != "薬を飲んだ" {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err = s.Unified(context.Background(), "", Window{From: day.Add(10 * time.Hour)}, 20, 0, 0.5); err != nil || got.Total != 1 || got.Hits[0].Entry.ID != "late" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.Unified(context.Background(), "", Window{}, 20, 0, 0.5); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	if _, err := s.Unified(context.Background(), "", Window{From: day.Add(time.Hour), To: day}, 20, 0, 0.5); !errors.Is(err, ErrWindow) {
		t.Fatal(err)
	}
}
