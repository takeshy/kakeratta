package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestTimelineAPIDefaultsToTwentyWholeThreads(t *testing.T) {
	server, workspace := app(t, "")
	for i := 0; i < 21; i++ {
		id := fmt.Sprintf("post-%02d", i)
		at := time.Date(2026, 9, 13, 0, i, 0, 0, time.UTC)
		if err := workspace.Append(timeline.Entry{ID: id, Kind: timeline.KindPost, Root: id, At: at}); err != nil {
			t.Fatal(err)
		}
		if err := workspace.Append(timeline.Entry{ID: "reply-" + id, Kind: timeline.KindReply, Root: id, At: at.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) store.TimelinePage {
		t.Helper()
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var page store.TimelinePage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := get("/api/timeline")
	if len(first.Entries) != 40 || first.Next == "" {
		t.Fatalf("first: %+v", first)
	}
	second := get("/api/timeline?before=" + url.QueryEscape(first.Next))
	if len(second.Entries) != 2 || second.Next != "" || second.Entries[0].ID != "post-00" {
		t.Fatalf("second: %+v", second)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/api/timeline?before=invalid", nil))
	if w.Code != 400 {
		t.Fatalf("invalid cursor status %d", w.Code)
	}
}
