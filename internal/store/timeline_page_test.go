package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestTimelinePagesKeepWholeThreadsAndStableCursors(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 46; i++ {
		id := fmt.Sprintf("post-%02d", i)
		if err := s.Append(timeline.Entry{ID: id, Kind: timeline.KindPost, Root: id, At: at, Deleted: i == 45}); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(timeline.Entry{ID: "reply-" + id, Kind: timeline.KindReply, Root: id, At: at.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.TimelinePage(context.Background(), 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 40 || first.Next == "" {
		t.Fatalf("first page: %+v", first)
	}
	seen := map[string]bool{}
	page := first
	for n := 0; n < 3; n++ {
		roots := map[string]bool{}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatalf("duplicate %s", entry.ID)
			}
			seen[entry.ID] = true
			if entry.Kind == timeline.KindPost {
				roots[entry.ID] = true
			}
		}
		expected := 20
		if n == 2 {
			expected = 5
		}
		if len(roots) != expected {
			t.Fatalf("page %d has %d posts", n, len(roots))
		}
		for _, entry := range page.Entries {
			if !roots[entry.Root] {
				t.Fatalf("orphan %s", entry.ID)
			}
		}
		if n == 0 {
			// New posts and deletion of the cursor's post cannot shift subsequent pages.
			cursor, _ := decodeCursor(page.Next)
			_ = s.Append(timeline.Entry{ID: "new", Kind: timeline.KindPost, Root: "new", At: at.Add(2 * time.Hour)})
			_ = s.Append(timeline.Entry{ID: cursor.ID, Kind: timeline.KindPost, Root: cursor.ID, At: cursor.At, Deleted: true})
		}
		if n == 2 {
			if page.Next != "" {
				t.Fatal("unexpected next page")
			}
		} else {
			page, err = s.TimelinePage(context.Background(), 20, page.Next)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if seen["post-45"] || seen["reply-post-45"] || seen["new"] {
		t.Fatal("deleted or newer post leaked into pagination")
	}
}

func TestTimelinePageRejectsInvalidCursor(t *testing.T) {
	_, err := open(t).TimelinePage(context.Background(), 20, "not-a-cursor")
	if err != ErrTimelineCursor {
		t.Fatalf("error = %v", err)
	}
}
