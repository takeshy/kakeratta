package persona

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// An ask's life, as kept in the workspace:
//
//	open (pending) --claim--> claimed --submit_answer (accepted)--> closed, answered
//	   |                         |------ release / answer timeout --> closed, fallback
//	   `---- claim timeout / connection changed / pause ----------> closed, fallback
//
// Every step is a transaction on the ask's record, so the agent may reach any
// process serving the workspace. A closed ask queues a reaction that carries
// its turn on (resumeAsk): with the agent's answer through the app's own
// phases, or with the built-in planner from the start.

// askRecord is one ask as the workspace keeps it.
type askRecord struct {
	Ask      Ask           `json:"ask"`
	Context  AskContext    `json:"context"`
	PlanWait time.Duration `json:"planWait"`
	Turn     askTurn       `json:"turn"`
	Research askResearch   `json:"research"`
	// Malformed counts the answers sent back.
	Malformed int `json:"malformed"`
	// Closed is set once the ask is over; Answer is the accepted answer, or
	// Fallback why the built-in planner takes the turn back.
	Closed   bool    `json:"closed"`
	Answer   *Answer `json:"answer,omitempty"`
	Fallback string  `json:"fallback,omitempty"`
}

// askTurn is what the turn was started with, to start it again from.
type askTurn struct {
	Persona      string          `json:"persona"`
	Trigger      timeline.Entry  `json:"trigger"`
	Place        *timeline.Place `json:"place,omitempty"`
	Named        bool            `json:"named"`
	Listening    bool            `json:"listening"`
	Instructions string          `json:"instructions"`
	Deep         bool            `json:"deep"`
}

// askResearch is what the agent's reads found, carried into the answer's
// phases as the in-process planner's own reads would be.
type askResearch struct {
	ReadsUsed   int                           `json:"readsUsed"`
	Lookups     lookupsState                  `json:"lookups"`
	Referenced  []string                      `json:"referenced,omitempty"`
	Read        []string                      `json:"read,omitempty"`
	ReadSources []timeline.Source             `json:"readSources,omitempty"`
	Cited       map[string]string             `json:"cited,omitempty"`
	Failures    map[string]string             `json:"failures,omitempty"`
	NotesRead   map[string][]store.NoteSource `json:"notesRead,omitempty"`
}

// askKeep is how long past its waits an ask record is kept, should nothing
// ever carry its turn on.
const askKeep = time.Hour

// presenceEvery is how often the agent's calling in is written down.
const presenceEvery = 30 * time.Second

func (r *Runner) readAsk(ctx context.Context, id string) (askRecord, bool, error) {
	var record askRecord
	if r.store == nil {
		return record, false, nil
	}
	raw, ok, err := r.store.Record(ctx, store.RecordAsks, id)
	if err != nil || !ok {
		return record, false, err
	}
	return record, true, json.Unmarshal(raw, &record)
}

// changeAsk changes one ask atomically. change returns false to leave it.
func (r *Runner) changeAsk(ctx context.Context, id string, expire time.Time, change func(*askRecord, bool) (bool, error)) (askRecord, error) {
	var record askRecord
	err := r.store.UpdateRecord(ctx, store.RecordAsks, id, expire, func(current []byte, exists bool) ([]byte, error) {
		record = askRecord{}
		if exists {
			if err := json.Unmarshal(current, &record); err != nil {
				return nil, err
			}
		}
		write, err := change(&record, exists)
		if err != nil {
			return nil, err
		}
		if !write {
			if !exists {
				return nil, nil
			}
			return current, nil
		}
		return json.Marshal(record)
	})
	return record, err
}

func (record askRecord) expiry() time.Time {
	return record.Ask.ClaimBy.Add(record.PlanWait).Add(askKeep)
}

func (r *Runner) openAsks(ctx context.Context) []askRecord {
	if r.store == nil {
		return nil
	}
	raws, err := r.store.Records(ctx, store.RecordAsks)
	if err != nil {
		r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
		return nil
	}
	var out []askRecord
	for _, raw := range raws {
		var record askRecord
		if json.Unmarshal(raw, &record) == nil {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Ask.At.Equal(out[j].Ask.At) {
			return out[i].Ask.At.Before(out[j].Ask.At)
		}
		return out[i].Ask.ID < out[j].Ask.ID
	})
	return out
}

// Asks lists the open asks, oldest first.
func (r *Runner) Asks() []Ask {
	out := []Ask{}
	for _, record := range r.openAsks(context.Background()) {
		if !record.Closed {
			out = append(out, record.Ask)
		}
	}
	return out
}

// openAsk keeps a new ask and ends the turn that asked it; the turn is
// carried on once the ask is over.
func (r *Runner) openAsk(ctx context.Context, settings store.Settings, ask Ask, brief AskContext, turn askTurn) error {
	claimWait, planWait := r.askWaits(settings)
	now := time.Now().UTC()
	ask.State, ask.At, ask.ClaimBy = "pending", now, now.Add(claimWait)
	record := askRecord{Ask: ask, Context: brief, PlanWait: planWait, Turn: turn}
	if _, err := r.changeAsk(ctx, ask.ID, record.expiry(), func(current *askRecord, _ bool) (bool, error) {
		*current = record
		return true, nil
	}); err != nil {
		return err
	}
	r.emit("ask", map[string]any{"persona": ask.Persona, "id": ask.ID, "state": "pending", "trigger": ask.Trigger})
	r.after(claimWait, ask.ID)
	return nil
}

// ClaimAsk takes an ask for one execution. The same claimID may claim again,
// after a lost response; another may not.
func (r *Runner) ClaimAsk(id, claimID string) (Ask, AskContext, error) {
	now := time.Now().UTC()
	first := false
	record, err := r.changeAsk(context.Background(), id, time.Time{}, func(record *askRecord, exists bool) (bool, error) {
		first = false
		switch {
		case !exists || record.Closed || (record.Ask.State == "pending" && now.After(record.Ask.ClaimBy)):
			return false, ErrAskNotFound
		case record.Ask.State == "pending":
			planBy := now.Add(record.PlanWait)
			record.Ask.State, record.Ask.ClaimID, record.Ask.ClaimedAt, record.Ask.PlanBy = "claimed", claimID, &now, &planBy
			first = true
			return true, nil
		case record.Ask.ClaimID != claimID:
			return false, ErrAskClaimed
		}
		return false, nil
	})
	if err != nil {
		return Ask{}, AskContext{}, err
	}
	if first {
		r.emit("ask", map[string]any{"persona": record.Ask.Persona, "id": id, "state": "claimed", "trigger": record.Ask.Trigger})
		r.after(record.PlanWait, id)
	}
	return record.Ask, record.Context, nil
}

func claimedBy(record askRecord, exists bool, claimID string) error {
	if !exists || record.Closed {
		return ErrAskNotFound
	}
	if record.Ask.State != "claimed" || record.Ask.ClaimID != claimID {
		return ErrAskNotYours
	}
	return nil
}

// ReadForAsk looks things up for a claimed ask with the persona's own tools
// and notes, the way the in-process planner's read does, and within the same
// limits. What it finds - saved posts in particular - may then be cited in
// the answer.
func (r *Runner) ReadForAsk(ctx context.Context, id, claimID string, steps []ReadStep) (string, error) {
	// One read at a time per ask: each builds on the budget the last left.
	release, err := r.store.Lock(ctx, "ask:"+id)
	if err != nil {
		return "", err
	}
	defer release()
	record, exists, err := r.readAsk(ctx, id)
	if err != nil {
		return "", err
	}
	if err := claimedBy(record, exists, claimID); err != nil {
		return "", err
	}
	turn, err := r.askTurnContext(ctx, record)
	if err != nil {
		return "", err
	}
	research := record.Research
	limit := turnReadLimit(record.Turn.Deep)
	if research.ReadsUsed >= limit {
		return readsRefused, nil
	}
	for _, step := range steps {
		if step.Type == "notes_read" || (step.Type == "tool" && step.Tool == "notes_read") {
			for _, path := range step.Paths {
				research.Referenced = append(research.Referenced, strings.TrimPrefix(path, "/"))
			}
		}
	}
	prov := newProvenance()
	for path, sources := range research.NotesRead {
		prov.read[path] = sources
	}
	if research.Failures == nil {
		research.Failures = map[string]string{}
	}
	if research.Cited == nil {
		research.Cited = map[string]string{}
	}
	budget := research.Lookups.restore(limit, turnSearchLimit(record.Turn.Deep))
	text, sources := r.readAll(ctx, turn.skill, steps, turn.set, turn.post, prov, research.Failures, budget, research.Cited)
	research.ReadSources = append(research.ReadSources, sources...)
	research.Read = append(research.Read, text)
	research.ReadsUsed++
	research.Lookups = budget.state()
	research.NotesRead = prov.read
	if _, err := r.changeAsk(ctx, id, record.expiry(), func(current *askRecord, exists bool) (bool, error) {
		if err := claimedBy(*current, exists, claimID); err != nil {
			return false, err
		}
		current.Research = research
		return true, nil
	}); err != nil {
		return "", err
	}
	return text + readFooter(limit-research.ReadsUsed, turn.set), nil
}

// SubmitAskAnswer hands a claimed ask's answer to the app, which checks it
// exactly as it would the in-process planner's. The verdict comes back at
// once: a rejected answer may be fixed and submitted again.
func (r *Runner) SubmitAskAnswer(ctx context.Context, id, claimID string, answer *Answer, parseErr error) (AskVerdict, error) {
	if parseErr == nil && answer != nil {
		// Some agents send escaped newlines inside the already decoded JSON text.
		// Normalize the submitted copy before planning, saving, and publishing it.
		copy := *answer
		copy.Text = strings.ReplaceAll(copy.Text, `\n`, "\n")
		answer = &copy
	}
	var verdict AskVerdict
	record, err := r.changeAsk(ctx, id, time.Time{}, func(record *askRecord, exists bool) (bool, error) {
		if err := claimedBy(*record, exists, claimID); err != nil {
			return false, err
		}
		var problems []string
		switch {
		case parseErr != nil:
			problems = []string{"Answer の形式が不正です: " + parseErr.Error()}
		case answer == nil:
			problems = []string{"Answer の形式が不正です"}
		default:
			problems = checkAnswer(*answer, turnRules{named: record.Turn.Named, listening: record.Turn.Listening, cited: record.Research.Cited})
		}
		if len(problems) == 0 {
			record.Closed, record.Answer = true, answer
			verdict = AskVerdict{Accepted: true, Closed: true, Note: "回答を受け付けました。本体が記憶・操作を判断し、必要な確認と実行後に公開します。"}
			return true, nil
		}
		record.Malformed++
		if record.Malformed > maxMalformed {
			record.Closed, record.Fallback = true, "drafts exhausted"
			verdict = AskVerdict{Problems: problems, Closed: true, Note: "差し戻しの上限に達したため、内蔵のプランナーに戻しました。"}
			return true, nil
		}
		verdict = AskVerdict{Problems: problems, Note: "この Answer は受け付けられませんでした。直して submit_answer を呼んでください。"}
		return true, nil
	})
	if err != nil {
		return AskVerdict{}, err
	}
	if record.Closed {
		r.carryOn(record)
	}
	return verdict, nil
}

// ReleaseAsk gives a claimed ask back: the in-process planner takes the turn
// at once instead of waiting out the deadline.
func (r *Runner) ReleaseAsk(ctx context.Context, id, claimID, reason string) error {
	if reason == "" {
		reason = "released"
	}
	record, err := r.changeAsk(ctx, id, time.Time{}, func(record *askRecord, exists bool) (bool, error) {
		if err := claimedBy(*record, exists, claimID); err != nil {
			return false, err
		}
		record.Closed, record.Fallback = true, "released: "+reason
		return true, nil
	})
	if err == nil {
		r.carryOn(record)
	}
	return err
}

// closeAsk ends an open ask for the built-in planner, for a reason.
func (r *Runner) closeAsk(ctx context.Context, id, reason string, due func(askRecord, time.Time) bool) {
	closed := false
	record, err := r.changeAsk(ctx, id, time.Time{}, func(record *askRecord, exists bool) (bool, error) {
		closed = false
		if !exists || record.Closed || (due != nil && !due(*record, time.Now())) {
			return false, nil
		}
		record.Closed, record.Fallback, closed = true, reason, true
		return true, nil
	})
	if err != nil {
		r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
		return
	}
	if closed {
		r.carryOn(record)
	}
}

func askDue(record askRecord, now time.Time) (string, bool) {
	switch {
	case record.Ask.State == "pending" && !now.Before(record.Ask.ClaimBy):
		return "claim timeout", true
	case record.Ask.State == "claimed" && record.Ask.PlanBy != nil && !now.Before(*record.Ask.PlanBy):
		return "answer timeout", true
	}
	return "", false
}

// sweepAsks ends the asks whose waits ran out, and carries on the turns of
// asks that were closed but whose turn nobody took up - their process having
// died in between.
func (r *Runner) sweepAsks(ctx context.Context, now time.Time) {
	for _, record := range r.openAsks(ctx) {
		if record.Closed {
			r.carryOn(record)
			continue
		}
		if reason, due := askDue(record, now); due {
			r.closeAsk(ctx, record.Ask.ID, reason, func(current askRecord, now time.Time) bool {
				_, due := askDue(current, now)
				return due
			})
		}
	}
}

// after ends an ask once a wait runs out, without waiting for the minute's
// sweep. Any process may do it; the sweep catches what a stopped one did not.
func (r *Runner) after(wait time.Duration, id string) {
	r.timersMu.Lock()
	defer r.timersMu.Unlock()
	var timer *time.Timer
	timer = time.AfterFunc(wait, func() {
		r.timersMu.Lock()
		_, live := r.timers[timer]
		delete(r.timers, timer)
		r.timersMu.Unlock()
		if !live {
			return
		}
		record, exists, err := r.readAsk(context.Background(), id)
		if err != nil || !exists || record.Closed {
			return
		}
		if reason, due := askDue(record, time.Now()); due {
			r.closeAsk(context.Background(), id, reason, func(current askRecord, now time.Time) bool {
				_, due := askDue(current, now)
				return due
			})
		}
	})
	r.timers[timer] = true
}

func (r *Runner) stopTimers() {
	r.timersMu.Lock()
	defer r.timersMu.Unlock()
	for timer := range r.timers {
		timer.Stop()
		delete(r.timers, timer)
	}
}

// carryOn queues the reaction that takes a closed ask's turn up again. Its
// ID is the ask's, so queueing it twice queues it once.
func (r *Runner) carryOn(record askRecord) {
	lane := "ask:" + record.Ask.ID
	if record.Turn.Trigger.ID != "" {
		lane = "thread:" + timeline.RootOf(record.Turn.Trigger)
	}
	r.accept(store.Reaction{ID: "ask-" + record.Ask.ID, Lane: lane, Ask: record.Ask.ID})
}

// resumeKey carries a closed ask into the turn it takes up again.
type resumeKey struct{}

func resumedFrom(ctx context.Context) *askRecord {
	record, _ := ctx.Value(resumeKey{}).(*askRecord)
	return record
}

// resumeAsk carries on the turn of a closed ask, and forgets the ask.
func (r *Runner) resumeAsk(ctx context.Context, id string) {
	record, exists, err := r.readAsk(ctx, id)
	if err != nil {
		r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
		return
	}
	if !exists || !record.Closed {
		return
	}
	forget := func() {
		if err := r.store.UpdateRecord(ctx, store.RecordAsks, id, time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil }); err != nil {
			r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
		}
	}
	turn, err := r.askTurnContext(ctx, record)
	if err != nil {
		// The post is gone or the persona is not active any more: there is
		// no turn to carry on.
		forget()
		r.FinishAsyncTurn(record.Turn.Trigger.ID, record.Turn.Persona, false)
		return
	}
	client, settings := r.clientOf()
	remark := r.run(context.WithValue(ctx, resumeKey{}, &record), turn.skill, turn.thread, turn.trigger,
		record.Turn.Named, record.Turn.Listening, record.Turn.Instructions, record.Turn.Deep, client, settings, r.registry(settings))
	forget()
	r.FinishAsyncTurn(record.Turn.Trigger.ID, record.Turn.Persona, remark != nil)
}

// askTurnView is the turn an ask was opened for, as it stands now.
type askTurnView struct {
	skill   Skill
	thread  []timeline.Entry
	trigger timeline.Entry
	post    timeline.Entry
	set     *tools.Set
}

var errTurnGone = errors.New("the turn's post or persona is gone")

func (r *Runner) askTurnContext(ctx context.Context, record askRecord) (askTurnView, error) {
	_, settings := r.clientOf()
	skills := r.catalogue.Active(settings.Active)
	index := slices.IndexFunc(skills, func(skill Skill) bool { return skill.ID == record.Turn.Persona })
	if index < 0 {
		return askTurnView{}, errTurnGone
	}
	skill := skills[index]
	trigger := record.Turn.Trigger
	thread := []timeline.Entry{trigger}
	if trigger.ID != "" {
		current, ok := r.store.Entry(trigger.ID)
		if !ok || current.Deleted {
			return askTurnView{}, errTurnGone
		}
		entries, err := store.SearchThread(ctx, r.store, current.ID)
		if err != nil {
			return askTurnView{}, errTurnGone
		}
		trigger, thread = current, timeline.Thread(entries, timeline.RootOf(current))
		if len(thread) == 0 {
			return askTurnView{}, errTurnGone
		}
	}
	if record.Turn.Place != nil {
		thread[0].Place = record.Turn.Place
	}
	set := r.registry(settings).For(skill.Tools).As(skill.ID)
	return askTurnView{skill: skill, thread: thread, trigger: trigger, post: thread[0], set: set}, nil
}

// AgentSeen notes that the external agent called in over MCP. Its Wake Gate
// calls every minute, so calling in is how the app knows it is there. The
// workspace keeps it for every process, written at most every presenceEvery.
func (r *Runner) AgentSeen(at time.Time) {
	r.agentSeen.Store(at.UnixNano())
	if r.store == nil {
		return
	}
	last := time.Unix(0, r.agentWritten.Load())
	if gap := at.Sub(last); gap < presenceEvery && gap > -presenceEvery {
		return
	}
	r.agentWritten.Store(at.UnixNano())
	raw, _ := json.Marshal(map[string]time.Time{"seen": at})
	if err := r.store.UpdateRecord(context.Background(), store.RecordPresence, "agent", time.Time{}, func([]byte, bool) ([]byte, error) { return raw, nil }); err != nil {
		r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
	}
}

// AgentLastSeen is when the external agent last called in, to this process
// or another, or the zero time if it has not.
func (r *Runner) AgentLastSeen() time.Time {
	var seen time.Time
	if local := r.agentSeen.Load(); local != 0 {
		seen = time.Unix(0, local)
	}
	if r.store == nil {
		return seen
	}
	raw, ok, err := r.store.Record(context.Background(), store.RecordPresence, "agent")
	if err != nil || !ok {
		return seen
	}
	var presence struct {
		Seen time.Time `json:"seen"`
	}
	if json.Unmarshal(raw, &presence) == nil && presence.Seen.After(seen) {
		seen = presence.Seen
	}
	return seen
}

// ResetExternalAgent prevents a newly selected connection from inheriting the
// old connection's presence or in-flight claims. Waiting turns fall back.
func (r *Runner) ResetExternalAgent() {
	r.agentSeen.Store(0)
	r.agentWritten.Store(0)
	if r.store == nil {
		return
	}
	ctx := context.Background()
	if err := r.store.UpdateRecord(ctx, store.RecordPresence, "agent", time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil }); err != nil {
		r.emit("error", map[string]any{"where": "ask", "message": err.Error()})
	}
	for _, record := range r.openAsks(ctx) {
		if !record.Closed {
			r.closeAsk(ctx, record.Ask.ID, "connection changed", nil)
		}
	}
}

// SetConnectionWaits gives the claim and plan waits of the connection in use.
// Each agent answers at its own pace, so the waits belong to its connection.
func (r *Runner) SetConnectionWaits(waits func() (time.Duration, time.Duration)) {
	r.connectionWaits = waits
}

// agentWorking reports whether the agent holds a claimed ask it has not finished.
func (r *Runner) agentWorking() bool {
	for _, record := range r.openAsks(context.Background()) {
		if !record.Closed && record.Ask.State == "claimed" {
			return true
		}
	}
	return false
}

// AgentAway reports whether the external agent has gone quiet for longer than
// the claim wait: silent that long, it would not claim an ask in time either.
// An agent away is set aside without anyone switching it off, and comes back
// by itself the moment it calls in again.
//
// An agent working on an ask it claimed is not away, however long since it
// last called: while it plans, its Wake Gate does not run, and the turn it is
// planning has its own deadline.
func (r *Runner) AgentAway(settings store.Settings) bool {
	if r.agentWorking() {
		return false
	}
	seen := r.AgentLastSeen()
	claim, _ := r.askWaits(settings)
	return seen.IsZero() || time.Since(seen) > claim
}

// plansExternally reports whether this turn's answer is asked of the
// external agent: the persona prefers it, it is not paused, it is calling in,
// and the turn is not one a person waits on or one carried on after an ask.
func (r *Runner) plansExternally(ctx context.Context, skill Skill, settings store.Settings) bool {
	if v, _ := ctx.Value(builtinOnlyKey{}).(bool); v || resumedFrom(ctx) != nil {
		return false
	}
	return settings.PlansExternally(skill.ID) && !r.AgentAway(settings)
}

// detaches reports whether a turn for this persona ends by asking the
// external agent, to be finished later: a caller keeping track of who is
// still thinking must not count it as done when run returns.
func (r *Runner) detaches(ctx context.Context, skill Skill, settings store.Settings) bool {
	return r.plansExternally(ctx, skill, settings)
}
