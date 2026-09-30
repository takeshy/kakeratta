package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func firestoreWorkspace(t *testing.T) (*firestore.Client, *FirestoreWorkspace) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore workspace tests")
	}
	client, err := firestore.NewClient(context.Background(), "kakeratta-store-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	blobs := &memoryBlobs{}
	w, err := NewFirestoreWorkspace(context.Background(), client, NewID("user"), blobs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Destroy(context.Background()); _ = w.Close() })
	return client, w
}

// A window reads the entries within it, with the posts its replies hang off.
func TestFirestoreTimelineWindow(t *testing.T) {
	_, w := firestoreWorkspace(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := timeline.Entry{ID: "old", Kind: timeline.KindPost, Speaker: timeline.User, Text: "old", At: now.Add(-48 * time.Hour)}
	old.Root = old.ID
	reply := timeline.Entry{ID: "reply", Kind: timeline.KindReply, Speaker: timeline.User, Text: "reply", At: now, Root: old.ID, ReplyTo: old.ID}
	other := timeline.Entry{ID: "other", Kind: timeline.KindPost, Speaker: timeline.User, Text: "earlier", At: now.Add(-72 * time.Hour)}
	other.Root = other.ID
	for _, entry := range []timeline.Entry{old, other, reply} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := w.TimelineWindow(context.Background(), now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "old" || got[1].ID != "reply" {
		t.Fatalf("window = %+v", got)
	}
}

// The reaction queue, runtime records and locks behave the same on Firestore
// as on files, and a reaction queued anywhere is announced to every watcher.
func TestFirestoreRuntimeContract(t *testing.T) {
	client, w := firestoreWorkspace(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	queued := make(chan string, 16)
	WatchReactions(ctx, client, func(key string) {
		if key == w.user.ID {
			queued <- key
		}
	})
	time.Sleep(500 * time.Millisecond)
	runtimeContract(t, w)
	select {
	case <-queued:
	case <-ctx.Done():
		t.Fatal("the change feed did not announce a queued reaction")
	}
	if claimable, err := WorkspaceClaimable(ctx, client, w.user.ID); err != nil || !claimable {
		t.Fatalf("claimable = %v, %v", claimable, err)
	}
	pending, err := ClaimableWorkspaces(ctx, client)
	if err != nil || !pending[w.user.ID] {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	if err := w.SetClosing(ctx, true); err != nil {
		t.Fatal(err)
	}
	if closing, _ := WorkspaceClosing(ctx, client, w.user.ID); !closing {
		t.Fatal("the closing mark is not seen")
	}
	if claimable, _ := WorkspaceClaimable(ctx, client, w.user.ID); claimable {
		t.Fatal("a closing workspace offered work")
	}
	if _, _, ok, _ := w.ClaimReaction(ctx, "w1", time.Minute); ok {
		t.Fatal("a closing workspace let work be claimed")
	}
	if err := w.SetClosing(ctx, false); err != nil {
		t.Fatal(err)
	}
	if exists, err := WorkspaceExists(ctx, client, w.user.ID); err != nil || exists {
		t.Fatalf("exists before any settings = %v, %v", exists, err)
	}
	if err := w.SaveSettings(w.ConfiguredSettings()); err != nil {
		t.Fatal(err)
	}
	if exists, _ := WorkspaceExists(ctx, client, w.user.ID); !exists {
		t.Fatal("a configured workspace is not found")
	}
}

// Events published through one workspace object reach a subscriber on another,
// as they would another process.
func TestFirestoreEvents(t *testing.T) {
	client, w := firestoreWorkspace(t)
	other, err := NewFirestoreWorkspace(context.Background(), client, w.user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got := make(chan string, 4)
	other.Subscribe(ctx, func(name string, data []byte) { got <- name + ":" + string(data[:min(len(data), 8)]) })
	time.Sleep(500 * time.Millisecond)
	if err := w.Publish("entry", []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Publish("big", []byte(strings.Repeat("y", 2*inlineEventBytes))); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`entry:{"id":1}`, "big:yyyyyyyy"} {
		select {
		case event := <-got:
			if event != want {
				t.Fatalf("event = %q, want %q", event, want)
			}
		case <-ctx.Done():
			t.Fatalf("no %s event", want)
		}
	}
}

// A persona installed or removed through another process is read as it now
// stands: nothing is kept that could go stale.
func TestFirestoreSkillsAreReadAsTheyStand(t *testing.T) {
	client, w := firestoreWorkspace(t)
	other, err := NewFirestoreWorkspace(context.Background(), client, w.user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.PersistSkill("chef", []byte(`{"id":"chef"}`)); err != nil {
		t.Fatal(err)
	}
	skills, err := w.Skills(context.Background())
	if err != nil || string(skills["chef"]) != `{"id":"chef"}` {
		t.Fatalf("skills = %v, %v", skills, err)
	}
	if err := other.DeleteSkill("chef"); err != nil {
		t.Fatal(err)
	}
	if skills, _ := w.Skills(context.Background()); len(skills) != 0 {
		t.Fatalf("a removed definition is still read: %v", skills)
	}
}

// Deleting a post removes its capture from the blob store.
func TestFirestoreDeletionRemovesCaptures(t *testing.T) {
	_, w := firestoreWorkspace(t)
	blob, err := w.PutBlob([]byte("png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	entry := timeline.Entry{ID: "pic", Kind: timeline.KindPost, Speaker: timeline.User, At: time.Now(), Media: &timeline.Media{BlobID: blob, MIME: "image/png"}}
	entry.Root = entry.ID
	if err := w.Append(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DeleteEntry(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.Blob(blob); err == nil {
		t.Fatal("the capture outlived its post")
	}
}

// A lock that another process now holds ends the holder's context, and the
// holder releasing it later does not delete the new holder's lock.
func TestFirestoreLockLostEndsTheHoldersContext(t *testing.T) {
	_, w := firestoreWorkspace(t)
	ctx := context.Background()
	defer func(ttl time.Duration) { LockTTL = ttl }(LockTTL)
	LockTTL = 600 * time.Millisecond
	held, release, err := w.Lock(ctx, "keeper:x")
	if err != nil {
		t.Fatal(err)
	}
	ref := w.user.Collection("locks").Doc(sanitise("keeper:x"))
	if _, err := ref.Set(ctx, lockDoc{Owner: "someone-else", Until: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context of a lost lock did not end")
	}
	release()
	doc, err := ref.Get(ctx)
	if err != nil {
		t.Fatalf("the new holder's lock was deleted: %v", err)
	}
	if owner, _ := doc.Data()["owner"].(string); owner != "someone-else" {
		t.Fatalf("owner = %q", owner)
	}
}

// A lock whose renewals keep failing ends its holder's context before the
// lease runs out, without waiting for a renewal to return.
func TestFirestoreLockEndsWhenRenewalsFail(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore workspace tests")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-store-test")
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewFirestoreWorkspace(ctx, client, NewID("user"), &memoryBlobs{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer func(ttl time.Duration) { LockTTL = ttl }(LockTTL)
	LockTTL = 600 * time.Millisecond
	held, release, err := w.Lock(ctx, "keeper:y")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_ = client.Close() // Every renewal fails from here on.
	select {
	case <-held.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the context of a lock that cannot be renewed did not end")
	}
	if took := time.Since(started); took > LockTTL {
		t.Fatalf("it ended after %s, past the lease of %s", took, LockTTL)
	}
	release()
}
