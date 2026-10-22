package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A connection the provider drops before answering is tried again; the
// request that then gets through is the one whose answer is returned.
func TestCompleteRetriesADroppedConnection(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijack")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"second time"}}]}`))
	}))
	defer server.Close()
	backoff := transportBackoff
	transportBackoff = time.Millisecond
	defer func() { transportBackoff = backoff }()

	client := New(server.URL, "key")
	reply, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if reply.Content != "second time" || calls.Load() != 2 {
		t.Fatalf("content=%q calls=%d", reply.Content, calls.Load())
	}
}

// Once the attempts are used up the transport error comes back as before.
func TestCompleteGivesUpAfterTheAttempts(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer server.Close()
	backoff := transportBackoff
	transportBackoff = time.Millisecond
	defer func() { transportBackoff = backoff }()

	client := New(server.URL, "key")
	if _, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}); err == nil {
		t.Fatal("expected a transport error")
	}
	if int(calls.Load()) != transportAttempts {
		t.Fatalf("calls=%d", calls.Load())
	}
}
