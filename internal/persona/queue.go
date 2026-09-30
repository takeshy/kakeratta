package persona

import (
	"context"
	"errors"
	"log"
	"slices"
	"strconv"
	"time"

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
	// claimTTL is how long a claim holds unless renewed; a worker renews it
	// every third of that while it works.
	claimTTL = 30 * time.Second
	// idlePoll is how often an idle runner looks for work it was not told of.
	idlePoll = time.Minute
	// maxReactionAttempts bounds how often a reaction is taken up. One that
	// brought its process down each time would otherwise do so forever.
	maxReactionAttempts = 3
)

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
	ctx, cancel := context.WithCancel(r.withSkills(context.Background()))
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(claimTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// A claim that is not ours any more - lapsed, or the
				// workspace is being deleted - ends the work.
				if kept, err := r.store.RenewReaction(ctx, item.ID, r.worker, claimTTL); err == nil && !kept {
					cancel()
					return
				}
			}
		}
	}()
	folded := []string{}
	finished := false
	defer func() {
		cancel()
		<-renewed
		// What was folded in goes first: while the claimed one stands, no one
		// else can take the rest of its lane.
		if !finished {
			folded = append(folded, item.ID)
		}
		for _, id := range folded {
			if err := r.store.FinishReaction(id); err != nil {
				r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
			}
		}
	}()
	if item.Attempts > maxReactionAttempts {
		r.emit("error", map[string]any{"where": "queue", "message": "反応を" + strconv.Itoa(maxReactionAttempts) + "回試みても終わらなかったため、取りやめました"})
		return
	}
fold:
	for _, next := range rest {
		switch {
		case item.Forgotten != nil && next.Forgotten != nil && next.Forgotten.Skill == item.Forgotten.Skill && next.Forgotten.Expired == item.Forgotten.Expired:
			item.Forgotten.Paths = append(item.Forgotten.Paths, next.Forgotten.Paths...)
		case item.Deleted != nil && next.Deleted != nil && next.Deleted.Skill == item.Deleted.Skill && next.Deleted.EntryID == item.Deleted.EntryID:
		default:
			break fold
		}
		folded = append(folded, next.ID)
	}
	now := time.Now()
	if !item.Due && !item.Index {
		// The work may have set a schedule, a retry, or an expires date; the
		// workspace looks at its times again soon.
		defer r.arm(time.Now().Add(soonDue))
	}
	switch {
	case item.Due:
		r.due(ctx, now)
	case item.Index:
		// Let go at once: what is saved while this pass runs asks for the
		// next one, rather than finding this one still waiting.
		if err := r.store.FinishReaction(item.ID); err != nil {
			r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		}
		finished = true
		r.indexPending(ctx)
	case item.Ask != "":
		r.resumeAsk(ctx, item.Ask)
	case item.Deliver != "":
		r.deliver(ctx, item.Entry, item.Deliver)
	case item.Forgotten != nil:
		r.forgottenAt(ctx, forgotten{skill: item.Forgotten.Skill, paths: item.Forgotten.Paths, expired: item.Forgotten.Expired})
	case item.Deleted != nil:
		d := item.Deleted
		r.deletedAt(ctx, deletedPost{entryID: d.EntryID, label: d.Label, skill: d.Skill, notes: d.Notes, removed: d.Removed})
	case item.Entry != "":
		r.reactEntry(ctx, item.Entry, item.Place, item.Edited)
	}
}

// accept keeps a reaction for whichever runner claims it first, and wakes
// this one.
func (r *Runner) accept(item store.Reaction) {
	if item.ID == "" {
		item.ID = store.NewID("r")
	}
	if item.At.IsZero() {
		item.At = time.Now()
	}
	var err error
	for attempt := 1; ; attempt++ {
		if item.ID == store.DueReaction || item.ID == store.IndexReaction || item.Ask != "" {
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
		return
	}
	r.Poke()
}

// ScheduleIndex queues bringing the search index up to date, unless that is
// waiting already.
func (r *Runner) ScheduleIndex() { r.scheduleIndex(time.Time{}) }

func (r *Runner) scheduleIndex(notBefore time.Time) {
	r.accept(store.Reaction{ID: store.IndexReaction, Lane: store.IndexReaction, Index: true, NotBefore: notBefore})
}

// indexPending brings the search index up to date. A failure is tried again
// half a minute later.
func (r *Runner) indexPending(ctx context.Context) {
	if r.search == nil {
		return
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := r.search.IndexPending(work); err != nil && ctx.Err() == nil {
		log.Printf("search indexing: %v", err)
		r.scheduleIndex(time.Now().Add(30 * time.Second))
	}
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
