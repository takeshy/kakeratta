package persona

import (
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

// Deleting a post is the user's, tidying the memory built on it is the
// persona's, and only the personas that cited it have anything to tidy.
func TestDeletingAPostWakesOnlyThePersonasThatCitedIt(t *testing.T) {
	var woken []string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		if strings.Contains(system, "根拠にしていた投稿が消されました") {
			woken = append(woken, user)
		}
		return `{"action":"silent"}`
	}, "gourmet", "book")

	entry := post(t, s, "この前の担々麺、ごまが強すぎた")
	if err := s.WriteNote("gourmet", "tastes/sesame.md", "ごま強めは苦手",
		store.NoteSource{EntryID: entry.ID, Label: "担々麺の投稿"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("book", "books/other.md", "関係のないメモ"); err != nil {
		t.Fatal(err)
	}

	r.EntryForgotten(entry.ID, entry.Text)
	r.stopMu.Lock()
	queued := len(r.queue)
	var items []deletedPost
	for _, item := range r.queue {
		if item.deleted != nil {
			items = append(items, *item.deleted)
		}
	}
	r.stopMu.Unlock()
	if queued != 1 || len(items) != 1 || items[0].skill != "gourmet" {
		t.Fatalf("queued %d wakes: %+v", queued, items)
	}

	r.deletedAt(items[0])
	if len(woken) != 1 {
		t.Fatalf("the persona that cited the post was not told: %v", woken)
	}
	if !strings.Contains(woken[0], entry.ID) || !strings.Contains(woken[0], "ごま") {
		t.Fatalf("the persona was not told which post:\n%s", woken[0])
	}
	// Asking again for the same post does not queue it twice.
	r.EntryForgotten(entry.ID, entry.Text)
	r.EntryForgotten(entry.ID, entry.Text)
	r.stopMu.Lock()
	again := len(r.queue)
	r.stopMu.Unlock()
	if again != 1 {
		t.Fatalf("the same deletion queued %d wakes", again)
	}
}
