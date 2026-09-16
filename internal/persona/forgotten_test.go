package persona

import (
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
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

	// The note that cited the post, so the app has a link of its own to clear.
	if err := s.WriteNote("gourmet", "tastes/sesame.md", "ごま強めは苦手",
		store.NoteSource{EntryID: entry.ID, Label: "担々麺の投稿"}); err != nil {
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

	// The app took its own link out before waking anyone, and says where to look.
	if note, ok := s.Note("gourmet", "tastes/sesame.md"); !ok || strings.Contains(note.Text, entry.ID) {
		t.Fatalf("the deleted post is still cited: %v", note.Text)
	} else if !strings.Contains(note.Text, "ごま強めは苦手") {
		t.Fatalf("the body was rewritten: %s", note.Text)
	}
	if len(items[0].notes) != 1 || items[0].notes[0] != "tastes/sesame.md" {
		t.Fatalf("the cited notes were not passed on: %v", items[0].notes)
	}

	r.deletedAt(items[0])
	if len(woken) != 1 {
		t.Fatalf("the persona that cited the post was not told: %v", woken)
	}
	if !strings.Contains(woken[0], "tastes/sesame.md") {
		t.Fatalf("the persona was not told which notes:\n%s", woken[0])
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

// A deletion wake is a turn like any other: the persona's declared tools are in
// its hands, so plana can cancel the schedule it set because of that post.
func TestADeletionWakeCanCancelTheSchedule(t *testing.T) {
	var offered string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "根拠にしていた投稿が消されました") {
			offered = system
		}
		return `{"action":"tool","tool":"schedule","args":{"action":"cancel","id":"s_gone"}}`
	}, "routine")

	entry := post(t, s, "17:00になったら夕飯の買い物に行く。")
	if err := s.RecordWake(entry.ID, "routine"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(store.Schedule{
		Skill: "routine", At: time.Now().Add(3 * time.Hour).UTC(), Note: "買い物に行ったか",
	})
	if err != nil {
		t.Fatal(err)
	}

	r.EntryForgotten(entry.ID, entry.Text)
	r.stopMu.Lock()
	var gone []deletedPost
	for _, item := range r.queue {
		if item.deleted != nil {
			gone = append(gone, *item.deleted)
		}
	}
	r.stopMu.Unlock()
	if len(gone) != 1 {
		t.Fatalf("queued %+v", gone)
	}
	r.deletedAt(gone[0])

	if !strings.Contains(offered, "schedule") {
		t.Fatalf("the persona was not offered its schedule tool:\n%s", offered)
	}
	if !strings.Contains(offered, "予定も見直してください") {
		t.Fatal("the deletion wake did not mention the schedules")
	}
	// The cancel it asked for went through, which is what having the tool means.
	if left, err := s.Schedules("routine"); err != nil || len(left) != 1 || left[0].ID != saved.ID {
		t.Fatalf("left = %+v (%v)", left, err)
	}
}
