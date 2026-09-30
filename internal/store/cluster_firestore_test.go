package store

import (
	"context"
	"os"
	"path/filepath"
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

// Reactions are kept until finished, oldest first, and one added anywhere is
// announced to the owner's feed.
func TestFirestoreReactions(t *testing.T) {
	_, w := firestoreWorkspace(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	added := make(chan struct{}, 4)
	w.WatchReactions(ctx, func() { added <- struct{}{} })
	time.Sleep(500 * time.Millisecond)
	now := time.Now()
	place := &timeline.Place{Lat: 35.6, Lon: 139.7}
	for i, item := range []Reaction{
		{ID: "r2", At: now.Add(time.Second), Forgotten: &ForgottenReaction{Skill: "gourmet", Paths: []string{"a.md"}, Expired: true}},
		{ID: "r1", At: now, Entry: "e1", Edited: true, Place: place},
	} {
		if err := w.SaveReaction(item); err != nil {
			t.Fatal(i, err)
		}
	}
	select {
	case <-added:
	case <-ctx.Done():
		t.Fatal("the feed did not announce the reaction")
	}
	items, err := w.Reactions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "r1" || items[0].Place == nil || items[0].Place.Lat != 35.6 || !items[0].Edited ||
		items[1].Forgotten == nil || !items[1].Forgotten.Expired || items[1].Forgotten.Paths[0] != "a.md" {
		t.Fatalf("items = %+v", items)
	}
	if err := w.FinishReaction("r1"); err != nil {
		t.Fatal(err)
	}
	if items, _ := w.Reactions(ctx); len(items) != 1 || items[0].ID != "r2" {
		t.Fatalf("after finishing = %+v", items)
	}
}

// A persona installed or removed through another instance appears in, or
// leaves, this one's materialised definitions.
func TestFirestoreSkillsFollowOtherInstances(t *testing.T) {
	client, w := firestoreWorkspace(t)
	other, err := NewFirestoreWorkspace(context.Background(), client, filepath.Base(w.user.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.PersistSkill("chef", []byte(`{"id":"chef"}`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.SkillDir(), "chef", "skill.json")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, err := os.ReadFile(path); err == nil && string(raw) == `{"id":"chef"}` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the installed definition did not arrive")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := other.DeleteSkill("chef"); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the removed definition stayed")
		}
		time.Sleep(100 * time.Millisecond)
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
