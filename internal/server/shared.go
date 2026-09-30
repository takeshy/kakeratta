package server

import (
	"context"
	"io"
	"log"
	"math/rand/v2"
	"time"

	"github.com/takeshy/kakeratta/internal/cluster"
	"github.com/takeshy/kakeratta/internal/store"
)

// Shared is what the instances of one deployment share besides the
// workspaces themselves. With it, any number of instances serve the same
// users: every request is answered where it lands, and each workspace's
// persona work is taken up reaction by reaction by whichever instance claims
// it (store.Reaction). No instance owns a workspace.
type Shared struct {
	// Coordinator elects the one instance that queues every workspace's
	// periodic checks each minute.
	Coordinator cluster.Coordinator
	// Tick queues a workspace's periodic checks without opening it.
	Tick func(ctx context.Context, key string) error
	// Watch reports the workspaces reactions are queued for, until ctx ends.
	Watch func(ctx context.Context, queued func(key string))
	// Pending names the workspaces with work someone could take up now.
	Pending func(ctx context.Context) (map[string]bool, error)
	// Claimable reports whether one workspace has such work.
	Claimable func(ctx context.Context, key string) (bool, error)
	// Exists reports whether a workspace was ever set up.
	Exists func(ctx context.Context, key string) (bool, error)
	// Closing reports whether a workspace is being deleted.
	Closing func(ctx context.Context, key string) (bool, error)
}

const (
	// schedulerJob is the lease name of the instance queueing ticks.
	schedulerJob = "scheduler"
	// idleTenant is how long an open workspace stays open after its last
	// request, with nothing in hand and no page listening.
	idleTenant = 10 * time.Minute
	// affinity is how long an instance without a workspace open waits before
	// opening it for queued work, so one that has it open claims it first.
	affinity = 2 * time.Second
)

// sweepDelays are when a deleted workspace is deleted again: after requests
// in flight on other instances when it was marked closing, and after the
// longest turn that may have been running there.
var sweepDelays = []time.Duration{90 * time.Second, 11 * time.Minute}

// wake gets queued work of a workspace taken up. An instance with the
// workspace open is poked at once; one without opens it only if the work is
// still there a moment later.
func (g *Gateway) wake(key string) {
	g.mu.Lock()
	tenant := g.tenants[key]
	if tenant != nil {
		// Work keeps a workspace open where it is, so the same instance goes
		// on claiming it rather than another opening it afresh.
		g.used[key] = time.Now()
		g.mu.Unlock()
		tenant.runner.Poke()
		return
	}
	if g.waking[key] || g.closing[key] {
		g.mu.Unlock()
		return
	}
	g.waking[key] = true
	g.mu.Unlock()
	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.waking, key)
			g.mu.Unlock()
		}()
		select {
		case <-g.stop:
			return
		case <-time.After(affinity + rand.N(time.Second)):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		claimable, err := g.shared.Claimable(ctx, key)
		cancel()
		if err != nil || !claimable {
			return
		}
		// Opening the workspace starts its runner, which claims the work.
		if _, err := g.workspace(key); err != nil {
			log.Printf("open %s for queued work: %v", key, err)
		}
	}()
}

// tickShared runs the minute's shared duties: the elected instance queues
// every workspace's periodic checks; every instance takes up work nobody has
// claimed and closes what it holds open for nothing.
func (g *Gateway) tickShared(keys []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if elected, err := g.shared.Coordinator.Acquire(ctx, schedulerJob); err != nil {
		log.Printf("scheduler election: %v", err)
	} else if elected {
		for _, key := range keys {
			select {
			case <-g.stop:
				return
			default:
			}
			g.mu.Lock()
			closing := g.closing[key]
			g.mu.Unlock()
			if closing {
				continue
			}
			if err := g.shared.Tick(ctx, key); err != nil {
				log.Printf("tick %s: %v", key, err)
			}
		}
	}
	if pending, err := g.shared.Pending(ctx); err != nil {
		log.Printf("pending work: %v", err)
	} else {
		for key := range pending {
			g.wake(key)
		}
	}
	g.closeIdle()
}

// closeIdle closes the workspaces open here with nothing to do.
func (g *Gateway) closeIdle() {
	now := time.Now()
	g.mu.Lock()
	var idle []*Server
	for key, tenant := range g.tenants {
		if now.Sub(g.used[key]) > idleTenant && tenant.Idle() {
			idle = append(idle, tenant)
			delete(g.tenants, key)
			delete(g.used, key)
		}
	}
	g.mu.Unlock()
	for _, tenant := range idle {
		tenant.Halt()
		closeWorkspace(tenant)
	}
}

// closeWorkspace lets go of what a closed tenant's workspace holds open in
// this process, such as its materialised skills and their change feed.
func closeWorkspace(tenant *Server) {
	if closer, ok := tenant.store.(io.Closer); ok {
		_ = closer.Close()
	}
}

// waitForClaims waits until no reaction of a closing workspace is in hand
// anywhere: a claim either finishes or, marked closing, is not renewed.
func waitForClaims(ctx context.Context, workspace store.Workspace) error {
	for {
		items, err := workspace.Reactions(ctx)
		if err != nil {
			return err
		}
		held := false
		now := time.Now()
		for _, item := range items {
			if item.ClaimedBy != "" && now.Before(item.ClaimUntil) {
				held = true
			}
		}
		if !held {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// sweepClosed deletes a deleted workspace again: whatever a request or turn
// in flight elsewhere wrote after the first pass goes too. The closing mark
// is lifted after the last pass, so the same account can start afresh.
func (g *Gateway) sweepClosed(key string) {
	start := time.Now()
	for _, delay := range sweepDelays {
		select {
		case <-g.stop:
			return
		case <-time.After(time.Until(start.Add(delay))):
		}
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		tenant, err := g.open(key)
		if err != nil {
			log.Printf("sweep closed %s: %v", key, err)
			cancel()
			continue
		}
		tenant.Halt()
		if err := tenant.destroy(ctx); err != nil {
			log.Printf("sweep closed %s: %v", key, err)
		}
		if delay == sweepDelays[len(sweepDelays)-1] {
			if err := tenant.store.SetClosing(ctx, false); err != nil {
				log.Printf("sweep closed %s: %v", key, err)
			}
		}
		closeWorkspace(tenant)
		cancel()
	}
}
