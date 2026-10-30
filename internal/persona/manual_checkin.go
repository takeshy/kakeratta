package persona

import (
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// RequestCheckIn persists manual work in the same lane as automatic checks.
// Fixed IDs coalesce requests while queued/running. After completion, another
// explicit request can run again without changing automatic execution records.
func (r *Runner) RequestCheckIn(period string) error {
	if period != "daily" && period != "monthly" {
		return fmt.Errorf("invalid check-in period")
	}
	now := time.Now()
	return r.accept(store.Reaction{ID: "manual-" + period, Lane: store.DueReaction, At: now, CheckIn: period})
}

// manualStageID names a manual stage independently of automatic claims.
func manualStageID(period, id string) string { return "manual-" + period + ":" + id }

// manualStamp is a request's time as a claim key: fixed width, so a later
// request sorts after an earlier one and a retry of the same one matches.
func manualStamp(run time.Time) string { return run.UTC().Format("2006-01-02T15:04:05.000000000Z") }
