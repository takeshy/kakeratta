package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestIndexBeforeSearchIncludesAnalysisAndReusesSavedVectors(t *testing.T) {
	w := workspace(t)
	var inputs []string
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		var rows []any
		for i, text := range body.Input {
			inputs = append(inputs, text)
			v := make([]float64, Dimensions)
			v[0] = 1
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	w.SaveSettings(store.Settings{APIKey: "test", BaseURL: provider.URL})
	e := timeline.Entry{ID: "p", Root: "p", Kind: timeline.KindPost, Text: "夕食"}
	save(t, w, e)
	s := New(w)
	if err := s.IndexPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0] != "夕食" {
		t.Fatal(inputs)
	}
	e.Media = &timeline.Media{Text: "本日のおすすめ", Describe: "サザエのつぼ焼き", Origin: "お店のメニュー"}
	save(t, w, e)
	if err := s.IndexPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || !strings.Contains(inputs[1], "サザエのつぼ焼き") || !strings.Contains(inputs[1], "本日のおすすめ") {
		t.Fatal(inputs)
	}
	// A restart reads the persistent vectors instead of embedding documents again.
	s = New(w)
	if err := s.IndexPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 {
		t.Fatal("document embedded again")
	}
	if _, err := s.Search(context.Background(), "貝料理", "semantic", 20, 0); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || !strings.HasPrefix(inputs[2], "Instruct:") {
		t.Fatal(inputs)
	}
}
func TestNativeIndexDoesNotNeedAQueryOrVectorLookup(t *testing.T) {
	local := workspace(t)
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		v := make([]float64, Dimensions)
		v[0] = 1
		json.NewEncoder(rw).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer provider.Close()
	local.SaveSettings(store.Settings{APIKey: "test", BaseURL: provider.URL})
	entry := timeline.Entry{ID: "p", Kind: timeline.KindPost, Media: &timeline.Media{Describe: "サザエ"}}
	w := &nativeWorkspace{Store: local, pending: []store.SearchIndexEntry{{Entry: entry}}, entries: map[string]timeline.Entry{"p": entry}}
	if err := New(w).IndexPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.nearestCalls != 0 || w.replacements != 1 || len(w.passages) != 1 || w.passages[0].Text != "サザエ" {
		t.Fatalf("%+v", w)
	}
}
func TestKanaVariantsFindImageAnalysisWithoutSemanticProvider(t *testing.T) {
	w := workspace(t)
	save(t, w, timeline.Entry{ID: "p", Kind: timeline.KindPost, Media: &timeline.Media{Describe: "サザエのつぼ焼き"}})
	s := New(w)
	for _, mode := range []string{"keyword", "hybrid"} {
		for _, query := range []string{"サザエ", "さざえ", "ｻｻﾞｴ"} {
			got, err := s.Search(context.Background(), query, mode, 20, 0)
			if err != nil || got.Total != 1 || !strings.Contains(got.Hits[0].Snippet, "サザエ") {
				t.Fatalf("%s %s: %+v %v", mode, query, got, err)
			}
		}
	}
	// Keep script boundaries for mixed Japanese sentences after kana folding.
	if got := lexicalHits([]timeline.Entry{{ID: "wine", Text: "ピノ・ノワール"}}, "ピノが好き"); len(got) != 1 {
		t.Fatal(got)
	}
}
