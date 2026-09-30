// Package cluster lets several instances of the API serve the same users.
//
// Most of a workspace is plain persistent state any instance can read and
// write. What is not is the persona runner: its queue worker, the turns
// waiting on an external agent, the routing and reply trials, the daily and
// schedule checks. Those run on exactly one instance per workspace - its
// owner - which holds a lease on the workspace and renews it while it lives.
//
// Any instance can take a request. One that needs the runner is carried to
// the owner and its answer carried back; events go to every instance with a
// page open on that workspace. When an owner dies or shuts down, its lease
// runs out or is released, and the next instance to need the workspace takes
// it over, along with the reactions it left unfinished.
package cluster

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// LeaseTTL is how long a lease holds without renewal, and RenewEvery how
// often an owner renews it. An owner that misses two renewals in a row stops
// working the workspace before anyone else may start.
const (
	LeaseTTL   = 30 * time.Second
	RenewEvery = 10 * time.Second
)

var (
	// ErrOwnerGone is a forwarded request whose owner stopped holding the
	// workspace before answering. The caller may take the workspace over and
	// try again.
	ErrOwnerGone = errors.New("the instance serving this workspace went away")
	// ErrClosing is a workspace being deleted.
	ErrClosing = errors.New("workspace is being deleted")
)

// Lease is who works a workspace, and until when.
type Lease struct {
	Owner string
	Until time.Time
	// Closing is set while the workspace is being deleted: nobody may take
	// it, and whoever holds it lets it go.
	Closing bool
}

// Live reports whether someone holds the workspace now.
func (l Lease) Live(now time.Time) bool { return l.Owner != "" && now.Before(l.Until) }

// Call is a request carried to the owner.
type Call struct {
	Method string
	URL    string
	Host   string
	Header http.Header
	Body   []byte
}

// Reply is the owner's answer to a Call.
type Reply struct {
	Status int
	Header http.Header
	Body   []byte
}

// Coordinator is what instances share to divide the workspaces between them.
type Coordinator interface {
	// Instance names this process among the others.
	Instance() string
	// Lease reads who holds a workspace.
	Lease(ctx context.Context, key string) (Lease, error)
	// Acquire takes a workspace nobody holds, or extends this instance's own
	// hold on it. It reports false when another instance holds it, or when
	// the workspace is closing.
	Acquire(ctx context.Context, key string) (bool, error)
	// Release lets a workspace go at once, so another instance need not wait
	// for the lease to run out.
	Release(ctx context.Context, key string) error
	// MarkClosing stops anyone from taking the workspace; ClearClosing lifts
	// that once it is gone.
	MarkClosing(ctx context.Context, key string) error
	ClearClosing(ctx context.Context, key string) error

	// Forward carries a call to the owner and waits for its reply.
	Forward(ctx context.Context, key, owner string, call Call) (Reply, error)
	// Serve answers the calls forwarded to this instance for a workspace
	// until ctx ends.
	Serve(ctx context.Context, key string, handle func(context.Context, Call) Reply)

	// Publish sends an event to every instance subscribed to the workspace,
	// this one included.
	Publish(ctx context.Context, key, name string, data []byte) error
	// Subscribe delivers the workspace's events published from now on until
	// ctx ends.
	Subscribe(ctx context.Context, key string, deliver func(name string, data []byte))
}
