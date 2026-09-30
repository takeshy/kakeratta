package persona

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// A workspace asks to be woken at the next time anything of its own comes
// due, and at no other: there is no clock going round every workspace. What
// comes due is asks past their waits, note retries, check-ins and the month's
// summary, schedules, and notes past their expires date. When the time comes
// a due reaction runs all of them, and ends by setting the alarm for the time
// after that.
//
// The alarm is Cloud Tasks in a deployment (a task named for the workspace
// and the minute, so setting it twice sets it once) and a timer in the
// process otherwise. Either way it does nothing but queue the due reaction.
const (
	// maxDue is the furthest an alarm is set. Cloud Tasks takes no more than
	// thirty days; a workspace with nothing sooner wakes again then, sets it
	// again, and a far-off birthday is reached in steps.
	maxDue = 29 * 24 * time.Hour
	// minDue keeps a due that could not be cleared from waking the workspace
	// in a loop.
	minDue = 30 * time.Second
	// soonDue is how long after a piece of work the workspace looks again:
	// the work may have set a schedule, a retry, or an expires date.
	soonDue = 30 * time.Second
	// retryDue is how long the next wake is put off when what it depends on
	// could not be read, or the wake before it gave up.
	retryDue = 5 * time.Minute
)

// armBackoff is the wait before the second try of setting an alarm, and twice
// it before the third. A variable so that tests can shorten it.
var armBackoff = time.Second

// SetAlarm gives the runner the way to wake its workspace at a time. Set
// before Start.
func (r *Runner) SetAlarm(set func(at time.Time) error) { r.alarm = set }

// Due queues the reaction that runs whatever has come due. It does nothing if
// one is waiting already, and returns the error if it could not be kept: a
// wake that is not kept is a wake lost, so whoever asked must be able to try
// again.
func (r *Runner) Due() error {
	return r.accept(store.Reaction{ID: store.DueReaction, Lane: store.DueReaction, Due: true})
}

// Rearm works out when the workspace next has something to do and sets the
// alarm for then. Whatever changes the times - settings, personas, a note
// edited by hand - calls it; it is set for now, and the due reaction it
// brings does the working out.
func (r *Runner) Rearm() { r.arm(time.Now()) }

// arm sets the alarm for at, or for as soon as allowed if at has passed.
func (r *Runner) arm(at time.Time) error {
	if r.alarm == nil {
		return nil
	}
	err := r.alarm(at)
	if err != nil {
		r.emit("error", map[string]any{"where": "due", "message": err.Error()})
	}
	return err
}

// armAgain sets the alarm, trying a few times: it is the only thing that
// wakes the workspace again.
func (r *Runner) armAgain(ctx context.Context, at time.Time) (err error) {
	for attempt := 1; attempt <= 3; attempt++ {
		if err = r.arm(at); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * armBackoff):
		}
	}
	return err
}

// due runs everything that has come due and sets the alarm for the next time.
// The alarm is set whatever went wrong before, since it is the only thing that
// wakes the workspace again; it returns an error only if that could not be.
func (r *Runner) due(ctx context.Context, now time.Time) (err error) {
	_, settings := r.clientOf()
	var expiry time.Time
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "due", "message": fmt.Sprint(problem)})
		}
		if ctx.Err() != nil {
			// The claim is lost: whoever has it now sets the next wake.
			err = ctx.Err()
			return
		}
		err = r.armAgain(ctx, r.nextDue(ctx, time.Now(), settings, expiry))
	}()
	r.sweepAsks(ctx, now)
	r.retryNoteJobs(ctx, now)
	expiry = r.expireNotes(ctx, now, settings)
	r.dailyAt(ctx, now)
	r.schedulesAt(ctx, now)
	return nil
}

// nextDue is the earliest time after now that anything of the workspace's
// comes due, at most maxDue away and at least minDue.
func (r *Runner) nextDue(ctx context.Context, now time.Time, settings store.Settings, expiry time.Time) time.Time {
	next := now.Add(maxDue)
	consider := func(at time.Time) {
		if !at.IsZero() && at.Before(next) {
			next = at
		}
	}
	// A time that could not be read may be sooner than any that could: the
	// next look is not put off further than retryDue.
	failed := false
	consider(expiry)
	if schedules, err := r.store.Schedules(""); err != nil {
		r.emit("error", map[string]any{"where": "due", "message": err.Error()})
		failed = true
	} else {
		for _, schedule := range schedules {
			consider(schedule.At)
		}
	}
	if jobs, err := r.store.NoteJobs(); err != nil {
		r.emit("error", map[string]any{"where": "due", "message": err.Error()})
		failed = true
	} else {
		for _, job := range jobs {
			consider(job.NextAttempt)
		}
	}
	asks, err := r.readAsks(ctx)
	if err != nil {
		r.emit("error", map[string]any{"where": "due", "message": err.Error()})
		failed = true
	}
	for _, record := range asks {
		switch {
		case record.Closed:
		case record.Ask.State == "claimed" && record.Ask.PlanBy != nil:
			consider(*record.Ask.PlanBy)
		default:
			consider(record.Ask.ClaimBy)
		}
	}
	consider(r.nextCheckIn(ctx, now, settings))
	if failed {
		consider(now.Add(retryDue))
	}
	if floor := now.Add(minDue); next.Before(floor) {
		next = floor
	}
	return next
}

// checkInSearch is how many days ahead the next check-in is looked for: a
// monthly one lands on its day of the month, which every month has.
const checkInSearch = 33

// nextCheckIn is the next time a check-in, or the month's summary, will run:
// the earliest of what dailyAt and summarizeAt would do after now.
func (r *Runner) nextCheckIn(ctx context.Context, now time.Time, settings store.Settings) time.Time {
	loc := settings.Location()
	local := now.In(loc)
	root := r.store.Root()
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	var next time.Time
	// first is the first day's time after now that falls inside the window.
	first := func(at func(day time.Time) (time.Time, bool)) {
		for d := 0; d <= checkInSearch; d++ {
			day := today.AddDate(0, 0, d)
			t, ok := at(day)
			if ok && t.After(now) && t.Before(store.CheckInCloses(day)) {
				if next.IsZero() || t.Before(next) {
					next = t
				}
				return
			}
		}
	}
	first(func(day time.Time) (time.Time, bool) {
		return store.CheckInAt(root, store.SummaryID, day), day.Day() == store.SummaryDay(root)
	})
	skills := r.skills(ctx).Active(settings.Active)
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	for _, skill := range skills {
		schedule := settings.CheckInFor(skill.ID)
		monthly := schedule.Every == store.CheckInMonthly && skill.IsPerson()
		first(func(day time.Time) (time.Time, bool) {
			at := store.CheckInAt(root, skill.ID, day)
			if monthly {
				// After the summary is written, on its day.
				if summary := store.CheckInAt(root, store.SummaryID, day); summary.After(at) {
					at = summary
				}
				return at, day.Day() == store.SummaryDay(root)
			}
			return at, !store.CheckInSkipped(root, skill.ID, day)
		})
	}
	return next
}
