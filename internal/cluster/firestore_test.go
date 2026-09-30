package cluster

import (
	"context"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

// One instance holds a job at a time; another takes it once it is released
// or has run out.
func TestFirestoreLeases(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore lease test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-cluster-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	name := "job-" + NewInstanceID()
	a, b := NewFirestore(client, "a"), NewFirestore(client, "b")
	if ok, err := a.Acquire(ctx, name); err != nil || !ok {
		t.Fatalf("a acquire = %v, %v", ok, err)
	}
	if ok, _ := b.Acquire(ctx, name); ok {
		t.Fatal("b took a live lease")
	}
	if ok, _ := a.Acquire(ctx, name); !ok {
		t.Fatal("a could not renew its own lease")
	}
	if lease, _ := b.Lease(ctx, name); lease.Owner != "a" || !lease.Live(time.Now()) {
		t.Fatalf("lease = %+v", lease)
	}
	if err := a.Release(ctx, name); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Acquire(ctx, name); !ok {
		t.Fatal("b could not take a released lease")
	}
	b.now = func() time.Time { return time.Now().Add(-2 * LeaseTTL) }
	if ok, _ := b.Acquire(ctx, name); !ok {
		t.Fatal("b could not renew")
	}
	if ok, _ := a.Acquire(ctx, name); !ok {
		t.Fatal("a could not take an expired lease")
	}
}
