// Package cluster elects, among the instances serving the API, the one that
// does a job only one of them should: queueing every workspace's periodic
// checks each minute.
//
// Nothing else needs electing. Each workspace's persona work is taken up
// reaction by reaction by whichever instance claims it first (see
// store.Reaction), so no instance owns a workspace and any of them can stop
// at any moment.
package cluster

import (
	"context"
	"time"
)

// LeaseTTL is how long a lease holds without renewal. The scheduler renews
// its lease every minute, as it queues the minute's checks; a lease that
// outlives one missed minute keeps two instances from taking turns.
const LeaseTTL = 90 * time.Second

// Lease is who holds a named job, and until when.
type Lease struct {
	Owner string
	Until time.Time
}

// Live reports whether someone holds the job now.
func (l Lease) Live(now time.Time) bool { return l.Owner != "" && now.Before(l.Until) }

// Coordinator is what instances share to hand a job to one of them.
type Coordinator interface {
	// Instance names this process among the others.
	Instance() string
	// Lease reads who holds a job.
	Lease(ctx context.Context, name string) (Lease, error)
	// Acquire takes a job nobody holds, or extends this instance's own hold
	// on it. It reports false when another instance holds it.
	Acquire(ctx context.Context, name string) (bool, error)
	// Release lets a job go at once, so another instance need not wait for
	// the lease to run out.
	Release(ctx context.Context, name string) error
}
