package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// Shared is what the instances of one deployment share besides the
// workspaces themselves. With it, any number of instances serve the same
// users: every request is answered where it lands, and each workspace's
// persona work is taken up reaction by reaction by whichever instance claims
// it (store.Reaction). No instance owns a workspace, and none keeps a clock:
// a workspace asks to be woken at the next time anything of its comes due.
type Shared struct {
	// Alarm wakes a workspace at a time without opening it, by queueing a
	// task for it. Without one, a timer in each process does, and opens the
	// workspace when it fires.
	Alarm func(ctx context.Context, key string, at time.Time) error
	// Verify reads a wake a task delivered to /internal/due and returns the
	// workspace it names, refusing anything that is not a task's. Without
	// one, /internal/due is not served.
	Verify func(r *http.Request) (string, error)
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

// sweepShared takes up work nobody has claimed and closes what this instance
// holds open for nothing. It queues nothing: the work of a workspace's own
// times is its alarm's.
func (g *Gateway) sweepShared() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if pending, err := g.shared.Pending(ctx); err != nil {
		log.Printf("pending work: %v", err)
	} else {
		for key := range pending {
			g.wake(key)
		}
	}
	g.closeIdle()
}

// setAlarm wakes a workspace at a time: a task if the deployment has a queue
// for them, else a timer in this process.
func (g *Gateway) setAlarm(key string, at time.Time) error {
	if g.shared.Alarm != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return g.shared.Alarm(ctx, key, at)
	}
	g.alarmsMu.Lock()
	select {
	case <-g.stop:
		g.alarmsMu.Unlock()
		return nil
	default:
	}
	alarm := g.alarms[key]
	if alarm == nil {
		alarm = newLocalAlarm(func() {
			if err := g.fire(key); err != nil {
				// No task to try again here: try again in a minute.
				log.Printf("wake %s: %v", key, err)
				_ = g.setAlarm(key, time.Now().Add(time.Minute))
			}
		})
		g.alarms[key] = alarm
	}
	g.alarmsMu.Unlock()
	return alarm.Set(at)
}

// errStopping is a wake that arrived as the gateway closes: it is to be
// tried again, by the next process.
var errStopping = errors.New("the gateway is closing")

// fire is a workspace's alarm going off: it is opened here if need be, and
// its due reaction queued. A workspace that is gone or being deleted has
// nothing to wake and is not an error. Anything else that stops the wake from
// being kept is: a wake that is lost is never set again, so whoever asked -
// a task, which is then tried again - must hear of it.
func (g *Gateway) fire(key string) error {
	select {
	case <-g.stop:
		return errStopping
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exists, err := g.shared.Exists(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		return nil // Deleted, or not set up: nothing to wake.
	}
	tenant, err := g.workspace(key)
	if errors.Is(err, errWorkspaceClosing) {
		return nil
	}
	if err != nil {
		return err
	}
	return tenant.runner.Due()
}

// serveDue is a task delivering a wake. The reaction it leads to is worked by
// whichever instance claims it, this one or another.
func (g *Gateway) serveDue(w http.ResponseWriter, r *http.Request) {
	if g.shared == nil || g.shared.Verify == nil {
		notFound(w, r)
		return
	}
	key, err := g.shared.Verify(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "invalid task credential")
		return
	}
	if raw, err := hex.DecodeString(key); err != nil || len(raw) != sha256.Size {
		fail(w, http.StatusBadRequest, "invalid workspace")
		return
	}
	if err := g.fire(key); err != nil {
		log.Printf("wake %s: %v", key, err)
		fail(w, http.StatusServiceUnavailable, "wake not kept; retry")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
