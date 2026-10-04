package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

type workspaceResult struct {
	tenant *Server
	err    error
}

func requestWorkspace(g *Gateway, uid string) <-chan workspaceResult {
	result := make(chan workspaceResult, 1)
	go func() { tenant, err := g.tenant(uid); result <- workspaceResult{tenant, err} }()
	return result
}

func awaitWorkspace(t *testing.T, result <-chan workspaceResult) *Server {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.tenant
	case <-time.After(5 * time.Second):
		t.Fatal("workspace blocked by another account")
		return nil
	}
}

func blockedGateway(t *testing.T, stage string) (*Gateway, <-chan struct{}, func(), *atomic.Int32) {
	t.Helper()
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	block := func() { once.Do(func() { close(entered); <-unblock }) }
	root := t.TempDir()
	opens := &atomic.Int32{}
	options := AuthOptions{OpenWorkspace: func(key string, _ store.BlobStore) (store.Workspace, error) {
		if key == workspaceKey("alice") {
			opens.Add(1)
			if stage == "open" {
				block()
			}
		}
		return store.Open(filepath.Join(root, key))
	}}
	if stage == "closing" {
		options.Shared = &Shared{
			Watch:   func(context.Context, func(string)) {},
			Pending: func(context.Context) (map[string]bool, error) { return nil, nil },
			Alarm:   func(context.Context, string, time.Time) error { return nil },
			Closing: func(_ context.Context, key string) (bool, error) {
				if key == workspaceKey("alice") {
					block()
				}
				return false, nil
			},
		}
	}
	g := newGateway(options, fakeVerifier{})
	t.Cleanup(g.Close)
	t.Cleanup(release)
	return g, entered, release, opens
}

func TestGatewayWorkspaceLocksArePerAccount(t *testing.T) {
	for _, stage := range []string{"open", "closing"} {
		t.Run(stage, func(t *testing.T) {
			g, entered, release, opens := blockedGateway(t, stage)
			bob := awaitWorkspace(t, requestWorkspace(g, "bob"))
			first := requestWorkspace(g, "alice")
			<-entered
			second := requestWorkspace(g, "alice")
			if got := awaitWorkspace(t, requestWorkspace(g, "bob")); got != bob {
				t.Fatal("cached workspace replaced")
			}
			awaitWorkspace(t, requestWorkspace(g, "carol"))
			release()
			alice := awaitWorkspace(t, first)
			if got := awaitWorkspace(t, second); got != alice {
				t.Fatal("same account opened twice")
			}
			if opens.Load() != 1 {
				t.Fatalf("opens = %d", opens.Load())
			}
		})
	}
}

func TestGatewayCloseWaitsForOpeningWorkspace(t *testing.T) {
	g, entered, release, _ := blockedGateway(t, "open")
	result := requestWorkspace(g, "alice")
	<-entered
	closed := make(chan struct{})
	go func() { g.Close(); close(closed) }()
	<-g.stop
	select {
	case <-closed:
		t.Fatal("shutdown returned while workspace was opening")
	default:
	}
	if got := <-requestWorkspace(g, "bob"); !errors.Is(got.err, errStopping) {
		t.Fatalf("new workspace during shutdown: %v", got.err)
	}
	release()
	awaitWorkspace(t, result)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestGatewayDeletionWaitsForOpeningAccount(t *testing.T) {
	g, entered, release, opens := blockedGateway(t, "open")
	first := requestWorkspace(g, "alice")
	<-entered
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		g.closeAccount(response, httptest.NewRequest(http.MethodDelete, "/api/workspace", strings.NewReader(`{"confirm":true}`)), "alice")
		deleted <- response
	}()
	eventually(t, "account marked closing", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.closing[workspaceKey("alice")] })
	select {
	case got := <-requestWorkspace(g, "alice"):
		if !errors.Is(got.err, errWorkspaceClosing) {
			t.Fatalf("access during deletion: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request waited behind deletion")
	}
	awaitWorkspace(t, requestWorkspace(g, "bob"))
	release()
	awaitWorkspace(t, first)
	select {
	case response := <-deleted:
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not finish")
	}
	if opens.Load() != 1 {
		t.Fatalf("deletion opened a second workspace: %d", opens.Load())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tenants[workspaceKey("alice")] != nil {
		t.Fatal("deleted workspace still cached")
	}
}

func TestGatewayRestoreUsesBoundedWorkersAndStopsAdmission(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan string, workspaceWorkers*2)
			unblock := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(unblock) }) }
			work := func(key string) error { started <- key; <-unblock; return errors.New("test storage unavailable") }
			keys := make([]string, workspaceWorkers*2)
			for i := range keys {
				keys[i] = workspaceKey(string(rune('a' + i)))
			}
			options := AuthOptions{
				ListWorkspaces: func(context.Context) ([]string, error) { return keys, nil },
				OpenWorkspace:  func(key string, _ store.BlobStore) (store.Workspace, error) { return nil, work(key) },
			}
			if shared {
				options.Shared = &Shared{
					Watch:   func(context.Context, func(string)) {},
					Pending: func(context.Context) (map[string]bool, error) { return nil, nil },
					Alarm:   func(_ context.Context, key string, _ time.Time) error { return work(key) },
				}
			}
			g := newGateway(options, fakeVerifier{})
			t.Cleanup(g.Close)
			t.Cleanup(release)
			for i := 0; i < workspaceWorkers; i++ {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("restoration serialized across accounts")
				}
			}
			select {
			case <-started:
				t.Fatal("restore exceeded worker limit")
			case <-time.After(50 * time.Millisecond):
			}
			closed := make(chan struct{})
			go func() { g.Close(); close(closed) }()
			<-g.stop
			release()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("restore prevented shutdown")
			}
			select {
			case <-started:
				t.Fatal("restore admitted work after shutdown")
			default:
			}
		})
	}
}

func TestGatewayCloseIdleSkipsBusyAccount(t *testing.T) {
	g := testGateway(t)
	key := workspaceKey("alice")
	tenant := awaitWorkspace(t, requestWorkspace(g, "alice"))
	lock, release := g.workspaceLock(key)
	defer release()
	lock.mu.Lock()
	defer lock.mu.Unlock()
	g.mu.Lock()
	g.used[key] = time.Now().Add(-2 * idleTenant)
	g.mu.Unlock()
	done := make(chan struct{})
	go func() { g.closeIdle(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle sweep waited for busy account")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tenants[key] != tenant {
		t.Fatal("idle sweep evicted busy account")
	}
}

func TestGatewayBackgroundWakesAreBounded(t *testing.T) {
	started := make(chan string, workspaceWorkers*2)
	unblock := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	g := newGateway(AuthOptions{
		OpenWorkspace: func(string, store.BlobStore) (store.Workspace, error) { return nil, errors.New("unexpected open") },
		Shared: &Shared{
			Watch:     func(context.Context, func(string)) {},
			Pending:   func(context.Context) (map[string]bool, error) { return nil, nil },
			Claimable: func(_ context.Context, key string) (bool, error) { started <- key; <-unblock; return false, nil },
		},
	}, fakeVerifier{})
	t.Cleanup(g.Close)
	t.Cleanup(release)
	for i := 0; i < workspaceWorkers*2; i++ {
		key := workspaceKey(string(rune('a' + i)))
		g.wake(key)
		g.wake(key) // Repeated notifications coalesce while waiting.
	}
	for i := 0; i < workspaceWorkers; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("background wakes did not proceed concurrently")
		}
	}
	select {
	case <-started:
		t.Fatal("background wakes exceeded worker limit")
	case <-time.After(100 * time.Millisecond):
	}
	g.Close()
	release()
	eventually(t, "background wakes stopped", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return len(g.waking) == 0 })
	select {
	case <-started:
		t.Fatal("background wake started storage I/O after shutdown")
	default:
	}
}
