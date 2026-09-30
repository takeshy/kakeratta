package persona

import (
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
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

	// A note that cited the post among others, so the app has a link of its
	// own to clear, and one that rested on it alone, which goes with it.
	other := post(t, s, "麻婆豆腐も花椒より胡麻が勝っていた")
	if err := s.WriteNote("gourmet", "tastes/sesame.md", "ごま強めは苦手",
		store.NoteSource{EntryID: entry.ID, Label: "担々麺の投稿"},
		store.NoteSource{EntryID: other.ID, Label: "麻婆豆腐の投稿"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "shops/tantan.md", "担々麺の店",
		store.NoteSource{EntryID: entry.ID, Label: "担々麺の投稿"}); err != nil {
		t.Fatal(err)
	}

	r.EntryForgotten(entry.ID, entry.Text)
	queued := len(wakes(t, r))
	items := deletedWakes(t, r)
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
	// The note the post alone stood behind is gone, and the persona is told.
	if _, ok := s.Note("gourmet", "shops/tantan.md"); ok {
		t.Fatal("a note resting only on the deleted post survived")
	}
	if len(items[0].removed) != 1 || items[0].removed[0] != "shops/tantan.md" {
		t.Fatalf("the removed notes were not passed on: %v", items[0].removed)
	}

	r.deletedAt(items[0])
	if len(woken) != 1 {
		t.Fatalf("the persona that cited the post was not told: %v", woken)
	}
	if !strings.Contains(woken[0], "tastes/sesame.md") || !strings.Contains(woken[0], "shops/tantan.md") {
		t.Fatalf("the persona was not told which notes:\n%s", woken[0])
	}
	if !strings.Contains(woken[0], entry.ID) || !strings.Contains(woken[0], "ごま") {
		t.Fatalf("the persona was not told which post:\n%s", woken[0])
	}
	// The record is spent, so the same deletion cannot queue another wake.
	r.EntryForgotten(entry.ID, entry.Text)
	again := len(wakes(t, r))
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
	gone := deletedWakes(t, r)
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

// Tidying up after a deletion is not an occasion to speak. The wake has no post
// to answer, so a remark would land as a new top-level post - the app starting a
// conversation with itself about something the user just removed.
func TestADeletionWakeCannotPost(t *testing.T) {
	r, s, stub := setup(t, func(system, user string) string {
		return `{"action":"speak","text":"ドラマ、見てる？"}`
	}, "gourmet")

	entry := post(t, s, "22:00からドラマを見る")
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
		t.Fatal(err)
	}
	before := len(remarks(t, s))

	r.EntryForgotten(entry.ID, entry.Text)
	gone := deletedWakes(t, r)
	if len(gone) != 1 {
		t.Fatalf("queued %+v", gone)
	}
	r.deletedAt(gone[0])

	// It asked to speak and nothing came of it, in either shape it could take:
	// a remark under something, or a post of its own.
	if len(remarks(t, s)) != before {
		t.Fatalf("a deletion wake said something: %+v", remarks(t, s))
	}
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	for _, one := range entries {
		if one.Speaker == "gourmet" {
			t.Fatalf("a deletion wake wrote to the timeline: kind=%s %q", one.Kind, one.Text)
		}
	}
	// It was told so up front, and it was never asked for an answer at all:
	// there is nothing to research or say, so the wake goes straight to the
	// schedule and the notes - and never to an action.
	if !stub.seen("このターンは発言できません") {
		t.Fatal("the wake was not told it cannot speak")
	}
	for _, call := range stub.phases {
		for _, tool := range []string{"finish_read —", "submit_answer —", "submit_action —"} {
			if strings.Contains(call, tool) {
				t.Fatalf("a deletion wake was offered %s:\n%s", strings.TrimSuffix(tool, " —"), call)
			}
		}
	}
	if len(stub.phases) != 2 {
		t.Fatalf("a deletion wake made %d calls, not the schedule and note phases", len(stub.phases))
	}
}

// A deleted post takes the conversation under it along. The persona woken for
// the post is told once, the notes resting only on the post and the user's
// lines under it are deleted, and the record of who was woken is spent.
func TestDeletingAPostForgetsTheConversationUnderIt(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		return `{"action":"silent"}`
	}, "gourmet")
	entry := post(t, s, "この前の担々麺")
	reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User, Root: entry.ID, ReplyTo: entry.ID, Text: "辛いのは平気", At: time.Now()}
	remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: entry.ID, ReplyTo: entry.ID, Text: "ごま強め", At: time.Now()}
	for _, one := range []timeline.Entry{reply, remark} {
		if err := s.Append(one); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "tastes/sesame.md", "ごま強めは苦手", store.NoteSource{EntryID: entry.ID, Label: "担々麺"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "tastes/heat.md", "辛さは平気", store.NoteSource{EntryID: reply.ID, Label: "辛いのは平気"}); err != nil {
		t.Fatal(err)
	}

	r.EntriesForgotten(entry.ID, []timeline.Entry{entry, reply, remark})
	items := deletedWakes(t, r)
	if len(items) != 1 || items[0].skill != "gourmet" || items[0].entryID != entry.ID {
		t.Fatalf("queued %+v, want one wake for the post", items)
	}
	if len(items[0].removed) != 2 || !strings.Contains(items[0].label, "担々麺") || !strings.Contains(items[0].label, "辛いのは平気") {
		t.Fatalf("the wake does not carry both lines and both notes: %+v", items[0])
	}
	// Each note rested on one of the deleted lines alone, so both went with them.
	for _, path := range []string{"tastes/sesame.md", "tastes/heat.md"} {
		if note, ok := s.Note("gourmet", path); ok {
			t.Fatalf("%s survived its only source: %v", path, note.Text)
		}
	}
	if woke, _ := s.Woke(entry.ID); len(woke) != 0 {
		t.Fatalf("the wake record was not spent: %v", woke)
	}
}

// Deleting one of the user's lines under a post reaches the personas woken for
// that post, and leaves the post's own record in place.
func TestDeletingAReplyReachesThePersonasWokenForItsPost(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		return `{"action":"silent"}`
	}, "gourmet")
	entry := post(t, s, "この前の担々麺")
	reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User, Root: entry.ID, ReplyTo: entry.ID, Text: "辛いのは平気", At: time.Now()}
	if err := s.Append(reply); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "tastes/heat.md", "辛さは平気", store.NoteSource{EntryID: reply.ID, Label: "辛いのは平気"}); err != nil {
		t.Fatal(err)
	}
	r.EntryForgotten(reply.ID, reply.Text)
	queued := len(deletedWakes(t, r))
	if queued != 1 {
		t.Fatalf("queued %d wakes, want one", queued)
	}
	if note, _ := s.Note("gourmet", "tastes/heat.md"); strings.Contains(note.Text, reply.ID) {
		t.Fatalf("the deleted reply is still cited: %s", note.Text)
	}
	if woke, _ := s.Woke(entry.ID); len(woke) != 1 {
		t.Fatalf("the post's own record went with its reply: %v", woke)
	}
}

// A persona can cite a post it found by searching, without ever being woken for
// it. Deleting the post still takes away the note that rested on it alone.
func TestDeletingAPostReachesNotesCitingItFromSearch(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string {
		return `{"action":"silent"}`
	}, "gourmet", "book")
	entry := post(t, s, "駅前の古本屋で買った")
	if err := s.WriteNote("book", "shops/used.md", "駅前の古本屋", store.NoteSource{EntryID: entry.ID, Label: "古本屋"}); err != nil {
		t.Fatal(err)
	}
	r.EntryForgotten(entry.ID, entry.Text)
	if _, ok := s.Note("book", "shops/used.md"); ok {
		t.Fatal("a note citing the deleted post from search survived")
	}
	queued := wakes(t, r)
	if len(queued) != 1 || queued[0].deleted == nil || queued[0].deleted.skill != "book" {
		t.Fatalf("the persona that cited the post was not told: %+v", queued)
	}
}
