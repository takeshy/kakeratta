package tools

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestEveryPersonaCanSearchItsUsersTimelineWithCitations(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(timeline.Entry{ID: "old", Kind: timeline.KindPost, Speaker: timeline.User, Text: "赤ワイン"}); err != nil {
		t.Fatal(err)
	}
	set := New(Config{}).WithSearch(search.New(w)).For(nil)
	if !strings.Contains(set.Describe(), "timeline_search") {
		t.Fatal("search not exposed to existing personas")
	}
	result, err := set.Run(context.Background(), "timeline_search", Args{"query": "赤ワイン", "mode": "keyword"}, nil)
	if err != nil || !strings.Contains(result.Text, "赤ワイン") || len(result.Sources) != 1 || result.Sources[0].URL != "/?entry=old" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := set.Run(context.Background(), "web_search", Args{"query": "wine"}, nil); err == nil {
		t.Fatal("undeclared web search permitted")
	}
}

func TestSemanticContextIncludesTailAndFiltersWeakEvidence(t *testing.T) {
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		rows := []map[string]any{}
		for i, input := range body.Input {
			v := make([]float64, search.Dimensions)
			score := 0.4
			if strings.HasPrefix(input, "Instruct:") {
				score = 1
			} else if strings.Contains(input, "ピノ") {
				score = 0.8
			}
			v[0], v[1] = score, math.Sqrt(1-score*score)
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": rows})
	}))
	defer provider.Close()
	if err := w.SaveSettings(store.Settings{BaseURL: provider.URL, APIKey: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []timeline.Entry{
		{ID: "wine", Kind: timeline.KindPost, Speaker: timeline.User, Text: strings.Repeat("食事の記録。", 100) + "気に入ったのはピノ・ノワール"},
		{ID: "other", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Goの実装"},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	s := search.New(w)
	set := New(Config{}).WithSearch(s).For(nil)
	for _, tc := range []struct {
		name string
		args Args
		want int
	}{
		{"default", Args{"query": "軽い赤のお酒", "limit": 10}, 1},
		{"lower threshold", Args{"query": "軽い赤のお酒", "min_score": 0.3, "limit": 10}, 2},
		{"no evidence", Args{"query": "軽い赤のお酒", "min_score": 0.9}, 0},
		{"keyword", Args{"query": "Go", "mode": "keyword", "min_score": 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := set.Run(context.Background(), "timeline_search", tc.args, nil)
			if err != nil || len(result.Sources) != tc.want {
				t.Fatalf("%+v %v", result, err)
			}
			if tc.name == "default" {
				if !strings.Contains(result.Text, "気に入ったのはピノ・ノワール") || strings.Contains(result.Text, "Goの実装") {
					t.Fatalf("missing tail or leaked weak evidence: %s", result.Text)
				}
				var rows []map[string]any
				if err := json.Unmarshal([]byte(result.Text), &rows); err != nil {
					t.Fatal(err)
				}
				if rows[0]["speaker"] != string(timeline.User) || rows[0]["score"].(float64) <= 0 {
					t.Fatal(rows)
				}
			}
			if tc.want == 0 && !strings.Contains(result.Text, "確認できたとは判断せず") {
				t.Fatal(result.Text)
			}
		})
	}
	for _, score := range []float64{-1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := set.Run(context.Background(), "timeline_search", Args{"query": "wine", "min_score": score}, nil); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
	// The UI still offers weaker candidates and keeps its compact excerpt.
	ui, err := s.Search(context.Background(), "軽い赤のお酒", "semantic", 10, 0)
	if err != nil || ui.Total != 2 || strings.Contains(ui.Hits[0].Snippet, "ピノ") {
		t.Fatalf("%+v %v", ui, err)
	}
}
