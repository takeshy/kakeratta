package persona

import (
	"context"
	"errors"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// SetQueuedOnly prevents direct routing on passive web/worker servers.
func (r *Runner) SetQueuedOnly() { r.queuedOnly = true }

// WorkJob executes one delivery inside its push request, without a dispatcher.
func (r *Runner) WorkJob(ctx context.Context, job store.Job) error {
	w, ok := r.store.(interface {
		ClaimJob(context.Context, store.Job, string, time.Duration) (store.Reaction, bool, error)
		JobFinished(context.Context, store.Job) (bool, error)
	})
	if !ok {
		return errors.New("push worker requires a dispatch workspace")
	}
	item, taken, err := w.ClaimJob(ctx, job, r.worker, claimTTL)
	if err != nil || !taken {
		return err
	}
	r.inFlight.Add(1)
	defer r.inFlight.Add(-1)
	r.handleAsContext(ctx, item, nil, r.worker)
	finished, err := w.JobFinished(ctx, job)
	if err != nil {
		return err
	}
	if !finished {
		return store.ErrJobBusy
	}
	return nil
}
