package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFirestorePhysicalDeletionAndLateWrites(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore deletion test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-delete-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	w, err := NewFirestoreWorkspace(ctx, client, NewID("user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy(ctx)
	at := time.Now()
	parent := timeline.Entry{ID: "parent", Kind: timeline.KindPost, At: at}
	reply := timeline.Entry{ID: "reply", Kind: timeline.KindRemark, Root: parent.ID, ReplyTo: parent.ID, At: at.Add(time.Minute)}
	for _, entry := range []timeline.Entry{parent, reply} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.searchPassages().Doc("passage").Set(ctx, SearchPassage{EntryID: reply.ID, RootID: parent.ID, Text: "remove this"}); err != nil {
		t.Fatal(err)
	}
	gone, err := w.DeleteEntry(ctx, parent.ID)
	if err != nil || len(gone) != 2 {
		t.Fatalf("gone=%+v, err=%v", gone, err)
	}
	for _, id := range []string{parent.ID, reply.ID} {
		if _, err := w.timeline().Doc(id).Get(ctx); status.Code(err) != codes.NotFound {
			t.Fatalf("record %s remains: %v", id, err)
		}
	}
	if _, err := w.searchPassages().Doc("passage").Get(ctx); status.Code(err) != codes.NotFound {
		t.Fatalf("passage remains: %v", err)
	}
	if err := w.UpdateEntry(parent); !errors.Is(err, ErrEntryMissing) {
		t.Fatalf("late update=%v", err)
	}
	if err := w.Append(reply); !errors.Is(err, ErrEntryMissing) {
		t.Fatalf("late reply=%v", err)
	}
	if _, _, err := w.CreateEntry(reply); !errors.Is(err, ErrEntryMissing) {
		t.Fatalf("late create=%v", err)
	}
}
