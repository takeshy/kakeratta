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

	"github.com/takeshy/kakeratta/internal/store"
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

func TestAlarmStopWaitsForTheRunningCallback(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var alarm *localAlarm
	alarm = newLocalAlarm(func() {
		close(entered)
		<-release
		// A callback can ask to retry while Stop waits, without deadlocking
		// or starting another callback after shutdown.
		_ = alarm.Set(time.Now())
		close(finished)
	})
	defer alarm.Stop()
	defer unblock()
	_ = alarm.Set(time.Now())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("alarm did not fire")
	}
	stopped := make(chan struct{})
	go func() { alarm.Stop(); close(stopped) }()
	eventually(t, "alarm stopping", func() bool {
		alarm.mu.Lock()
		defer alarm.mu.Unlock()
		return alarm.stopped
	})
	select {
	case <-stopped:
		t.Fatal("Stop returned while the callback could still write to the workspace")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the callback finished")
	}
	select {
	case <-finished:
	default:
		t.Fatal("callback had not finished when Stop returned")
	}
}

func TestGatewayCloseWaitsForLocalWakeWithoutBlockingItsRetry(t *testing.T) {
	gateway, _ := dueGateway(t, nil, func(o *AuthOptions) { o.Shared.Alarm = nil })
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	alarm := newLocalAlarm(func() {
		close(entered)
		<-release
		_ = gateway.setAlarm("retry", time.Now())
	})
	gateway.alarmsMu.Lock()
	gateway.alarms["running"] = alarm
	gateway.alarmsMu.Unlock()
	_ = alarm.Set(time.Now())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("local wake did not start")
	}
	closed := make(chan struct{})
	go func() { gateway.Close(); close(closed) }()
	eventually(t, "local wake stopping", func() bool {
		alarm.mu.Lock()
		defer alarm.mu.Unlock()
		return alarm.stopped
	})
	select {
	case <-closed:
		t.Fatal("gateway closed before its local wake finished")
	default:
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("gateway close blocked the callback's retry")
	}
	gateway.alarmsMu.Lock()
	retry := gateway.alarms["retry"]
	gateway.alarmsMu.Unlock()
	if retry != nil {
		t.Fatal("retry created an alarm after shutdown")
	}
	if _, err := gateway.workspace("after-close"); !errors.Is(err, errStopping) {
		t.Fatalf("closed gateway opened another workspace: %v", err)
	}
}

// dueGateway is a gateway of a shared deployment whose alarms are recorded,
// over file workspaces.
func dueGateway(t *testing.T, verify func(*http.Request) (string, error), tweak ...func(*AuthOptions)) (*Gateway, *dueCalls) {
	t.Helper()
	calls := &dueCalls{}
	options := AuthOptions{
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
	}
	for _, change := range tweak {
		change(&options)
	}
	gateway := newGateway(options, fakeVerifier{})
	t.Cleanup(gateway.Close)
	return gateway, calls
}

// failingQueue is a workspace that cannot keep a reaction.
type failingQueue struct{ *store.Store }

func (failingQueue) CreateReaction(store.Reaction) error { return errors.New("the queue is down") }
func (failingQueue) SaveReaction(store.Reaction) error   { return errors.New("the queue is down") }

// A wake that could not be kept is answered with a failure, so the task is
// tried again; one for a workspace that is gone or being deleted is not.
func TestAWakeThatIsNotKeptIsAFailure(t *testing.T) {
	verify := func(r *http.Request) (string, error) {
		var body struct {
			Workspace string `json:"workspace"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		return body.Workspace, err
	}
	post := func(g *Gateway, key string) int {
		request := httptest.NewRequest(http.MethodPost, "/internal/due", strings.NewReader(`{"workspace":"`+key+`"}`))
		recorder := httptest.NewRecorder()
		g.ServeHTTP(recorder, request)
		return recorder.Code
	}
	key := workspaceKey("user-failing")

	broken, _ := dueGateway(t, verify, func(o *AuthOptions) {
		o.OpenWorkspace = func(userKey string, blobs store.BlobStore) (store.Workspace, error) {
			s, err := store.Open(t.TempDir())
			return failingQueue{s}, err
		}
	})
	if got := post(broken, key); got != http.StatusServiceUnavailable {
		t.Fatalf("a wake the queue could not keep = %d", got)
	}

	unreadable, _ := dueGateway(t, verify, func(o *AuthOptions) {
		o.Shared.Exists = func(context.Context, string) (bool, error) { return false, errors.New("firestore is down") }
	})
	if got := post(unreadable, key); got != http.StatusServiceUnavailable {
		t.Fatalf("a wake that could not tell if the workspace exists = %d", got)
	}

	unopenable, _ := dueGateway(t, verify, func(o *AuthOptions) {
		o.OpenWorkspace = func(string, store.BlobStore) (store.Workspace, error) { return nil, errors.New("cannot open") }
	})
	if got := post(unopenable, key); got != http.StatusServiceUnavailable {
		t.Fatalf("a wake that could not open the workspace = %d", got)
	}

	gone, _ := dueGateway(t, verify, func(o *AuthOptions) {
		o.Shared.Exists = func(context.Context, string) (bool, error) { return false, nil }
	})
	if got := post(gone, key); got != http.StatusNoContent {
		t.Fatalf("a wake for a workspace that is gone = %d", got)
	}

	closing, _ := dueGateway(t, verify, func(o *AuthOptions) {
		o.Shared.Closing = func(context.Context, string) (bool, error) { return true, nil }
	})
	if got := post(closing, key); got != http.StatusNoContent {
		t.Fatalf("a wake for a workspace being deleted = %d", got)
	}
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
