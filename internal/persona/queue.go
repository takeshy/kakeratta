package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The queue is kept in the workspace as store.Reaction records, in lanes: the
// replies of one conversation, the tidy-ups of one persona, the periodic
// checks, the search index. A lane is worked one reaction at a time, in
// order; different lanes run side by side, up to maxWorkers at once here, and
// on any other process serving the same workspace. Nothing here assumes this
// is the only one.
const (
	maxWorkers = 4
	// idlePoll is how often an idle runner looks for work it was not told of.
	idlePoll = time.Minute
	// maxReactionAttempts bounds how often a reaction is taken up. One that
	// brought its process down each time would otherwise do so forever.
	maxReactionAttempts = 3
	// interruptedDelay is how long a reaction whose work was cut short - its
	// claim was lost, or could not be renewed - is held back before it is
	// taken up again.
	interruptedDelay = 30 * time.Second
)

// claimTTL is how long a claim holds unless renewed; a worker renews it every
// third of that while it works. A variable so that tests can shorten it.
var claimTTL = 30 * time.Second

// Start begins taking up reactions. It is safe to call more than once.
func (r *Runner) Start() {
	if r.store == nil {
		return
	}
	r.once.Do(func() {
		r.stopMu.Lock()
		r.started = true
		r.stopMu.Unlock()
		go r.dispatch()
	})
}

// Poke tells the runner there may be work to claim: a reaction queued here or
// by another process, or a lane freed by one that finished.
func (r *Runner) Poke() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Stop works every reaction that can be claimed now - starting the runner if
// it was not - then returns. Tests use it to wait for what they queued.
func (r *Runner) Stop() { r.stop(false) }

// Halt finishes the reactions in hand and stops taking new ones. The rest stay
// kept for this process when it starts again, or for another one.
func (r *Runner) Halt() { r.stop(true) }

func (r *Runner) stop(halt bool) {
	r.stopMu.Lock()
	started := r.started
	r.stopping = true
	if halt {
		r.halting = true
	}
	r.stopMu.Unlock()
	if !started && !halt {
		r.Start()
		started = true
	}
	if started {
		r.Poke()
		<-r.done
	} else if halt {
		r.working.Wait()
	}
}

func (r *Runner) stopState() (stopping, halting bool) {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	return r.stopping, r.halting
}

// dispatch claims reactions and hands each to a worker.
func (r *Runner) dispatch() {
	defer close(r.done)
	defer r.working.Wait()
	poll := time.NewTimer(idlePoll)
	defer poll.Stop()
	for {
		stopping, halting := r.stopState()
		if halting {
			return
		}
		select {
		case r.slots <- struct{}{}:
		case <-r.nudge:
			continue
		}
		item, rest, ok, err := r.store.ClaimReaction(context.Background(), r.worker, claimTTL)
		if err != nil {
			r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		}
		if !ok {
			<-r.slots
			if stopping && r.inFlight.Load() == 0 {
				return
			}
			if !poll.Stop() {
				select {
				case <-poll.C:
				default:
				}
			}
			poll.Reset(idlePoll)
			select {
			case <-r.nudge:
			case <-poll.C:
			}
			continue
		}
		r.inFlight.Add(1)
		r.working.Add(1)
		go func() {
			defer r.working.Done()
			defer func() {
				<-r.slots
				r.inFlight.Add(-1)
				r.Poke()
			}()
			r.handle(item, rest)
		}()
	}
}

type skillsKey struct{}

// withSkills reads the persona definitions once for a piece of work and keeps
// them on its context; everything the work calls sees the same set.
func (r *Runner) withSkills(ctx context.Context) context.Context {
	if _, ok := ctx.Value(skillsKey{}).(*Skills); ok {
		return ctx
	}
	return context.WithValue(ctx, skillsKey{}, r.catalogue.Snapshot(ctx))
}

// skills is the persona definitions of the work ctx belongs to, or a fresh
// reading where the work took none.
func (r *Runner) skills(ctx context.Context) *Skills {
	if skills, ok := ctx.Value(skillsKey{}).(*Skills); ok {
		return skills
	}
	return r.catalogue.Snapshot(ctx)
}

// handle works one claimed reaction, renewing the claim meanwhile, and lets
// its records go. Reactions following it in its lane that ask the same thing
// - notes deleted one after another - are folded into it.
func (r *Runner) handle(item store.Reaction, rest []store.Reaction) {
	r.handleAs(item, rest, r.worker)
}

func (r *Runner) handleAs(item store.Reaction, rest []store.Reaction, worker string) {
	r.handleAsContext(context.Background(), item, rest, worker)
}

func (r *Runner) handleAsContext(base context.Context, item store.Reaction, rest []store.Reaction, worker string) {
	base = llm.WithTrace(base, llm.Trace{Job: item.ID, JobAttempt: item.Attempts})
	if item.Delivery != nil {
		var definitions []Skill
		if err := json.Unmarshal([]byte(item.Delivery.SkillsJSON), &definitions); err != nil {
			r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
			return // Keep corrupt payloads for inspection rather than rerouting.
		}
		base = context.WithValue(base, skillsKey{}, &Skills{all: definitions})
	} else {
		base = r.withSkills(base)
	}
	ctx, cancel := context.WithCancel(base)
	renewed := make(chan struct{})
	// The work ends if the claim has not been renewed for most of its lease,
	// on a clock of its own: a renewal that hangs must not let the work run on
	// after another worker may have taken the reaction over.
	safe := claimTTL * 2 / 3
	watchdog := time.AfterFunc(safe, cancel)
	go func() {
		defer close(renewed)
		defer watchdog.Stop()
		ticker := time.NewTicker(claimTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// A claim that is not ours any more - lapsed, or the
				// workspace is being deleted - ends the work. One renewal
				// may take no longer than the interval.
				renewing, stop := context.WithTimeout(ctx, claimTTL/3)
				kept, err := r.store.RenewReaction(renewing, item.ID, worker, claimTTL)
				stop()
				switch {
				case err == nil && kept:
					watchdog.Reset(safe)
				case err == nil:
					cancel()
					return
				}
			}
		}
	}()
	folded := []string{}
	// finished: already let go. keep: leave it kept for a later attempt.
	finished, keep := false, false
	defer func() {
		// The context ends only when the claim is lost or could not be
		// renewed, before this: the work was cut short, not done.
		interrupted := ctx.Err() != nil
		cancel()
		<-renewed
		if finished || keep {
			return
		}
		if interrupted {
			// Not finished, whatever the claim says: the work did not get to
			// the end, and what it was for - an answer, a tidy-up, what was
			// folded into it - would be lost. It is held back briefly with
			// its attempts spent afresh; if that cannot be stored either, the
			// claim runs out and it is taken up again all the same.
			if _, err := r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay)); err != nil {
				r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
			}
			return
		}
		// Only the worker whose claim it still is may let it go, with what
		// was folded into it: if the claim lapsed and another took it over,
		// that one's work is not ours to delete.
		if _, err := r.store.FinishReaction(context.Background(), item.ID, worker, folded...); err != nil {
			r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		}
	}()
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "react", "message": fmt.Sprint(problem)})
			cancel() // Recovery must retain work interrupted by a panic.
		}
	}()
	if item.Attempts > maxReactionAttempts {
		r.emit("error", map[string]any{"where": "queue", "message": "反応を" + strconv.Itoa(maxReactionAttempts) + "回試みても終わらなかったため、取りやめました"})
		if item.Due {
			// Giving it up must not end the wakes. It goes only once the next
			// one is set; until then it is kept, put off for a while and with
			// its attempts spent afresh, so that the workspace is woken again
			// when the queue is back, without anything having to remember it.
			if err := r.armAgain(ctx, time.Now().Add(retryDue)); err != nil {
				if _, deferErr := r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(retryDue)); deferErr != nil {
					r.emit("error", map[string]any{"where": "queue", "message": deferErr.Error()})
				}
				keep = true
			}
		}
		return
	}
fold:
	for _, next := range rest {
		switch {
		case item.Forgotten != nil && next.Forgotten != nil:
		case item.Deleted != nil && next.Deleted != nil && next.Deleted.Skill == item.Deleted.Skill && next.Deleted.EntryID == item.Deleted.EntryID:
		default:
			break fold
		}
		folded = append(folded, next.ID)
	}
	now := time.Now()
	if !item.Due && !item.Index && !item.Route && !item.Locate {
		// The work may have set a schedule or a retry; the
		// workspace looks at its times again soon.
		defer r.arm(time.Now().Add(soonDue))
	}
	switch {
	case item.Due:
		if err := r.due(ctx, now); err != nil {
			// The next wake could not be set. Not finished: the claim lapses
			// and the reaction is tried again, rather than the workspace
			// going quiet.
			r.emit("error", map[string]any{"where": "due", "message": err.Error()})
			keep = true
		}
	case item.Index:
		if err := r.indexPending(ctx); err != nil {
			log.Printf("search indexing: %v", err)
			_, _ = r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay))
			keep = true
		}
	case item.Locate:
		if r.placeLookup != nil {
			if err := r.placeLookup(ctx, item.Entry); err != nil {
				log.Printf("place lookup: %v", err)
				if item.Attempts < maxReactionAttempts {
					_, _ = r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay))
					keep = true
					r.arm(time.Now().Add(interruptedDelay))
				}
			}
		}
	case item.Ask != "":
		r.resumeAsk(ctx, item.Ask)
	case item.Deliver != "":
		r.deliver(ctx, item.Entry, item.Deliver)
	case item.Forgotten != nil:
		// Queued before memory was kept in fields: a note the user or its
		// expires date took away needs nobody woken any more.
	case item.Deleted != nil:
		d := item.Deleted
		r.deletedAt(ctx, deletedPost{entryID: d.EntryID, label: d.Label, skill: d.Skill})
	case item.Route:
		entry, exists := r.store.Entry(item.Entry)
		if !exists || entry.Deleted || (item.EntryText != nil && entry.Text != *item.EntryText) {
			return
		}
		if r.placeLookup != nil && entry.Kind == timeline.KindPost && entry.Place != nil && entry.Place.Label == "" {
			id := "place-" + entry.ID
			if err := r.accept(store.Reaction{ID: id, Lane: id, Entry: entry.ID, Locate: true}); err != nil {
				_, _ = r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay))
				keep = true
				return
			}
		}
		plan, record, err := r.prepareEntry(ctx, item.Entry, item.Place, item.Edited)
		if err != nil {
			r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
			_, _ = r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay))
			keep = true
			return
		}
		if plan == nil {
			return
		}
		entry, exists = r.store.Entry(item.Entry)
		if !exists || entry.Deleted || entry.Text != plan.Text {
			return
		}
		prepared, err := r.store.PrepareReaction(ctx, item.ID, worker, *plan, record)
		if err != nil || !prepared {
			if err != nil {
				r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
			}
			_, _ = r.store.DeferReaction(context.Background(), item.ID, worker, time.Now().Add(interruptedDelay))
			keep = true
			return
		}
		finished = true // The durable record now belongs to the delivery phase.
		r.emit("routed", map[string]any{"entryId": item.Entry, "deliver": plan.Targets})
		r.Poke()
	case item.Delivery != nil:
		if err := r.deliverEntry(ctx, item.Entry, item.Place, *item.Delivery, item.Edited); err != nil {
			r.emit("error", map[string]any{"where": "delivery", "message": err.Error()})
			cancel() // Leave a failed delivery available for another attempt.
		}
	case item.Entry != "":
		r.reactEntry(ctx, item.Entry, item.Place, item.Edited)
	}
}

// accept keeps a reaction for whichever runner claims it first, and wakes
// this one. It returns the error if the reaction could not be kept.
func (r *Runner) accept(item store.Reaction) error {
	if item.ID == "" {
		item.ID = store.NewID("r")
	}
	if item.At.IsZero() {
		item.At = time.Now()
	}
	var err error
	for attempt := 1; ; attempt++ {
		if item.ID == store.DueReaction || item.ID == store.IndexReaction || item.Ask != "" || item.Locate {
			// At most one waits at a time; one already waiting covers this.
			if err = r.store.CreateReaction(item); errors.Is(err, store.ErrReactionExists) {
				err = nil
			}
		} else {
			err = r.store.SaveReaction(item)
		}
		if err == nil || attempt == 3 || !transient(err) {
			break
		}
		// Contention on the queue passes; the work must not be lost to it.
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	if err != nil {
		// A reaction that could not be kept is work nobody will do.
		log.Printf("queue %s: %v", item.Lane, err)
		r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		return err
	}
	r.Poke()
	return nil
}

// ScheduleIndex queues bringing the search index up to date, unless that is
// waiting already.
func (r *Runner) ScheduleIndex() { r.scheduleIndex(time.Time{}) }

func (r *Runner) scheduleIndex(notBefore time.Time) {
	r.accept(store.Reaction{ID: store.IndexReaction, Lane: store.IndexReaction, Index: true, NotBefore: notBefore})
}

// indexPending brings the search index up to date. A failure is tried again
// half a minute later.
func (r *Runner) indexPending(ctx context.Context) error {
	if r.search == nil {
		return nil
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return r.search.IndexPending(work)
}

// deliver gives a post to one persona a person approved in a routing check.
func (r *Runner) deliver(ctx context.Context, entryID, personaID string) {
	entry, ok := r.store.Entry(entryID)
	if !ok || entry.Deleted {
		return
	}
	client, settings := r.clientOf()
	skills := r.skills(ctx).Active(settings.Active)
	index := slices.IndexFunc(skills, func(skill Skill) bool { return skill.ID == personaID })
	if index < 0 {
		return
	}
	skill := skills[index]
	entries, err := store.SearchThread(ctx, r.store, entry.ID)
	if err != nil {
		return
	}
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	pending := r.detaches(ctx, skill, settings)
	remark := r.run(ctx, skill, thread, entry, contains(Named(skills, entry.Text), skill.ID), false, "", false, client, settings, r.registry(settings))
	// A turn whose answer was asked of the external agent is marked done
	// when it is carried on.
	if !pending {
		r.turnsDone(entry.ID, []Skill{skill}, map[string]bool{skill.ID: remark != nil})
	}
}

// Idle reports whether the runner has no reaction in hand.
func (r *Runner) Idle() bool { return r.inFlight.Load() == 0 }

// transient is an error worth trying again: contention or a moment's outage.
func transient(err error) bool {
	switch status.Code(err) {
	case codes.Aborted, codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	}
	return false
}
