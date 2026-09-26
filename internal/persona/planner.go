package persona

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// A persona's turn has one part that is thinking: reading the context,
// deciding what to do, and writing that down as a workflow. That part - and
// only that part - can be handed to an external agent, such as Hermes, over
// MCP. The app still checks and reviews the workflow the agent hands in, and
// still runs it: the agent never writes a note or a remark itself.
//
// The hand-off is an ask. It waits for an agent to claim it, then for the
// claimed ask to come back as an approved workflow. Either wait running out -
// or the agent giving up - hands the turn back to the in-process planner, so
// a persona whose agent is asleep still answers, only later.
//
// Asks live in memory with the turn that is waiting on them. A restart ends
// the turn, and with it the ask: there is nothing left for a workflow to be
// handed to.

// Ask is one planning request, as an external agent sees it.
type Ask struct {
	ID        string     `json:"id"`
	Persona   string     `json:"persona"`
	Name      string     `json:"personaName"`
	Wake      string     `json:"wake"`
	Trigger   string     `json:"trigger,omitempty"`
	State     string     `json:"status"`
	ClaimID   string     `json:"claimId,omitempty"`
	At        time.Time  `json:"at"`
	ClaimBy   time.Time  `json:"claimBy"`
	ClaimedAt *time.Time `json:"claimedAt"`
	PlanBy    *time.Time `json:"planBy,omitempty"`
}

// AskContext is what the agent is given on claiming an ask: the same things
// the in-process planner reads, without duplicating them as full prompts.
type AskContext struct {
	Persona      AskPersona  `json:"persona"`
	Instructions string      `json:"instructions"`
	Rules        string      `json:"rules"`
	Place        string      `json:"place"`
	NoteCount    string      `json:"noteCount"`
	Turn         string      `json:"turn,omitempty"`
	Skill        string      `json:"skill"`
	Wake         string      `json:"wake"`
	WakeNote     string      `json:"wakeInstructions,omitempty"`
	Now          string      `json:"now"`
	Trigger      AskEntry    `json:"trigger"`
	Thread       []AskEntry  `json:"thread"`
	MemoryIndex  string      `json:"memoryIndex"`
	Schedules    string      `json:"schedules"`
	Workflow     AskWorkflow `json:"workflow"`
	Tools        []AskTool   `json:"tools"`
	Limits       AskLimits   `json:"limits"`
}

type AskPersona struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"`
	Tone   string `json:"tone"`
	Domain string `json:"domain"`
	Memory string `json:"memoryLayout"`
}

type AskEntry struct {
	ID       string `json:"id,omitempty"`
	Speaker  string `json:"speaker"`
	At       string `json:"at"`
	Text     string `json:"text"`
	Rendered string `json:"rendered"`
}

type AskWorkflow struct {
	Spec   string         `json:"spec"`
	Schema map[string]any `json:"schema"`
}

type AskTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Use is where the tool may appear: "read" (looked up through read_for_ask
	// before deciding) or "workflow" (a step the app runs afterwards).
	Use string `json:"use"`
}

type AskLimits struct {
	Reads     int `json:"reads"`
	Searches  int `json:"searches"`
	Steps     int `json:"steps"`
	Drafts    int `json:"drafts"`
	Malformed int `json:"malformed"`
}

// AskVerdict is the app's answer to a submitted workflow.
type AskVerdict struct {
	Accepted bool     `json:"accepted"`
	Problems []string `json:"problems,omitempty"`
	// Closed means the ask is over: accepted, or out of drafts and handed
	// back to the in-process planner.
	Closed bool   `json:"closed"`
	Note   string `json:"note,omitempty"`
}

var (
	ErrAskNotFound = errors.New("依頼が見つかりません。期限切れで内蔵のプランナーに戻ったか、既に終わっています")
	ErrAskClaimed  = errors.New("この依頼は別の claimId で取得されています")
	ErrAskNotYours = errors.New("claimId が一致しません。先に claim_ask で取得してください")
)

type askRequest struct {
	read     []ReadStep
	workflow *Workflow
	parseErr error
	release  string
	reply    chan askReply
}

type askReply struct {
	text    string
	verdict AskVerdict
	err     error
}

type openAsk struct {
	Ask
	context  AskContext
	planWait time.Duration
	claimed  chan struct{}
	requests chan askRequest
	// done is closed once the waiting turn stops listening.
	done   chan struct{}
	closed bool
}

type desk struct {
	mu   sync.Mutex
	asks map[string]*openAsk
	// order keeps asks listed oldest first.
	order []string
}

func newDesk() *desk { return &desk{asks: map[string]*openAsk{}} }

func (d *desk) open(a *openAsk) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asks[a.ID] = a
	d.order = append(d.order, a.ID)
}

func (d *desk) close(a *openAsk) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closeLocked(a)
}

func (d *desk) closeLocked(a *openAsk) {
	if a.closed {
		return
	}
	a.closed = true
	close(a.done)
	delete(d.asks, a.ID)
	for i, id := range d.order {
		if id == a.ID {
			d.order = append(d.order[:i:i], d.order[i+1:]...)
			break
		}
	}
}

// closeUnclaimed ends an ask whose claim wait ran out - unless an agent
// claimed it at the last moment, in which case the turn goes on waiting for
// its workflow instead.
func (d *desk) closeUnclaimed(a *openAsk) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a.State == "claimed" {
		return false
	}
	d.closeLocked(a)
	return true
}

// working reports whether an agent holds a claimed ask it has not finished.
func (d *desk) working() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.asks {
		if a.State == "claimed" {
			return true
		}
	}
	return false
}

func (d *desk) find(id string) (*openAsk, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.asks[id]
	return a, ok
}

// Asks lists the open asks, oldest first.
func (r *Runner) Asks() []Ask {
	r.desk.mu.Lock()
	defer r.desk.mu.Unlock()
	out := []Ask{}
	for _, id := range r.desk.order {
		out = append(out, r.desk.asks[id].Ask)
	}
	return out
}

// ClaimAsk takes an ask for one execution. The same claimID may claim again,
// after a lost response; another may not.
func (r *Runner) ClaimAsk(id, claimID string) (Ask, AskContext, error) {
	r.desk.mu.Lock()
	defer r.desk.mu.Unlock()
	a, ok := r.desk.asks[id]
	if !ok {
		return Ask{}, AskContext{}, ErrAskNotFound
	}
	switch {
	case a.State == "pending":
		now := time.Now().UTC()
		planBy := now.Add(a.planWait)
		a.State, a.ClaimID, a.ClaimedAt, a.PlanBy = "claimed", claimID, &now, &planBy
		close(a.claimed)
	case a.ClaimID != claimID:
		return Ask{}, AskContext{}, ErrAskClaimed
	}
	return a.Ask, a.context, nil
}

func (r *Runner) askFor(id, claimID string) (*openAsk, error) {
	a, ok := r.desk.find(id)
	if !ok {
		return nil, ErrAskNotFound
	}
	r.desk.mu.Lock()
	state, claim := a.State, a.ClaimID
	r.desk.mu.Unlock()
	if state != "claimed" || claim != claimID {
		return nil, ErrAskNotYours
	}
	return a, nil
}

func (r *Runner) sendAsk(ctx context.Context, id, claimID string, request askRequest) (askReply, error) {
	a, err := r.askFor(id, claimID)
	if err != nil {
		return askReply{}, err
	}
	request.reply = make(chan askReply, 1)
	select {
	case a.requests <- request:
	case <-a.done:
		return askReply{}, ErrAskNotFound
	case <-ctx.Done():
		return askReply{}, ctx.Err()
	}
	select {
	case reply := <-request.reply:
		return reply, reply.err
	case <-ctx.Done():
		return askReply{}, ctx.Err()
	}
}

// ReadForAsk looks things up for a claimed ask with the persona's own tools
// and notes, the way the in-process planner's read does, and within the same
// limits. What it finds - saved posts in particular - may then be cited by a
// speak step.
func (r *Runner) ReadForAsk(ctx context.Context, id, claimID string, steps []ReadStep) (string, error) {
	reply, err := r.sendAsk(ctx, id, claimID, askRequest{read: steps})
	return reply.text, err
}

// SubmitAskWorkflow hands a claimed ask's workflow to the app, which checks
// and reviews it exactly as it would the in-process planner's. The verdict
// comes back at once: rejected workflows may be fixed and submitted again.
func (r *Runner) SubmitAskWorkflow(ctx context.Context, id, claimID string, workflow *Workflow, parseErr error) (AskVerdict, error) {
	reply, err := r.sendAsk(ctx, id, claimID, askRequest{workflow: workflow, parseErr: parseErr})
	return reply.verdict, err
}

// ReleaseAsk gives a claimed ask back: the in-process planner takes the turn
// at once instead of waiting out the deadline.
func (r *Runner) ReleaseAsk(ctx context.Context, id, claimID, reason string) error {
	if reason == "" {
		reason = "released"
	}
	_, err := r.sendAsk(ctx, id, claimID, askRequest{release: reason})
	return err
}

// planning marks a context whose turn already runs apart from the queue, so
// run does not detach it again.
type planningKey struct{}

// builtinOnlyKey marks a turn that must be planned in-process: one a person
// is waiting on, such as a trial reply.
type builtinOnlyKey struct{}

func withBuiltinPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, builtinOnlyKey{}, true)
}

func plannedApart(ctx context.Context) bool {
	v, _ := ctx.Value(planningKey{}).(bool)
	return v
}

// AgentSeen notes that the external agent called in over MCP. Its Wake Gate
// calls every minute, so calling in is how the app knows it is there.
func (r *Runner) AgentSeen(at time.Time) {
	r.agentSeen.Store(at.UnixNano())
}

// AgentLastSeen is when the external agent last called in, or the zero time
// if it has not since the app started.
func (r *Runner) AgentLastSeen() time.Time {
	seen := r.agentSeen.Load()
	if seen == 0 {
		return time.Time{}
	}
	return time.Unix(0, seen)
}

// AgentAway reports whether the external agent has gone quiet for longer than
// the claim wait: silent that long, it would not claim an ask in time either. An agent away is set aside without anyone switching it
// off, and comes back by itself the moment it calls in again.
//
// An agent working on an ask it claimed is not away, however long since it
// last called: while it plans, its Wake Gate does not run, and the turn it is
// planning has its own deadline.
func (r *Runner) AgentAway(settings store.Settings) bool {
	if r.desk.working() {
		return false
	}
	seen := r.AgentLastSeen()
	claim, _ := r.askWaits(settings)
	return seen.IsZero() || time.Since(seen) > claim
}

// plansExternally reports whether this turn's planning goes to the external
// agent: the persona prefers it, it is not paused, and it is calling in.
func (r *Runner) plansExternally(ctx context.Context, skill Skill, settings store.Settings) bool {
	if v, _ := ctx.Value(builtinOnlyKey{}).(bool); v {
		return false
	}
	return settings.PlansExternally(skill.ID) && !r.AgentAway(settings)
}

// Detaches reports whether a turn for this persona runs apart from the queue,
// finishing later: a caller keeping track of who is still thinking must not
// count it as done when run returns.
func (r *Runner) detaches(ctx context.Context, skill Skill, settings store.Settings) bool {
	return r.plansExternally(ctx, skill, settings) && !plannedApart(ctx)
}

// detach runs an externally planned turn apart from the queue. Waiting for an
// agent can take many minutes, and the queue - every other post, check-in and
// schedule - must not wait with it. When the turn ends, the routing sheet is
// told, as for any turn that finished late.
func (r *Runner) detach(skill Skill, trigger timeline.Entry, settings store.Settings, turn func(context.Context) *timeline.Entry) {
	claim, plan := r.askWaits(settings)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), planningKey{}, true), claim+plan+10*time.Minute)
	r.detached.Add(1)
	go func() {
		defer r.detached.Done()
		defer cancel()
		var remark *timeline.Entry
		defer func() {
			if problem := recover(); problem != nil {
				r.emit("error", map[string]any{"persona": skill.ID, "message": fmt.Sprint(problem)})
			}
			if trigger.ID != "" {
				r.FinishAsyncTurn(trigger.ID, skill.ID, remark != nil)
			}
		}()
		remark = turn(ctx)
	}()
}

// askWaits are how long an ask waits to be claimed, and then to be planned.
func (r *Runner) askWaits(settings store.Settings) (time.Duration, time.Duration) {
	if r.waitsForAsk != nil {
		return r.waitsForAsk()
	}
	claim, plan := settings.ExternalAgent.ClaimWait(), settings.ExternalAgent.PlanWait()
	if claim <= 0 {
		claim = store.DefaultClaimMinutes * time.Minute
	}
	if plan <= 0 {
		plan = store.DefaultPlanMinutes * time.Minute
	}
	return claim, plan
}

// externalPlan is what the waiting turn does with an agent's requests.
type externalPlan struct {
	read     func(steps []ReadStep) string
	consider func(workflow *Workflow, parseErr error) (approved bool, problems []string, err error)
	// drafts reports whether another workflow may still be considered.
	drafts func() bool
}

// errAborted is a turn cut short by shutdown, not by the agent: nothing falls
// through, because nothing is going to run it.
var errAborted = errors.New("aborted")

// planExternally opens an ask and waits on it. It returns the approved
// workflow, or nil with the reason the turn falls through to the in-process
// planner.
func (r *Runner) planExternally(ctx context.Context, settings store.Settings, ask Ask, brief AskContext, plan externalPlan) (*Workflow, string, error) {
	claimWait, planWait := r.askWaits(settings)
	now := time.Now().UTC()
	ask.State, ask.At, ask.ClaimBy = "pending", now, now.Add(claimWait)
	a := &openAsk{Ask: ask, context: brief, planWait: planWait, claimed: make(chan struct{}), requests: make(chan askRequest), done: make(chan struct{})}
	r.desk.open(a)
	defer r.desk.close(a)
	r.emit("ask", map[string]any{"persona": ask.Persona, "id": ask.ID, "state": "pending", "trigger": ask.Trigger})

	claimTimer := time.NewTimer(claimWait)
	defer claimTimer.Stop()
	select {
	case <-a.claimed:
	case <-claimTimer.C:
		if r.desk.closeUnclaimed(a) {
			return nil, "claim timeout", nil
		}
	case <-ctx.Done():
		return nil, "", errAborted
	case <-r.apart.Done():
		return nil, "", errAborted
	}
	r.desk.mu.Lock()
	planBy := *a.PlanBy
	r.desk.mu.Unlock()
	r.emit("ask", map[string]any{"persona": ask.Persona, "id": ask.ID, "state": "claimed", "trigger": ask.Trigger})

	planTimer := time.NewTimer(time.Until(planBy))
	defer planTimer.Stop()
	for {
		select {
		case request := <-a.requests:
			switch {
			case request.release != "":
				request.reply <- askReply{}
				return nil, "released: " + request.release, nil
			case request.workflow != nil || request.parseErr != nil:
				approved, problems, err := plan.consider(request.workflow, request.parseErr)
				if err != nil {
					request.reply <- askReply{verdict: AskVerdict{Closed: true, Problems: []string{err.Error()}, Note: "確認に失敗したため、内蔵のプランナーに戻しました。"}}
					return nil, "review failed: " + err.Error(), nil
				}
				if approved {
					request.reply <- askReply{verdict: AskVerdict{Accepted: true, Closed: true, Note: "受け付けました。本体が実行します。"}}
					return request.workflow, "", nil
				}
				if !plan.drafts() {
					request.reply <- askReply{verdict: AskVerdict{Problems: problems, Closed: true, Note: "差し戻しの上限に達したため、内蔵のプランナーに戻しました。"}}
					return nil, "drafts exhausted", nil
				}
				request.reply <- askReply{verdict: AskVerdict{Problems: problems, Note: "この workflow は実行されませんでした。直して、もう一度 submit_workflow を呼んでください。"}}
			default:
				request.reply <- askReply{text: plan.read(request.read)}
			}
		case <-planTimer.C:
			return nil, "workflow timeout", nil
		case <-ctx.Done():
			return nil, "", errAborted
		case <-r.apart.Done():
			return nil, "", errAborted
		}
	}
}

// askTools lists what the persona holds, and where each may be used.
func askTools(set *tools.Set) []AskTool {
	shape := shapeOf(set)
	actions := map[string]bool{}
	for _, name := range shape.actions {
		actions[name] = true
	}
	out := []AskTool{
		{Name: "notes_list", Description: "自分のメモの一覧（新しい順）。args で {\"tag\":\"…\"} や {\"after\":\"…\"} を渡せる。", Use: "read"},
		{Name: "notes_read", Description: "自分のメモを paths で読む。", Use: "read"},
	}
	for _, declared := range set.Declarations() {
		use := "read"
		switch {
		case declared.Name == "schedule":
			use = "workflow"
			declared.Description += "（workflow の write_schedule / cancel_schedule で使う）"
		case actions[declared.Name]:
			use = "workflow"
		}
		out = append(out, AskTool{Name: declared.Name, Description: declared.Description, Parameters: declared.Parameters, Use: use})
	}
	return out
}

func askEntry(entry timeline.Entry, names map[string]string, here *time.Location) AskEntry {
	return AskEntry{ID: entry.ID, Speaker: entry.Speaker, At: entry.At.In(here).Format(time.RFC3339), Text: entry.Text, Rendered: entry.Render(names, here)}
}

// askContext gathers what the agent needs to plan this turn.
func (r *Runner) askContext(skill Skill, set *tools.Set, spec, rules, turn, wakeNote, wake string, thread []timeline.Entry, trigger timeline.Entry,
	indexNote, schedules string, names map[string]string, here *time.Location, deep bool) AskContext {
	entries := make([]AskEntry, 0, len(thread))
	for _, entry := range thread {
		entries = append(entries, askEntry(entry, names, here))
	}
	submit := workflowToolFor(set)
	return AskContext{
		Persona:      AskPersona{ID: skill.ID, Name: skill.Name, Icon: skill.Icon, Tone: skill.Tone, Domain: skill.Domain, Memory: skill.Memory},
		Instructions: skill.Instructions,
		Rules:        rules,
		Place:        placeLine(thread[0].Place),
		NoteCount:    r.noteCount(skill.ID),
		Turn:         turn,
		Skill:        okfHost + okfSkill,
		Wake:         wake,
		WakeNote:     wakeNote,
		Now:          time.Now().In(here).Format(time.RFC3339),
		Trigger:      askEntry(trigger, names, here),
		Thread:       entries,
		MemoryIndex:  indexNote,
		Schedules:    schedules,
		Workflow:     AskWorkflow{Spec: spec, Schema: submit.Parameters},
		Tools:        askTools(set),
		Limits: AskLimits{Reads: turnReadLimit(deep), Searches: turnSearchLimit(deep), Steps: maxSteps,
			Drafts: maxDrafts, Malformed: maxMalformed},
	}
}
