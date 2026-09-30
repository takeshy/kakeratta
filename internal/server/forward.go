package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/cluster"
)

// maxForwardBody bounds a request carried to another instance. The requests
// that need the owner are small JSON; posts with images are never carried.
const maxForwardBody = 4 << 20

// ownerRoute names the requests that need the workspace's persona runner -
// its external-agent desk, its routing and reply trials, its calendar locks -
// and so must be answered by the instance that owns the workspace.
func ownerRoute(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/mcp/"):
		return true
	case strings.HasPrefix(path, "/api/entry/"):
		return strings.HasSuffix(path, "/routing/check") || strings.HasSuffix(path, "/routing/apply") ||
			strings.HasSuffix(path, "/reply/preview") || strings.HasSuffix(path, "/reply/apply")
	case path == "/api/settings":
		return r.Method == http.MethodPut
	case path == "/api/personas" || strings.HasPrefix(path, "/api/personas/"):
		return r.Method != http.MethodGet && r.Method != http.MethodHead
	}
	return false
}

// hopHeaders are not carried with a forwarded request or reply. The session
// cookie stays behind too: the caller has already checked it.
var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Cookie", "Content-Length"}

func forwardHeader(header http.Header) http.Header {
	out := header.Clone()
	if out == nil {
		out = http.Header{}
	}
	for _, name := range hopHeaders {
		out.Del(name)
	}
	return out
}

// serveCall answers a request another instance carried here.
func (s *Server) serveCall(ctx context.Context, call cluster.Call) cluster.Reply {
	request, err := http.NewRequestWithContext(ctx, call.Method, call.URL, bytes.NewReader(call.Body))
	if err != nil {
		return cluster.Reply{Status: http.StatusBadRequest, Body: []byte(`{"error":"forwarded request unreadable"}`)}
	}
	request.Host = call.Host
	request.Header = forwardHeader(call.Header)
	recorder := &replyRecorder{header: http.Header{}}
	s.ServeHTTP(recorder, request)
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return cluster.Reply{Status: recorder.status, Header: forwardHeader(recorder.header), Body: recorder.body.Bytes()}
}

type replyRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *replyRecorder) Header() http.Header { return r.header }
func (r *replyRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *replyRecorder) Write(data []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(data)
}

// clusterBus carries one workspace's events through the coordinator.
type clusterBus struct {
	coordinator cluster.Coordinator
	key         string
}

func (b clusterBus) Publish(name string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return b.coordinator.Publish(ctx, b.key, name, data)
}

func (b clusterBus) Subscribe(ctx context.Context, deliver func(string, []byte)) {
	b.coordinator.Subscribe(ctx, b.key, deliver)
}

var errNoOwner = errors.New("workspace unavailable; retry later")

// owner finds who works a workspace: this instance, whose tenant is
// returned, or another one, named. With take, a workspace nobody holds is
// taken over here; without, it is reported unavailable.
func (g *Gateway) owner(ctx context.Context, key string, take bool) (*Server, string, error) {
	g.mu.Lock()
	if _, owned := g.owned[key]; owned {
		tenant := g.tenants[key]
		g.mu.Unlock()
		return tenant, "", nil
	}
	if g.demoting[key] {
		// The runner here is still finishing; nobody may start another.
		g.mu.Unlock()
		return nil, "", errNoOwner
	}
	g.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		lease, err := g.cluster.Lease(ctx, key)
		if err != nil {
			return nil, "", err
		}
		if lease.Closing {
			return nil, "", cluster.ErrClosing
		}
		if lease.Live(time.Now()) && lease.Owner != g.cluster.Instance() {
			return nil, lease.Owner, nil
		}
		if !take {
			return nil, "", errNoOwner
		}
		acquired, err := g.cluster.Acquire(ctx, key)
		if err != nil {
			return nil, "", err
		}
		if acquired {
			tenant, err := g.promote(key)
			return tenant, "", err
		}
	}
	return nil, "", errNoOwner
}

// promote makes this instance the one working a workspace whose lease it
// has just taken.
func (g *Gateway) promote(key string) (*Server, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, owned := g.owned[key]; owned {
		return g.tenants[key], nil
	}
	if g.closing[key] {
		return nil, cluster.ErrClosing
	}
	if g.demoting[key] {
		return nil, errNoOwner
	}
	tenant := g.tenants[key]
	if tenant == nil {
		var err error
		if tenant, err = g.open(key); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = g.cluster.Release(ctx, key)
			return nil, err
		}
		g.tenants[key] = tenant
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.owned[key] = cancel
	g.renewed[key] = time.Now()
	g.used[key] = time.Now()
	tenant.Lead()
	g.cluster.Serve(ctx, key, tenant.serveCall)
	return tenant, nil
}

// demote stops working a workspace here: the lease was lost, the workspace
// is closing, or the process is shutting down. The reaction in hand is
// finished; the rest stays kept for whoever takes the workspace next.
func (g *Gateway) demote(key string) {
	g.mu.Lock()
	cancel, owned := g.owned[key]
	tenant := g.tenants[key]
	delete(g.owned, key)
	delete(g.renewed, key)
	delete(g.tenants, key)
	if owned {
		g.demoting[key] = true
	}
	g.mu.Unlock()
	if !owned {
		return
	}
	defer func() {
		g.mu.Lock()
		delete(g.demoting, key)
		g.mu.Unlock()
	}()
	cancel()
	if tenant != nil {
		tenant.Halt()
		closeWorkspace(tenant)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := g.cluster.Release(ctx, key); err != nil {
		log.Printf("release %s: %v", key, err)
	}
}

// serveOwned answers a request that needs the workspace's runner, here or on
// the instance that owns it.
func (g *Gateway) serveOwned(w http.ResponseWriter, r *http.Request, key string, take bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxForwardBody))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		tenant, other, err := g.owner(r.Context(), key, take)
		if errors.Is(err, cluster.ErrClosing) {
			fail(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, errNoOwner.Error())
			return
		}
		if tenant != nil {
			r.Body = io.NopCloser(bytes.NewReader(body))
			tenant.ServeHTTP(w, r)
			return
		}
		reply, err := g.cluster.Forward(r.Context(), key, other, cluster.Call{
			Method: r.Method, URL: r.URL.RequestURI(), Host: r.Host, Header: forwardHeader(r.Header), Body: body,
		})
		if errors.Is(err, cluster.ErrOwnerGone) {
			continue
		}
		if err != nil {
			if r.Context().Err() == nil {
				log.Printf("forward %s to %s: %v", r.URL.Path, other, err)
			}
			fail(w, http.StatusBadGateway, "workspace unavailable; retry later")
			return
		}
		for name, values := range forwardHeader(reply.Header) {
			w.Header()[name] = values
		}
		w.WriteHeader(reply.Status)
		_, _ = w.Write(reply.Body)
		return
	}
	fail(w, http.StatusServiceUnavailable, errNoOwner.Error())
}

// idleFollower is how long a workspace another instance owns stays open here
// after its last request.
const idleFollower = 10 * time.Minute

// keepLeases renews the workspaces this instance owns until the gateway
// closes. One whose lease is lost, or that is being deleted, is let go; so is
// one whose lease could not be renewed for so long that another instance may
// soon take it.
func (g *Gateway) keepLeases() {
	defer close(g.leasesDone)
	ticker := time.NewTicker(cluster.RenewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
		}
		g.mu.Lock()
		keys := make([]string, 0, len(g.owned))
		for key := range g.owned {
			keys = append(keys, key)
		}
		g.mu.Unlock()
		for _, key := range keys {
			ctx, cancel := context.WithTimeout(context.Background(), cluster.RenewEvery)
			kept, err := g.cluster.Acquire(ctx, key)
			cancel()
			g.mu.Lock()
			if err == nil && kept {
				g.renewed[key] = time.Now()
			}
			last := g.renewed[key]
			g.mu.Unlock()
			if (err == nil && !kept) || time.Since(last) > cluster.LeaseTTL-cluster.RenewEvery {
				if err != nil {
					log.Printf("renew %s: %v", key, err)
				}
				// Letting go waits for the reaction in hand; the other
				// workspaces' leases are renewed meanwhile.
				g.demotions.Add(1)
				go func(key string) {
					defer g.demotions.Done()
					g.demote(key)
				}(key)
			}
		}
	}
}

// restoreCluster takes over the workspaces nobody works, so their schedules
// and check-ins run, and closes what this instance holds open for nothing.
func (g *Gateway) restoreCluster(keys []string) {
	now := time.Now()
	for _, key := range keys {
		select {
		case <-g.stop:
			return
		default:
		}
		g.mu.Lock()
		_, owned := g.owned[key]
		busy := g.closing[key] || g.demoting[key]
		g.mu.Unlock()
		if owned || busy {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		lease, err := g.cluster.Lease(ctx, key)
		if err == nil && lease.Closing {
			g.dropFollower(key)
		} else if err == nil && !lease.Live(now) {
			if acquired, err := g.cluster.Acquire(ctx, key); err == nil && acquired {
				if _, err := g.promote(key); err != nil {
					log.Printf("take over %s: %v", key, err)
				}
			}
		}
		cancel()
	}
	g.mu.Lock()
	var idle []string
	for key, tenant := range g.tenants {
		if _, owned := g.owned[key]; !owned && now.Sub(g.used[key]) > idleFollower && tenant.Idle() {
			idle = append(idle, key)
		}
	}
	g.mu.Unlock()
	for _, key := range idle {
		g.dropFollower(key)
	}
}

// dropFollower closes a workspace this instance only serves requests for.
func (g *Gateway) dropFollower(key string) {
	g.mu.Lock()
	tenant := g.tenants[key]
	if _, owned := g.owned[key]; owned || tenant == nil {
		g.mu.Unlock()
		return
	}
	delete(g.tenants, key)
	delete(g.used, key)
	g.mu.Unlock()
	tenant.Halt()
	closeWorkspace(tenant)
}

// closeWorkspace lets go of what a closed tenant's workspace holds open in
// this process, such as its materialised skills and their change feed.
func closeWorkspace(tenant *Server) {
	if closer, ok := tenant.store.(io.Closer); ok {
		_ = closer.Close()
	}
}

// waitForRelease waits until nobody else works a closing workspace: its owner
// sees the closing mark at its next renewal and lets go.
func (g *Gateway) waitForRelease(ctx context.Context, key string) error {
	for {
		lease, err := g.cluster.Lease(ctx, key)
		if err != nil {
			return err
		}
		if !lease.Live(time.Now()) || lease.Owner == g.cluster.Instance() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
