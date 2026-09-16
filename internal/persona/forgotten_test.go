package persona

import (
	"strings"
	"testing"
)

// Deleting a post is the user's; tidying the memory built on it is the
// persona's. The app tells the personas it woke for that post and nobody else,
// and it works that out from its own record rather than from a note.
func TestDeletingAPostWakesThePersonasItWokeForIt(t *testing.T) {
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
	// Only the persona the app actually woke for this post is told about it.
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
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
	// The record is spent, so the same deletion cannot queue another wake.
	r.EntryForgotten(entry.ID, entry.Text)
	r.stopMu.Lock()
	again := len(r.queue)
	r.stopMu.Unlock()
	if again != 1 {
		t.Fatalf("the same deletion queued %d wakes", again)
	}
	if woke, err := s.Woke(entry.ID); err != nil || len(woke) != 0 {
		t.Fatalf("the record survived: %v (%v)", woke, err)
	}
}
