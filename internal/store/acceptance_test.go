package store

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func acceptanceContract(t *testing.T, w Workspace) {
	t.Helper()
	entry := timeline.Entry{ID: "accepted", Root: "accepted", Kind: timeline.KindPost, Speaker: timeline.User, Text: "once", At: time.Now()}
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, err := w.CreateEntry(entry, EntryReaction(entry))
			if err != nil {
				t.Error(err)
			}
			if fresh {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created %d times", created.Load())
	}
	requests, err := w.Reactions(context.Background())
	if err != nil || len(requests) != 1 || requests[0].Entry != entry.ID || !requests[0].Route {
		t.Fatalf("requests = %+v, %v", requests, err)
	}
	reply := timeline.Entry{ID: "reply", Root: entry.ID, ReplyTo: entry.ID, Kind: timeline.KindReply, Speaker: timeline.User, Text: "reply", At: time.Now()}
	if _, fresh, err := w.CreateEntry(reply, EntryReaction(reply)); err != nil || !fresh {
		t.Fatalf("reply = %v, %v", fresh, err)
	}
	if err := w.SaveRouting(entry.ID, Routing{Delivered: []string{"old"}}); err != nil {
		t.Fatal(err)
	}
	entry.Text = "edited"
	reaction := EntryReaction(entry)
	reaction.Edited = true
	if _, err := w.UpdateText(entry.ID, entry.Text, reaction); err != nil {
		t.Fatal(err)
	}
	if record, err := w.Routing(entry.ID); err != nil || record != nil {
		t.Fatalf("old routing = %+v %v", record, err)
	}
	requests, err = w.Reactions(context.Background())
	if err != nil || len(requests) != 3 {
		t.Fatalf("edit requests = %+v, %v", requests, err)
	}
}

func TestEntryAcceptanceIncludesRoutingRequest(t *testing.T) {
	w, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acceptanceContract(t, w)
	reopened, err := Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	requests, err := reopened.Reactions(context.Background())
	if err != nil || len(requests) != 3 {
		t.Fatalf("restart requests = %+v, %v", requests, err)
	}
}
func TestFirestoreEntryAcceptanceIncludesRoutingRequest(t *testing.T) {
	_, w := firestoreWorkspace(t)
	acceptanceContract(t, w)
}

func TestFailedRoutingWriteDoesNotAcceptEntry(t *testing.T) {
	w, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A regular file blocks creation of the reaction directory, without relying
	// on permissions (tests may run as root).
	blocked := filepath.Join(w.Root(), reactionsDir)
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := timeline.Entry{ID: "post", Root: "post", Kind: timeline.KindPost, Speaker: timeline.User, Text: "retry", At: time.Now()}
	if _, fresh, err := w.CreateEntry(entry, EntryReaction(entry)); err == nil || fresh {
		t.Fatalf("accepted without a request: %v, %v", fresh, err)
	}
	if _, ok := w.Entry(entry.ID); ok {
		t.Fatal("entry persisted despite queue failure")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := w.CreateEntry(entry, EntryReaction(entry)); err != nil || !fresh {
		t.Fatalf("retry = %v, %v", fresh, err)
	}
}

func indexCoalescingContract(t *testing.T, w Workspace) {
	t.Helper()
	ctx := context.Background()
	queue := func() {
		t.Helper()
		if err := w.CreateReaction(Reaction{ID: IndexReaction, Lane: IndexReaction, Index: true, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	queue()
	queue()
	head, _, ok, err := w.ClaimReaction(ctx, "one", time.Minute)
	if err != nil || !ok || head.IndexAgain {
		t.Fatalf("initial claim = %+v %v %v", head, ok, err)
	}
	queue()
	queue()
	if _, _, ok, err := w.ClaimReaction(ctx, "two", time.Minute); err != nil || ok {
		t.Fatalf("concurrent indexing = %v %v", ok, err)
	}
	if ok, err := w.RenewReaction(ctx, head.ID, "one", time.Minute); err != nil || !ok {
		t.Fatalf("renew = %v %v", ok, err)
	}
	if ok, err := w.FinishReaction(ctx, head.ID, "one"); err != nil || !ok {
		t.Fatalf("finish = %v %v", ok, err)
	}
	next, _, ok, err := w.ClaimReaction(ctx, "two", time.Minute)
	if err != nil || !ok || next.IndexAgain || next.Attempts != 1 {
		t.Fatalf("follow-up = %+v %v %v", next, ok, err)
	}
	if ok, err := w.FinishReaction(ctx, head.ID, "one"); err != nil || ok {
		t.Fatalf("stale finish = %v %v", ok, err)
	}
	if ok, err := w.FinishReaction(ctx, next.ID, "two"); err != nil || !ok {
		t.Fatalf("final finish = %v %v", ok, err)
	}
	if items, err := w.Reactions(ctx); err != nil || len(items) != 0 {
		t.Fatalf("left = %+v %v", items, err)
	}
}
func TestIndexChangesWaitForTheRunningPass(t *testing.T) {
	w, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	indexCoalescingContract(t, w)
}
func TestFirestoreIndexChangesWaitForTheRunningPass(t *testing.T) {
	_, w := firestoreWorkspace(t)
	indexCoalescingContract(t, w)
}

func TestQueueFailureLeavesEditedTextUnchanged(t *testing.T) {
	w, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := timeline.Entry{ID: "edit", Root: "edit", Kind: timeline.KindPost, Speaker: timeline.User, Text: "before", At: time.Now()}
	if err := w.Append(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Root(), reactionsDir), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	entry.Text = "after"
	request := EntryReaction(entry)
	request.Edited = true
	if _, err := w.UpdateText(entry.ID, entry.Text, request); err == nil {
		t.Fatal("accepted edit without request")
	}
	got, ok := w.Entry(entry.ID)
	if !ok || got.Text != "before" {
		t.Fatalf("failed edit changed entry: %+v", got)
	}
}
