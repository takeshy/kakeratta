package store

import (
	"context"
	"encoding/json"
	"errors"
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
	if err := w.FinishReaction("a1"); err != nil {
		t.Fatal(err)
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
	release, err := w.Lock(ctx, "keeper:gourmet")
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	if _, err := w.Lock(waiting, "keeper:gourmet"); err == nil {
		t.Fatal("a held lock was taken")
	}
	cancel()
	release()
	again2, err := w.Lock(ctx, "keeper:gourmet")
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
