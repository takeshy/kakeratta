package persona

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
)

type observedIndexRenewals struct {
	store.Workspace
	renewed chan struct{}
}

func (w observedIndexRenewals) RenewReaction(ctx context.Context, id, worker string, ttl time.Duration) (bool, error) {
	kept, err := w.Workspace.RenewReaction(ctx, id, worker, ttl)
	if kept && err == nil {
		select {
		case w.renewed <- struct{}{}:
		default:
		}
	}
	return kept, err
}

func TestIndexRetainsItsLeaseUntilEmbeddingFinishes(t *testing.T) {
	old := claimTTL
	claimTTL = 600 * time.Millisecond
	defer func() { claimTTL = old }()
	r, s, _ := setup(t, answering)
	renewed := make(chan struct{}, 4)
	r.store = observedIndexRenewals{s, renewed}
	var completed atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		for range 2 {
			select {
			case <-renewed:
			case <-req.Context().Done():
				return
			case <-time.After(5 * time.Second):
				t.Error("lease was not renewed")
				return
			}
		}
		v := make([]float64, 1024)
		for i := range v {
			v[i] = .1
		}
		completed.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer endpoint.Close()
	settings := s.Settings()
	settings.BaseURL = endpoint.URL
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	post(t, s, "index me")
	r.SetSearch(search.New(s))
	r.ScheduleIndex()
	item, rest, ok, err := s.ClaimReaction(context.Background(), r.worker, claimTTL)
	if err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	r.handle(item, rest)
	if !completed.Load() {
		t.Fatal("indexing cancelled before embedding completed")
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("completed indexing still queued: %+v", got)
	}
}

func TestFailedIndexRetainsADelayedRetry(t *testing.T) {
	r, s, _ := setup(t, answering)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer endpoint.Close()
	settings := s.Settings()
	settings.BaseURL = endpoint.URL
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	post(t, s, "retry indexing")
	r.SetSearch(search.New(s))
	r.ScheduleIndex()
	item, rest, ok, err := s.ClaimReaction(context.Background(), r.worker, claimTTL)
	if err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	r.handle(item, rest)
	got := pending(t, s)
	if len(got) != 1 || !got[0].Index || got[0].ClaimedBy != "" || got[0].Attempts != 0 || !got[0].NotBefore.After(time.Now()) {
		t.Fatalf("retry = %+v", got)
	}
}
