package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// The reaction queue, runtime records and locks behave the same whichever
// workspace keeps them; both implementations run this.
func runtimeContract(t *testing.T, w Workspace) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	place := &timeline.Place{Lat: 35.6, Lon: 139.7}
	for _, item := range []Reaction{
		{ID: "a2", At: now.Add(time.Second), Lane: "thread:a", Entry: "a-reply"},
		{ID: "a1", At: now, Lane: "thread:a", Entry: "a-post", Edited: true, Place: place},
		{ID: "b1", At: now.Add(2 * time.Second), Lane: "thread:b", Entry: "b-post"},
		{ID: "later", At: now, Lane: "index", Index: true, NotBefore: now.Add(time.Hour)},
	} {
		if err := w.SaveReaction(item); err != nil {
			t.Fatal(err)
		}
	}
	// A lane is worked in order: its head, with the rest of it behind.
	head, rest, ok, err := w.ClaimReaction(ctx, "w1", time.Minute)
	if err != nil || !ok || head.ID != "a1" || head.Attempts != 1 || head.Place == nil || head.Place.Lat != 35.6 || !head.Edited ||
		len(rest) != 1 || rest[0].ID != "a2" {
		t.Fatalf("first claim = %+v %+v %v %v", head, rest, ok, err)
	}
	// Another worker gets the next lane, never the claimed lane's second.
	next, _, ok, err := w.ClaimReaction(ctx, "w2", time.Minute)
	if err != nil || !ok || next.ID != "b1" {
		t.Fatalf("second claim = %+v %v %v", next, ok, err)
	}
	// Nothing else is free: a2 waits behind a1, and the index is held back.
	if _, _, ok, _ := w.ClaimReaction(ctx, "w3", time.Minute); ok {
		t.Fatal("claimed past a held lane or a held-back reaction")
	}
	if kept, err := w.RenewReaction(ctx, "a1", "w1", time.Minute); err != nil || !kept {
		t.Fatalf("renew = %v, %v", kept, err)
	}
	if kept, _ := w.RenewReaction(ctx, "a1", "w2", time.Minute); kept {
		t.Fatal("another worker renewed a claim")
	}
	// Released, it is taken up again at once, one attempt further.
	if err := w.ReleaseReaction(ctx, "a1", "w1"); err != nil {
		t.Fatal(err)
	}
	again, _, ok, _ := w.ClaimReaction(ctx, "w3", time.Minute)
	if !ok || again.ID != "a1" || again.Attempts != 2 {
		t.Fatalf("after release = %+v %v", again, ok)
	}
	// Only the worker whose claim it is may finish it: w1 gave it up and w3
	// has it now, so w1 finding out late deletes nothing.
	if done, err := w.FinishReaction(ctx, "a1", "w1", "a2"); err != nil || done {
		t.Fatalf("a stale worker finished it: %v %v", done, err)
	}
	if items, _ := w.Reactions(ctx); len(items) != 4 {
		t.Fatalf("a stale finish deleted reactions: %d left", len(items))
	}
	if done, err := w.FinishReaction(ctx, "a1", "w3"); err != nil || !done {
		t.Fatalf("the holder could not finish it: %v %v", done, err)
	}
	if after, _, ok, _ := w.ClaimReaction(ctx, "w1", time.Minute); !ok || after.ID != "a2" {
		t.Fatalf("after finishing the head = %+v %v", after, ok)
	}
	// A lapsed claim is free for anyone.
	if err := w.SaveReaction(Reaction{ID: "lapsed", At: now, Lane: "thread:c", ClaimedBy: "dead", ClaimUntil: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if taken, _, ok, _ := w.ClaimReaction(ctx, "w1", time.Minute); !ok || taken.ID != "lapsed" {
		t.Fatalf("lapsed claim = %+v %v", taken, ok)
	}
	// A fixed ID waits once.
	if err := w.CreateReaction(Reaction{ID: DueReaction, At: now, Lane: DueReaction, Due: true}); err != nil {
		t.Fatal(err)
	}
	if err := w.CreateReaction(Reaction{ID: DueReaction, At: now, Lane: DueReaction, Due: true}); !errors.Is(err, ErrReactionExists) {
		t.Fatalf("second tick = %v", err)
	}

	// A reaction that must not be dropped is held back by the worker whose
	// claim it is, with its attempts spent afresh.
	if err := w.SaveReaction(Reaction{ID: "held", At: now, Lane: "thread:held", ClaimedBy: "w9", ClaimUntil: now.Add(time.Minute), Attempts: 5}); err != nil {
		t.Fatal(err)
	}
	if done, err := w.DeferReaction(ctx, "held", "w8", now.Add(time.Hour)); err != nil || done {
		t.Fatalf("another worker deferred it: %v %v", done, err)
	}
	if done, err := w.DeferReaction(ctx, "held", "w9", now.Add(time.Hour)); err != nil || !done {
		t.Fatalf("the holder could not defer it: %v %v", done, err)
	}
	items, err := w.Reactions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "held" && (item.ClaimedBy != "" || item.Attempts != 0 || !item.NotBefore.After(now.Add(50*time.Minute))) {
			t.Fatalf("deferred = %+v", item)
		}
	}
	if i, _, ok := nextClaim(items, time.Now()); ok && items[i].ID == "held" {
		t.Fatal("a deferred reaction can be claimed before its time")
	}

	// Records change atomically and lapse.
	for i := 0; i < 3; i++ {
		err := w.UpdateRecord(ctx, RecordTrials, "count", time.Time{}, func(current []byte, exists bool) ([]byte, error) {
			n := 0
			if exists {
				_ = json.Unmarshal(current, &n)
			}
			return json.Marshal(n + 1)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if raw, ok, err := w.Record(ctx, RecordTrials, "count"); err != nil || !ok || string(raw) != "3" {
		t.Fatalf("record = %s %v %v", raw, ok, err)
	}
	if err := w.UpdateRecord(ctx, RecordTrials, "gone", now.Add(-time.Second), func([]byte, bool) ([]byte, error) { return []byte(`1`), nil }); err != nil {
		t.Fatal(err)
	}
	all, err := w.Records(ctx, RecordTrials)
	if err != nil || len(all) != 1 || string(all["count"]) != "3" {
		t.Fatalf("records = %v %v", all, err)
	}
	if err := w.UpdateRecord(ctx, RecordTrials, "count", time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.Record(ctx, RecordTrials, "count"); ok {
		t.Fatal("a deleted record is still there")
	}
	if err := w.UpdateRecord(ctx, "timeline", "x", time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil }); !errors.Is(err, ErrUnknownRecords) {
		t.Fatalf("unknown collection = %v", err)
	}

	// A lock is held by one at a time.
	held, release, err := w.Lock(ctx, "keeper:gourmet")
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	if _, _, err := w.Lock(waiting, "keeper:gourmet"); err == nil {
		t.Fatal("a held lock was taken")
	}
	cancel()
	if held.Err() != nil {
		t.Fatalf("a held lock's context ended: %v", held.Err())
	}
	release()
	_, again2, err := w.Lock(ctx, "keeper:gourmet")
	if err != nil {
		t.Fatal(err)
	}
	again2()
}

func TestFileRuntimeContract(t *testing.T) {
	s := open(t)
	runtimeContract(t, s)
	if err := s.SetClosing(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReaction(Reaction{ID: "x", At: time.Now(), Lane: "thread:x"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.ClaimReaction(context.Background(), "w", time.Minute); ok {
		t.Fatal("a closing workspace let work be claimed")
	}
}

// Reading the persona definitions honours the caller's context.
func TestSkillsHonourTheContext(t *testing.T) {
	s := open(t)
	if err := s.PersistSkill("chef", []byte(`{"id":"chef"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Skills(ctx); err == nil {
		t.Fatal("a cancelled context still read the definitions")
	}
	skills, err := s.Skills(context.Background())
	if err != nil || string(skills["chef"]) != `{"id":"chef"}` {
		t.Fatalf("skills = %v, %v", skills, err)
	}
}

// What one speaker said, newest first, and nobody else's: the file store and
// Firestore agree.
func speakerContract(t *testing.T, w Workspace) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	post := timeline.Entry{ID: "p1", Kind: timeline.KindPost, Speaker: timeline.User, Text: "父に送った", At: base, Root: "p1"}
	for _, entry := range []timeline.Entry{
		post,
		{ID: "r1", Kind: timeline.KindRemark, Speaker: "father", Text: "一つ目", At: base.Add(time.Minute), Root: "p1", ReplyTo: "p1"},
		{ID: "r2", Kind: timeline.KindRemark, Speaker: "mother", Text: "別の人", At: base.Add(2 * time.Minute), Root: "p1", ReplyTo: "p1"},
		{ID: "r3", Kind: timeline.KindRemark, Speaker: "father", Text: "二つ目", At: base.Add(3 * time.Minute), Root: "p1", ReplyTo: "p1"},
		{ID: "p2", Kind: timeline.KindPost, Speaker: timeline.User, Text: "次の日", At: base.Add(24 * time.Hour), Root: "p2"},
		{ID: "r4", Kind: timeline.KindRemark, Speaker: "father", Text: "消える発言", At: base.Add(25 * time.Hour), Root: "p2", ReplyTo: "p2"},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(entries []timeline.Entry) []string {
		var out []string
		for _, entry := range entries {
			out = append(out, entry.ID)
		}
		return out
	}
	got, err := w.SpeakerEntries(ctx, "father")
	if err != nil || strings.Join(ids(got), ",") != "r4,r3,r1" {
		t.Fatalf("father = %v %v", ids(got), err)
	}
	if got, _ := w.SpeakerEntries(ctx, timeline.User); strings.Join(ids(got), ",") != "p2,p1" {
		t.Fatalf("user = %v", ids(got))
	}
	if got, _ := w.SpeakerEntries(ctx, "nobody"); len(got) != 0 {
		t.Fatalf("nobody = %v", ids(got))
	}
	// A deleted conversation takes what the speaker said in it.
	if _, err := w.DeleteEntry(ctx, "p2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.SpeakerEntries(ctx, "father"); strings.Join(ids(got), ",") != "r3,r1" {
		t.Fatalf("father after deletion = %v", ids(got))
	}
}

func TestFileSpeakerEntries(t *testing.T) { speakerContract(t, open(t)) }
