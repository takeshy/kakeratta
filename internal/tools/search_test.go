package tools

import (
	"context"
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
	set := New(w.Settings()).WithSearch(search.New(w)).For(nil)
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
