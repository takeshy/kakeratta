package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// writeRecord keeps a record of entry for persona under id, the way the keeper
// does.
func writeRecord(t *testing.T, s *store.Store, persona string, entry timeline.Entry, id string) {
	t.Helper()
	path := recordsPath(entry.At.In(time.UTC))
	var records []record
	if note, ok := s.Note(persona, path); ok {
		records = parseRecords(note.Text, time.UTC)
	}
	records = append(records, record{At: entry.At, ID: id, Summary: entry.Text, Tags: []string{"テスト"}})
	if err := s.WriteNote(persona, path, recordsText(entry.At.In(time.UTC).Format("2006-01"), records, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

// recordIDs are the ids of a persona's records, newest first.
func recordIDs(r *Runner, persona string) []string {
	found, _ := r.findRecords(persona, "", "", "", 100, time.UTC)
	var ids []string
	for _, rec := range found {
		ids = append(ids, rec.ID)
	}
	return ids
}

// Deleting a post is the user's; tidying the memory built on it is the
// persona's. The app takes the post's records out itself - kept under the
// kakera, or under the remark that answered it - and wakes only the personas
// that had one, so their fields can lose what rested on it.
func TestDeletingAPostDropsItsRecordsAndWakesThePersonasThatKeptThem(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		return `{"action":"silent"}`
	}, "gourmet", "book")

	entry := post(t, s, "この前の担々麺、ごまが強すぎた")
	remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: entry.ID, ReplyTo: entry.ID, Text: "ごま強めだったね", At: entry.At}
	if err := s.Append(remark); err != nil {
		t.Fatal(err)
	}
	other := post(t, s, "麻婆豆腐も花椒より胡麻が勝っていた")
	for _, persona := range []string{"gourmet", "book"} {
		if err := s.RecordWake(entry.ID, persona); err != nil {
			t.Fatal(err)
		}
	}
	// gourmet replied, so its record leads to the remark; book kept nothing.
	writeRecord(t, s, "gourmet", entry, remark.ID)
	writeRecord(t, s, "gourmet", other, other.ID)
	if err := s.WriteNote("gourmet", "Preference.md", "ごま強めは苦手。担々麺で強すぎた。\n"); err != nil {
		t.Fatal(err)
	}

	r.EntriesForgotten(entry.ID, []timeline.Entry{entry, remark})
	items := deletedWakes(t, r)
	if len(items) != 1 || items[0].skill != "gourmet" || items[0].entryID != entry.ID {
		t.Fatalf("queued %+v, want one wake for gourmet", items)
	}
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 || ids[0] != other.ID {
		t.Fatalf("records left = %v, want only the other post's", ids)
	}
	if woke, _ := s.Woke(entry.ID); len(woke) != 0 {
		t.Fatalf("the wake record was not spent: %v", woke)
	}

	f.keep = func(system, user string) string {
		if !strings.Contains(system, "ユーザーが投稿を削除した") || !strings.Contains(user, "ごまが強すぎた") || !strings.Contains(user, "担々麺で強すぎた") {
			t.Errorf("the keeper was not told what was deleted and what it keeps:\n%s\n%s", system, user)
		}
		return `{"fields":[{"field":"Preference","text":"ごま強めは苦手。"}]}`
	}
	r.deletedAt(context.Background(), items[0])
	if note, _ := s.Note("gourmet", "Preference.md"); strings.Contains(note.Text, "担々麺") {
		t.Fatalf("the field still rests on the deleted post: %s", note.Text)
	}
	// A tidy-up is not a kakera: no record is made for it.
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 {
		t.Fatalf("records after the tidy-up = %v", ids)
	}
}

// Deleting one of the user's lines under a post takes its record and reaches
// the personas woken for that post, leaving the post's own record in place.
func TestDeletingAReplyDropsOnlyItsRecord(t *testing.T) {
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
	writeRecord(t, s, "gourmet", entry, entry.ID)
	writeRecord(t, s, "gourmet", reply, reply.ID)
	r.EntryForgotten(reply.ID, reply.Text)
	if queued := len(deletedWakes(t, r)); queued != 1 {
		t.Fatalf("queued %d wakes, want one", queued)
	}
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 || ids[0] != entry.ID {
		t.Fatalf("records left = %v", ids)
	}
	if woke, _ := s.Woke(entry.ID); len(woke) != 1 {
		t.Fatalf("the post's own record went with its reply: %v", woke)
	}
}

// A persona that kept no record of the post has nothing to tidy and is not
// woken.
func TestDeletingAPostWithoutRecordsWakesNobody(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	entry := post(t, s, "雨")
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
		t.Fatal(err)
	}
	r.EntryForgotten(entry.ID, entry.Text)
	if queued := deletedWakes(t, r); len(queued) != 0 {
		t.Fatalf("queued %+v", queued)
	}
}

// A deletion wake is a turn like any other: the persona's declared tools are in
// its hands, so plana can cancel the schedule it set because of that post.
func TestADeletionWakeCanCancelTheSchedule(t *testing.T) {
	var offered string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "かけらが消されました") {
			offered = system
		}
		return `{"action":"tool","tool":"schedule","args":{"action":"cancel","id":"s_gone"}}`
	}, "routine")

	entry := post(t, s, "17:00になったら夕飯の買い物に行く。")
	if err := s.RecordWake(entry.ID, "routine"); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, s, "routine", entry, entry.ID)
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
	r.deletedAt(context.Background(), gone[0])

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
	writeRecord(t, s, "gourmet", entry, entry.ID)
	before := len(remarks(t, s))

	r.EntryForgotten(entry.ID, entry.Text)
	gone := deletedWakes(t, r)
	if len(gone) != 1 {
		t.Fatalf("queued %+v", gone)
	}
	r.deletedAt(context.Background(), gone[0])

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
		t.Fatalf("a deletion wake made %d calls, not the schedule phase and the keeper", len(stub.phases))
	}
}

// A reply of the persona's deleted on its own leaves the kakera it answered:
// the record is kept under the kakera, and nobody is woken.
func TestDeletingOnlyAReplyKeepsItsRecordUnderTheKakera(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	entry := post(t, s, "担々麺")
	remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: entry.ID, ReplyTo: entry.ID, Text: "いいね", At: entry.At.Add(time.Minute)}
	if err := s.Append(remark); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWake(entry.ID, "gourmet"); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, s, "gourmet", entry, remark.ID)
	r.EntriesForgotten(entry.ID, []timeline.Entry{remark})
	if queued := deletedWakes(t, r); len(queued) != 0 {
		t.Fatalf("queued %+v", queued)
	}
	if ids := recordIDs(r, "gourmet"); len(ids) != 1 || ids[0] != entry.ID {
		t.Fatalf("records = %v, want the kakera's", ids)
	}
}
