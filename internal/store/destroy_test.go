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

// Closing a workspace leaves nothing: not the directory, and not the captures
// where the blob store keeps them apart from it.
func TestDestroyRemovesTheWorkspaceAndItsCaptures(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	blobs := &memoryBlobs{}
	s.SetBlobStore(blobs)
	if err := s.SaveSettings(Settings{APIKey: "nebius-key", LowModel: "low"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutBlob([]byte("capture"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateEntry(timeline.Entry{ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User, Text: "かけら", At: time.Now(), Root: "post_1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("cafe", "shops/regular.md", "いつもの店"); err != nil {
		t.Fatal(err)
	}
	if len(blobs.data) != 1 {
		t.Fatalf("captures before = %d", len(blobs.data))
	}

	if err := s.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the workspace directory remains: %v", err)
	}
	if len(blobs.data) != 0 {
		t.Fatalf("captures after = %d", len(blobs.data))
	}
}

// The hosted workspace is a document with collections under it, some nested
// under documents of their own; all of it goes. Runs against the emulator only
// (`firebase emulators:start --only firestore`).
func TestFirestoreWorkspaceDestroyLeavesNothing(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore destroy test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-destroy-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	key := NewID("user")
	w, err := NewFirestoreWorkspace(ctx, client, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SaveSettings(Settings{APIKey: "nebius-key", LowModel: "low", Active: []string{"cafe"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.CreateEntry(timeline.Entry{ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User, Text: "かけら", At: time.Now(), Root: "post_1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteNote("cafe", "shops/regular.md", "いつもの店"); err != nil {
		t.Fatal(err)
	}
	if err := w.PersistSkill("cafe", []byte(`{"id":"cafe","name":"カフェ"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PutBlob([]byte("capture"), "image/png"); err != nil {
		t.Fatal(err)
	}
	root := w.Root()

	if err := w.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the materialised directory remains: %v", err)
	}
	user := client.Collection("users").Doc(key)
	if snapshot, err := user.Get(ctx); err == nil && snapshot.Exists() {
		t.Fatal("the user document remains")
	}
	collections, err := user.Collections(ctx).GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("collections remain: %d", len(collections))
	}
	notes, err := user.Collection("personas").Doc("cafe").Collection("memory").Documents(ctx).GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes remain: %d", len(notes))
	}
}
