package store

import (
	"context"
	"testing"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
)

func TestWorkspaceIDSurvivesReopening(t *testing.T) {
	s := open(t)
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != reopened.ID() {
		t.Fatal("file workspace ID changed on reopening")
	}

	// Constructing these workspaces needs no Firestore RPC or emulator.
	client, err := firestore.NewClient(context.Background(), "workspace-id-test", option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	newWorkspace := func(key string) *FirestoreWorkspace {
		t.Helper()
		w, err := NewFirestoreWorkspace(context.Background(), client, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	}
	first, second, other := newWorkspace("alice"), newWorkspace("alice"), newWorkspace("bob")
	if first.Root() == second.Root() {
		t.Fatal("test needs independent scratch directories")
	}
	if first.ID() != "alice" || second.ID() != first.ID() || other.ID() == first.ID() {
		t.Fatalf("unstable or shared workspace IDs: %q, %q, %q", first.ID(), second.ID(), other.ID())
	}
}
