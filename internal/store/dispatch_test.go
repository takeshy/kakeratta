package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFirestoreDispatchOutboxAndClaims(t *testing.T) {
	client, w := firestoreWorkspace(t)
	ctx := context.Background()
	w.SetJobPublisher(func(context.Context, Job) error { return errors.New("broker unavailable") })
	first := Reaction{ID: "first", Lane: "thread", At: time.Now().Add(-time.Minute)}
	second := Reaction{ID: "second", Lane: "thread", At: time.Now()}
	for _, item := range []Reaction{first, second} {
		if err := w.CreateReaction(item); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id string) Reaction {
		t.Helper()
		doc, err := w.reactions().Doc(id).Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	first, second = read(first.ID), read(second.ID)
	job := func(item Reaction) Job { return Job{Workspace: w.user.ID, Reaction: item.ID, Dispatch: item.Dispatch} }
	if first.Dispatch == "" || second.Dispatch == "" {
		t.Fatal("missing transactional dispatch generation")
	}
	if _, _, err := w.ClaimJob(ctx, job(second), "worker", time.Minute); !errors.Is(err, ErrJobBusy) {
		t.Fatalf("later lane item claimed: %v", err)
	}
	if _, taken, err := w.ClaimJob(ctx, job(first), "worker", time.Minute); err != nil || !taken {
		t.Fatalf("first claim: %v %v", taken, err)
	}
	if _, _, err := w.ClaimJob(ctx, job(first), "duplicate", time.Minute); !errors.Is(err, ErrJobBusy) {
		t.Fatalf("duplicate live claim: %v", err)
	}
	if _, err := w.DeferReaction(ctx, first.ID, "worker", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if done, err := w.JobFinished(ctx, job(first)); err != nil || !done {
		t.Fatalf("successor not durable: %v %v", done, err)
	}
	if _, taken, err := w.ClaimJob(ctx, job(first), "stale", time.Minute); err != nil || taken {
		t.Fatalf("stale job claimed: %v %v", taken, err)
	}
	if _, _, err := w.ClaimJob(ctx, job(read(first.ID)), "early", time.Minute); !errors.Is(err, ErrJobBusy) {
		t.Fatalf("delayed job claimed early: %v", err)
	}
	// Simulate the repair interval passing without waiting for a minute.
	docs, err := w.user.Collection("dispatchOutbox").Documents(ctx).GetAll()
	if err != nil || len(docs) != 3 {
		t.Fatalf("outbox lost after broker failure: %d %v", len(docs), err)
	}
	for _, doc := range docs {
		var request dispatchRequest
		if err := doc.DataTo(&request); err != nil {
			t.Fatal(err)
		}
		request.ReadyAt = time.Now().Add(-time.Minute)
		if _, err := doc.Ref.Set(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := RepairDispatch(ctx, client, func(_ context.Context, delivered Job) error {
		if delivered.Workspace == w.user.ID {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("repair delivered %d jobs", count)
	}
	docs, err = w.user.Collection("dispatchOutbox").Documents(ctx).GetAll()
	if err != nil || len(docs) != 0 {
		t.Fatalf("sent outbox retained: %d %v", len(docs), err)
	}
}

func TestFirestoreDispatchPreservesAcceptanceAndIndexTransitions(t *testing.T) {
	for _, test := range []struct {
		name     string
		contract func(*testing.T, Workspace)
	}{{"acceptance", acceptanceContract}, {"index", indexCoalescingContract}} {
		t.Run(test.name, func(t *testing.T) {
			_, w := firestoreWorkspace(t)
			w.SetJobPublisher(func(context.Context, Job) error { return errors.New("broker offline") })
			test.contract(t, w)
			docs, err := w.user.Collection("dispatchOutbox").Documents(context.Background()).GetAll()
			if err != nil || len(docs) == 0 {
				t.Fatalf("transitions lost dispatch requests: %d %v", len(docs), err)
			}
		})
	}
}
