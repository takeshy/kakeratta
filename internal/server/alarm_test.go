package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The earliest wake stands; a later one asked for meanwhile waits behind it,
// and an earlier one replaces it.
func TestTheLocalAlarmKeepsTheEarliestTime(t *testing.T) {
	var fired atomic.Int32
	alarm := newLocalAlarm(func() { fired.Add(1) })
	defer alarm.Stop()
	if err := alarm.Set(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := alarm.Set(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := alarm.Set(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the earliest wake", func() bool { return fired.Load() == 1 })
	time.Sleep(200 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("fired %d times", got)
	}
	// Once it has gone off, the next is set afresh.
	if err := alarm.Set(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the next wake", func() bool { return fired.Load() == 2 })
}

func TestAStoppedAlarmDoesNotFire(t *testing.T) {
	var fired atomic.Int32
	alarm := newLocalAlarm(func() { fired.Add(1) })
	_ = alarm.Set(time.Now().Add(30 * time.Millisecond))
	alarm.Stop()
	_ = alarm.Set(time.Now().Add(10 * time.Millisecond))
	time.Sleep(150 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("a stopped alarm fired")
	}
}

// dueGateway is a gateway of a shared deployment whose alarms are recorded,
// over file workspaces.
func dueGateway(t *testing.T, verify func(*http.Request) (string, error)) (*Gateway, *dueCalls) {
	t.Helper()
	calls := &dueCalls{}
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), Version: "test",
		FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		Shared: &Shared{
			Alarm: func(_ context.Context, key string, at time.Time) error {
				calls.mu.Lock()
				defer calls.mu.Unlock()
				calls.at = append(calls.at, at)
				calls.keys = append(calls.keys, key)
				return nil
			},
			Verify:    verify,
			Watch:     func(context.Context, func(string)) {},
			Pending:   func(context.Context) (map[string]bool, error) { return nil, nil },
			Claimable: func(context.Context, string) (bool, error) { return false, nil },
			Exists:    func(context.Context, string) (bool, error) { return true, nil },
			Closing:   func(context.Context, string) (bool, error) { return false, nil },
		},
	}, fakeVerifier{})
	t.Cleanup(gateway.Close)
	return gateway, calls
}

type dueCalls struct {
	mu   sync.Mutex
	at   []time.Time
	keys []string
}

func (c *dueCalls) snapshot() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.at...)
}

// A task calling back wakes the workspace it names: the workspace is opened,
// works what is due, and asks for the next time. Anything else is refused.
func TestATaskCallingBackWakesAWorkspace(t *testing.T) {
	verify := func(r *http.Request) (string, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return "", errors.New("no")
		}
		var body struct {
			Workspace string `json:"workspace"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		return body.Workspace, err
	}
	gateway, calls := dueGateway(t, verify)
	post := func(header, workspace string) int {
		request := httptest.NewRequest(http.MethodPost, "/internal/due", strings.NewReader(`{"workspace":"`+workspace+`"}`))
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		recorder := httptest.NewRecorder()
		gateway.ServeHTTP(recorder, request)
		return recorder.Code
	}
	key := workspaceKey("user-due")
	if got := post("", key); got != http.StatusUnauthorized {
		t.Fatalf("without a token = %d", got)
	}
	if got := post("Bearer good", "../evil"); got != http.StatusBadRequest {
		t.Fatalf("a bad workspace = %d", got)
	}
	if len(calls.snapshot()) != 0 {
		t.Fatal("a refused wake opened a workspace")
	}
	before := time.Now()
	if got := post("Bearer good", key); got != http.StatusNoContent {
		t.Fatalf("a task = %d", got)
	}
	// Opening the workspace asked to be woken now; the due reaction it then
	// ran asked for the next time.
	eventually(t, "the workspace to ask for its next time", func() bool {
		for _, at := range calls.snapshot() {
			if at.After(before.Add(10 * time.Second)) {
				return true
			}
		}
		return false
	})
	for _, name := range calls.keys {
		if name != key {
			t.Fatalf("woke %s, want %s", name, key)
		}
	}
}

// Without a way to check tasks the route is not there.
func TestDueIsNotServedWithoutAVerifier(t *testing.T) {
	gateway, _ := dueGateway(t, nil)
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/internal/due", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}
