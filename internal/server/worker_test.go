package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

type pushWorkspace struct {
	*store.Store
	claimError error
}

func (w *pushWorkspace) ClaimJob(context.Context, store.Job, string, time.Duration) (store.Reaction, bool, error) {
	return store.Reaction{}, false, w.claimError
}
func (w *pushWorkspace) JobFinished(context.Context, store.Job) (bool, error) { return true, nil }

func TestPushWorkerAcknowledgmentAndIsolation(t *testing.T) {
	for _, test := range []struct {
		name                  string
		authError, claimError error
		exists                bool
		code                  int
	}{
		{"stale", nil, nil, true, 204},
		{"busy", nil, store.ErrJobBusy, true, 503},
		{"gone", nil, nil, false, 204},
		{"unauthorized", errors.New("token"), nil, true, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, _ := dueGateway(t, nil, func(o *AuthOptions) {
				o.Mode = "worker"
				o.VerifyJob = func(*http.Request) (store.Job, error) {
					return store.Job{Workspace: strings.Repeat("a", 64), Reaction: "reaction", Dispatch: "dispatch"}, test.authError
				}
				o.Shared.Exists = func(context.Context, string) (bool, error) { return test.exists, nil }
				o.OpenWorkspace = func(string, store.BlobStore) (store.Workspace, error) {
					w, err := store.Open(t.TempDir())
					if err != nil {
						return nil, err
					}
					return &pushWorkspace{Store: w, claimError: test.claimError}, nil
				}
			})
			response := httptest.NewRecorder()
			g.ServeHTTP(response, httptest.NewRequest("POST", "/internal/work", nil))
			if response.Code != test.code {
				t.Fatalf("status %d, want %d: %s", response.Code, test.code, response.Body.String())
			}
			response = httptest.NewRecorder()
			g.ServeHTTP(response, httptest.NewRequest("GET", "/api/settings", nil))
			if response.Code != 404 {
				t.Fatalf("worker exposed user API: %d", response.Code)
			}
			if len(g.tenants) != 0 {
				t.Fatal("worker retained tenant between jobs")
			}
		})
	}
}

func TestPassiveWebKeepsDueWorkQueued(t *testing.T) {
	g, _ := dueGateway(t, func(*http.Request) (string, error) { return strings.Repeat("b", 64), nil }, func(o *AuthOptions) { o.Mode = "web" })
	response := httptest.NewRecorder()
	g.ServeHTTP(response, httptest.NewRequest("POST", "/internal/due", nil))
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	g.mu.Lock()
	tenant := g.tenants[strings.Repeat("b", 64)]
	g.mu.Unlock()
	if tenant == nil {
		t.Fatal("no workspace")
	}
	items, err := tenant.store.Reactions(context.Background())
	if err != nil || len(items) != 1 || !items[0].Due {
		t.Fatalf("web processed due reaction itself: %+v %v", items, err)
	}
}
