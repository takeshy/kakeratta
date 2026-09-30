package cluster

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func emulator(t *testing.T) *firestore.Client {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore coordination tests")
	}
	client, err := firestore.NewClient(context.Background(), "kakeratta-cluster-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// One instance holds a workspace at a time; the other takes it only once the
// lease is released or has run out, and nobody takes one being deleted.
func TestFirestoreLeases(t *testing.T) {
	client := emulator(t)
	ctx := context.Background()
	key := "lease-" + NewInstanceID()
	a, b := NewFirestore(client, "a"), NewFirestore(client, "b")
	if ok, err := a.Acquire(ctx, key); err != nil || !ok {
		t.Fatalf("a acquire = %v, %v", ok, err)
	}
	if ok, _ := b.Acquire(ctx, key); ok {
		t.Fatal("b took a live lease")
	}
	if ok, _ := a.Acquire(ctx, key); !ok {
		t.Fatal("a could not renew its own lease")
	}
	if lease, _ := b.Lease(ctx, key); lease.Owner != "a" || !lease.Live(time.Now()) {
		t.Fatalf("lease = %+v", lease)
	}
	if err := a.Release(ctx, key); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Acquire(ctx, key); !ok {
		t.Fatal("b could not take a released lease")
	}
	// A lease that ran out is free, as when its owner died.
	b.now = func() time.Time { return time.Now().Add(-2 * LeaseTTL) }
	if ok, _ := b.Acquire(ctx, key); !ok {
		t.Fatal("b could not renew")
	}
	if ok, _ := a.Acquire(ctx, key); !ok {
		t.Fatal("a could not take an expired lease")
	}
	if err := b.MarkClosing(ctx, key); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.Acquire(ctx, key); ok {
		t.Fatal("a kept a closing workspace")
	}
	if err := b.ClearClosing(ctx, key); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Acquire(ctx, key); !ok {
		t.Fatal("a cleared workspace could not be taken")
	}
}

// A call reaches the owner and its reply comes back, large bodies included.
func TestFirestoreForwarding(t *testing.T) {
	client := emulator(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	key := "forward-" + NewInstanceID()
	owner, caller := NewFirestore(client, "owner"), NewFirestore(client, "caller")
	if ok, err := owner.Acquire(ctx, key); err != nil || !ok {
		t.Fatal(ok, err)
	}
	big := bytes.Repeat([]byte("x"), 3*inlineBytes/2)
	owner.Serve(ctx, key, func(_ context.Context, call Call) Reply {
		header := http.Header{"Content-Type": {"application/json"}, "X-Seen-Host": {call.Host}}
		return Reply{Status: http.StatusTeapot, Header: header, Body: append(append([]byte(call.Method+" "+call.URL+" "), call.Body...), big...)}
	})
	reply, err := caller.Forward(ctx, key, "owner", Call{Method: "POST", URL: "/api/x?y=1", Host: "example.test", Header: http.Header{"Origin": {"https://example.test"}}, Body: big})
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte("POST /api/x?y=1 "), big...), big...)
	if reply.Status != http.StatusTeapot || reply.Header.Get("X-Seen-Host") != "example.test" || !bytes.Equal(reply.Body, want) {
		t.Fatalf("reply = %d %v %d bytes", reply.Status, reply.Header, len(reply.Body))
	}
	calls, err := client.Collection("users").Doc(key).Collection("calls").Documents(ctx).GetAll()
	if err != nil || len(calls) != 0 {
		t.Fatalf("calls left behind: %d, %v", len(calls), err)
	}
}

// A caller waiting on an owner that stopped holding the workspace is told so,
// instead of waiting out its whole deadline.
func TestFirestoreForwardingToAGoneOwner(t *testing.T) {
	client := emulator(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	key := "gone-" + NewInstanceID()
	owner, caller := NewFirestore(client, "owner"), NewFirestore(client, "caller")
	if ok, err := owner.Acquire(ctx, key); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// The owner never answers, then lets the workspace go.
	time.AfterFunc(time.Second, func() { _ = owner.Release(context.Background(), key) })
	_, err := caller.Forward(ctx, key, "owner", Call{Method: "GET", URL: "/api/x"})
	if !errors.Is(err, ErrOwnerGone) {
		t.Fatalf("err = %v", err)
	}
}

// An event reaches every instance subscribed to the workspace.
func TestFirestoreEvents(t *testing.T) {
	client := emulator(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	key := "events-" + NewInstanceID()
	a, b := NewFirestore(client, "a"), NewFirestore(client, "b")
	var mu sync.Mutex
	got := map[string][]string{}
	done := make(chan struct{}, 4)
	for name, instance := range map[string]*Firestore{"a": a, "b": b} {
		name := name
		instance.Subscribe(ctx, key, func(event string, data []byte) {
			mu.Lock()
			got[name] = append(got[name], event+":"+string(data[:min(len(data), 5)]))
			mu.Unlock()
			done <- struct{}{}
		})
	}
	time.Sleep(time.Second) // Let both listeners see their first snapshot.
	if err := a.Publish(ctx, key, "entry", []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, key, "big", bytes.Repeat([]byte("y"), 2*inlineBytes)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatalf("events = %v", got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"a", "b"} {
		if len(got[name]) != 2 || got[name][0] != `entry:{"id"` || got[name][1] != "big:yyyyy" {
			t.Fatalf("%s got %v", name, got[name])
		}
	}
}
