package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/takeshy/kakeratta/internal/capture"
	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Runner wakes personas. One runs per workspace in each process serving it.
//
// Reactions are asynchronous by default: a post is stored and shown
// immediately, and whatever anyone has to say about it arrives afterwards.
// The work is kept in the workspace as store.Reaction records and taken up
// by whichever runner claims it first - this one, or one in another process
// serving the same workspace (see queue.go). A runner holds nothing another
// one would need: asks waiting on the external agent, trials and locks are
// kept in the workspace too.
type Runner struct {
	store       store.Workspace
	catalogue   *Catalogue
	clientOf    func() (*llm.Client, store.Settings)
	search      *search.Service
	placeLookup func(context.Context, string) error
	tools       tools.Config
	emit        func(name string, data any)

	// worker names this runner's claims; see queue.go for the rest.
	worker     string
	nudge      chan struct{}
	slots      chan struct{}
	done       chan struct{}
	once       sync.Once
	stopMu     sync.Mutex
	started    bool
	stopping   bool
	halting    bool
	inFlight   atomic.Int32
	working    sync.WaitGroup
	queuedOnly bool

	// alarm wakes the workspace at a time: it makes sure a due reaction is
	// queued then, through Cloud Tasks or a timer in this process.
	alarm func(at time.Time) error

	// agentSeen is when the external agent last called in over MCP, as Unix
	// nanoseconds, as this process heard it; the workspace keeps it for the
	// others, written at most every presenceEvery.
	agentSeen    atomic.Int64
	agentWritten atomic.Int64
	// waitsForAsk overrides the settings' claim and plan waits, for tests.
	waitsForAsk func() (time.Duration, time.Duration)
	// connectionWaits are the waits of the connection in use; a zero one
	// falls back to the settings.
	connectionWaits func() (time.Duration, time.Duration)
}

func New(s store.Workspace, catalogue *Catalogue, clientOf func() (*llm.Client, store.Settings)) *Runner {
	return &Runner{
		store: s, catalogue: catalogue, clientOf: clientOf,
		search: search.New(s),
		emit:   func(string, any) {},
		worker: store.NewID("w"),
		nudge:  make(chan struct{}, 1),
		slots:  make(chan struct{}, maxWorkers),
		done:   make(chan struct{}),
	}
}

// SetSearch shares the UI's indexing work with all personas. Set before Start.
func (r *Runner) SetSearch(service *search.Service) { r.search = service }

// SetPlaceLookup installs background location enrichment before Start.
func (r *Runner) SetPlaceLookup(lookup func(context.Context, string) error) { r.placeLookup = lookup }

// SetTools is the operator's tool configuration: search key, map endpoints and
// MCP servers. Set before Start.
func (r *Runner) SetTools(config tools.Config) { r.tools = config }

// OnEvent is how the screen hears that something landed.
func (r *Runner) OnEvent(emit func(name string, data any)) {
	if emit != nil {
		r.emit = emit
	}
}

// React durably accepts an entry and starts routing directly. Routing and persona turns
// remain asynchronous and ordered within the conversation.
func (r *Runner) React(entryID string, place *timeline.Place) {
	r.enqueueEntry(entryID, place, false)
}

// EntryEdited notifies previous recipients and routes the current content anew.
func (r *Runner) EntryEdited(entryID string) {
	r.enqueueEntry(entryID, nil, true)
}

func (r *Runner) enqueueEntry(entryID string, place *timeline.Place, edited bool) {
	item := store.Reaction{ID: store.NewID("r"), Lane: r.threadLane(entryID), Entry: entryID, Edited: edited, Place: place, Route: true}
	if entry, ok := r.store.Entry(entryID); ok {
		item.EntryText = &entry.Text
	}
	if err := r.accept(item); err != nil {
		return
	}
	r.RouteAccepted(item.ID)
}

// RouteAccepted starts a routing request already persisted with an entry.
// A stopped runner leaves it for recovery instead of creating another request.
func (r *Runner) RouteAccepted(id string) {
	r.Poke()
	if r.queuedOnly {
		return
	}
	// Routing runs directly, outside the delivery slots. The durable claim
	// still makes recovery and competing instances use exactly this phase.
	r.stopMu.Lock()
	if r.stopping || r.halting {
		r.stopMu.Unlock()
		return
	}
	r.working.Add(1)
	r.inFlight.Add(1)
	r.stopMu.Unlock()
	go func() {
		defer r.working.Done()
		defer func() { r.inFlight.Add(-1); r.Poke() }()
		claimed, ok, err := r.store.ClaimRoutingReaction(context.Background(), id, store.NewID("route"), claimTTL)
		if err != nil {
			r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
			return
		}
		if ok {
			r.handleAs(claimed, nil, claimed.ClaimedBy)
		}
	}()
}

// threadLane is the lane of an entry's conversation: its replies and edits
// are reacted to one at a time, in order.
func (r *Runner) threadLane(entryID string) string {
	if entry, ok := r.store.Entry(entryID); ok {
		return "thread:" + timeline.RootOf(entry)
	}
	return "thread:" + entryID
}

func (r *Runner) react(entryID string) {
	r.reactAt(entryID, nil)
}

func (r *Runner) reactAt(entryID string, transientPlace *timeline.Place) {
	r.reactEntry(context.Background(), entryID, transientPlace, false)
}

// keepRouting writes down who a post went to, for a person to look at later:
// the personas the router chose, the ones the user called by @ or by replying
// to them (with how), and the ones the router passed over. Anyone who got the
// post for another reason is left out of the passed-over. Nothing is kept when
// the router had no answer and nobody was called - "no record" and "nobody"
// are different things to read.
func routingRecord(entry timeline.Entry, routing Routing, called map[string]string, targets map[string]bool) *store.Routing {
	if !routing.Answered && len(called) == 0 {
		return nil
	}
	record := store.Routing{Delivered: []string{}, Excluded: map[string]string{}, EntryText: &entry.Text, Tracked: true, Done: []string{}, Spoke: []string{}}
	for _, id := range routing.Deliver {
		if _, ok := called[id]; !ok {
			record.Delivered = append(record.Delivered, id)
			if term, ok := routing.Heard[id]; ok {
				if record.Heard == nil {
					record.Heard = map[string]string{}
				}
				record.Heard[id] = term
			}
		}
	}
	ids := make([]string, 0, len(called))
	for id := range called {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if record.Called == nil {
			record.Called = map[string]string{}
		}
		record.Called[id] = called[id]
		record.Delivered = append(record.Delivered, id)
	}
	for id, reason := range routing.Excluded {
		if !targets[id] {
			record.Excluded[id] = reason
		}
	}
	return &record
}

// turnsDone notes on the post's routing record that these personas have had
// their turn - spoken or not - and tells the page, so a sheet showing the
// router's call can stop saying the persona is still thinking.
func (r *Runner) turnsDone(entryID string, wave []Skill, spoke map[string]bool) {
	var ids []string
	for _, skill := range wave {
		ids = append(ids, skill.ID)
	}
	err := r.store.UpdateRouting(entryID, func(record *store.Routing) (*store.Routing, error) {
		if record == nil {
			return nil, nil
		}
		for _, skill := range wave {
			markDone(record, skill.ID, spoke[skill.ID])
		}
		return record, nil
	})
	if err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
	r.emit("turn", map[string]any{"entryId": entryID, "personas": ids})
}

// markDone notes on a routing record that a persona has had its turn, and
// whether it spoke.
func markDone(record *store.Routing, personaID string, spoke bool) {
	if record.Done == nil {
		record.Done = []string{}
	}
	if record.Spoke == nil {
		record.Spoke = []string{}
	}
	if !contains(record.Done, personaID) {
		record.Done = append(record.Done, personaID)
	}
	if spoke && contains(record.Delivered, personaID) && !contains(record.Spoke, personaID) {
		record.Spoke = append(record.Spoke, personaID)
	}
}

// markUnrelated notes on the post's routing record that this persona stayed
// quiet because the post was not its business.
func (r *Runner) markUnrelated(entryID, personaID string) {
	err := r.store.UpdateRouting(entryID, func(record *store.Routing) (*store.Routing, error) {
		if record == nil || contains(record.Unrelated, personaID) {
			return nil, nil
		}
		record.Unrelated = append(record.Unrelated, personaID)
		return record, nil
	})
	if err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
}

// FinishAsyncTurn is turnsDone for a persona whose turn was left pending
// because its answer was asked of the external agent. Called once that turn
// has actually concluded, so a routing sheet that has been saying "still
// thinking" can move on to what really happened.
func (r *Runner) FinishAsyncTurn(entryID, personaID string, spoke bool) {
	if entryID == "" {
		return
	}
	err := r.store.UpdateRouting(entryID, func(record *store.Routing) (*store.Routing, error) {
		if record == nil {
			return nil, nil
		}
		markDone(record, personaID, spoke)
		return record, nil
	})
	if err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
	r.emit("turn", map[string]any{"entryId": entryID, "personas": []string{personaID}})
}

// RoutingCheck is a trial run of the router for one persona: whether the post
// would reach it now, and if not, what the router said.
type RoutingCheck struct {
	Answered  bool   `json:"answered"`
	Delivered bool   `json:"delivered"`
	Reason    string `json:"reason,omitempty"`
	// Heard is the name or keyword a person would be reached by.
	Heard string `json:"heard,omitempty"`
	// Words are all of a person's names and keywords in the post, so one it
	// should not have reached shows every word there is to remove.
	Words []string `json:"words,omitempty"`
	Error string   `json:"error,omitempty"`
	Token string   `json:"token,omitempty"`
}

func routeConfigured(ctx context.Context, client *llm.Client, settings store.Settings, skills []Skill, thread []Entryish, trigger Entryish) Routing {
	if jevConfigured(settings) {
		return RouteJev(ctx, openRouterDecisionsURL, settings.OpenRouterAPIKey, skills, thread, trigger)
	}
	model := strings.TrimSpace(settings.RouterModel)
	if model == "" {
		model = routingModel()
	}
	return Route(ctx, client, model, skills, thread, trigger)
}

// routeSubjects is the router's call on the subjects, with the people
// decided beside it by their names and keywords. A person heard in the post is
// delivered as if routed, so the routing sheet shows it and the word it was
// heard by; one not heard is passed over for that reason.
func routeSubjects(ctx context.Context, client *llm.Client, settings store.Settings, skills []Skill, thread []Entryish, trigger Entryish) Routing {
	subjects := Subjects(skills)
	people := People(skills)
	routing := Routing{Answered: true}
	if len(subjects) > 0 {
		routing = routeConfigured(ctx, client, settings, subjects, thread, trigger)
	}
	if len(people) == 0 {
		return routing
	}
	if routing.Excluded == nil {
		routing.Excluded = map[string]string{}
	}
	routing.Heard = Heard(people, search.Text(trigger))
	for _, skill := range people {
		if _, ok := routing.Heard[skill.ID]; ok {
			routing.Deliver = append(routing.Deliver, skill.ID)
			continue
		}
		routing.Excluded[skill.ID] = "呼び名・キーワードが投稿にありませんでした"
	}
	// The people are decided even when the router could not be: a post about
	// someone is not lost because the subjects' model was down. The record of
	// it still needs the router's answer, as before.
	return routing
}

func jevConfigured(settings store.Settings) bool {
	return settings.OpenRouterMode && strings.TrimSpace(settings.OpenRouterRouterModel) == jevModel && strings.TrimSpace(settings.OpenRouterAPIKey) != ""
}

// CheckRouting routes a post again without delivering it, so a person who has
// just changed a persona's conditions can see whether the change did what
// they meant. It reads the conditions as they are now saved; nothing is woken
// and nothing is written down.
func (r *Runner) CheckRouting(ctx context.Context, entryID, skillID string) (RoutingCheck, error) {
	ctx = r.withSkills(ctx)
	entry, ok := r.store.Entry(entryID)
	if !ok || entry.Deleted {
		return RoutingCheck{}, errors.New("投稿が見つかりません")
	}
	client, settings := r.clientOf()
	skills := r.skills(ctx).Active(settings.Active)
	found := false
	var words []string
	for _, skill := range skills {
		if skill.ID == skillID {
			found = true
			// A person is decided by its own words; the router has no say.
			if skill.IsPerson() {
				skills = []Skill{skill}
				words = HeardTerms(skill, search.Text(entry))
			}
			break
		}
	}
	if !found {
		return RoutingCheck{}, errors.New("その担当は有効ではありません")
	}
	entries, err := store.SearchThread(ctx, r.store, entry.ID)
	if err != nil {
		return RoutingCheck{}, err
	}
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	routing := routeSubjects(ctx, client, settings, skills, thread, entry)
	check := RoutingCheck{Answered: routing.Answered, Error: routing.Error, Words: words}
	if routing.Answered {
		check.Delivered = contains(routing.Deliver, skillID)
		check.Reason = routing.Excluded[skillID]
		check.Heard = routing.Heard[skillID]
		check.Token = store.NewID("trial")
		if err := r.saveTrial(ctx, check.Token, trialRecord{Routing: &routingTrial{
			EntryID: entryID, PersonaID: skillID, Text: entry.Text, Delivered: check.Delivered, Reason: check.Reason, Heard: check.Heard,
		}}); err != nil {
			return RoutingCheck{}, err
		}
	}
	return check, nil
}

const editedInstructions = "\n\n以前あなたに渡した投稿が編集されました。今回の対象発言は編集後の内容です。以前の内容より優先し、必要に応じて回答・メモ・予定を更新してください。担当領域の内容が削除された場合も、その変更を反映してください。"

// reactEntry is also used by direct test/legacy paths. Durable reactions split
// these two phases so routing never waits behind a previous persona turn.
func (r *Runner) reactEntry(outer context.Context, entryID string, transientPlace *timeline.Place, edited bool) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "react", "message": fmt.Sprint(problem)})
		}
	}()
	plan, record, err := r.prepareEntry(outer, entryID, transientPlace, edited)
	if err != nil || plan == nil {
		return
	}
	if record == nil {
		err = r.store.ForgetRouting(entryID)
	} else {
		err = r.store.SaveRouting(entryID, *record)
	}
	if err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
		return
	}
	r.emit("routed", map[string]any{"entryId": entryID, "deliver": plan.Targets})
	if err := r.deliverEntry(outer, entryID, transientPlace, *plan, edited); err != nil {
		r.emit("error", map[string]any{"where": "delivery", "message": err.Error()})
	}
}

func (r *Runner) prepareEntry(outer context.Context, entryID string, transientPlace *timeline.Place, edited bool) (*store.EntryDelivery, *store.Routing, error) {
	entry, exists := r.store.Entry(entryID)
	if !exists || entry.Deleted {
		return nil, nil, nil
	}
	client, settings := r.clientOf()
	root := timeline.RootOf(entry)

	// The picture is read once, here, before anyone is woken. Everyone gets the
	// same text out of it.
	if entry.Kind == timeline.KindPost && entry.Media != nil && !entry.Media.Ready() {
		entry = r.readCapture(outer, entry, client, settings)
		if entry.Deleted {
			return nil, nil, nil
		}
	}

	skills := r.skills(outer).Active(settings.Active)
	if len(skills) == 0 {
		return nil, nil, nil
	}
	entries, err := store.SearchThread(context.Background(), r.store, entry.ID)
	if err != nil {
		return nil, nil, err
	}
	thread := timeline.Thread(entries, root)
	if len(thread) == 0 {
		return nil, nil, nil
	}
	// A place may be used for this wake without becoming part of the saved
	// timeline. The post copy is shared by prompts and tools below only.
	if transientPlace != nil && len(thread) > 0 {
		thread[0].Place = transientPlace
	}

	ctx, cancel := context.WithTimeout(outer, 10*time.Minute)
	defer cancel()

	// Read before running anyone: new recipients must get an ordinary first delivery.
	previous := map[string]bool{}
	if edited {
		ids, err := r.store.Woke(root)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range ids {
			previous[id] = true
		}
	}

	// Who gets it is the app's one decision about a post.
	named := Named(skills, entry.Text)
	called := map[string]string{}
	targets := map[string]bool{}
	for _, skill := range skills {
		if previous[skill.ID] {
			targets[skill.ID] = true
		}
	}
	for _, id := range named {
		targets[id] = true
		called[id] = "mention"
	}
	parent, hasParent := r.store.Entry(entry.ReplyTo)
	// With no mention overriding the addressee, a reply to a persona is said
	// to it. A post that persona started on its own - a
	// check-in, a schedule, a review - is its question, and a reply under it
	// is the answer.
	asked := ""
	if hasParent && !parent.Deleted && entry.Kind == timeline.KindReply && isActive(skills, parent.Speaker) {
		if len(named) == 0 {
			targets[parent.Speaker] = true
			called[parent.Speaker] = "reply"
		}
		if called[parent.Speaker] != "" && parent.Root == parent.ID {
			asked = parent.Speaker
		}
	}
	// A directed conversation needs no automatic subject or person matching.
	routing := Routing{Answered: true}
	if len(called) == 0 {
		routing = routeSubjects(ctx, client, settings, skills, thread, entry)
	}
	for _, id := range routing.Deliver {
		targets[id] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	definitions, err := json.Marshal(r.skills(ctx).All())
	if err != nil {
		return nil, nil, err
	}
	plan := &store.EntryDelivery{Text: entry.Text, Active: append([]string{}, settings.Active...), Targets: keys(targets), Named: named, Previous: keys(previous), Asked: asked, Heard: routing.Heard, SkillsJSON: string(definitions)}
	if hasParent && !parent.Deleted && entry.Kind == timeline.KindReply && called[parent.Speaker] != "" {
		plan.ReplySpeaker = parent.Speaker
	}
	return plan, routingRecord(entry, routing, called, targets), nil
}

func (r *Runner) deliverEntry(outer context.Context, entryID string, transientPlace *timeline.Place, plan store.EntryDelivery, edited bool) error {
	entry, exists := r.store.Entry(entryID)
	if !exists || entry.Deleted || entry.Text != plan.Text {
		return nil
	}
	client, settings := r.clientOf()
	skills := r.skills(outer).All()
	if plan.Active != nil {
		skills = r.skills(outer).Active(plan.Active)
	}
	// Routing may precede an earlier turn's RecordWake. Refresh only once
	// this edit owns the delivery lane, after earlier turns have finished.
	if edited {
		previous, err := r.store.Woke(timeline.RootOf(entry))
		if err != nil {
			return err
		}
		for _, skill := range skills {
			if contains(previous, skill.ID) {
				if !contains(plan.Previous, skill.ID) {
					plan.Previous = append(plan.Previous, skill.ID)
				}
				if !contains(plan.Targets, skill.ID) {
					plan.Targets = append(plan.Targets, skill.ID)
				}
			}
		}
		if err := r.store.UpdateRouting(entryID, func(record *store.Routing) (*store.Routing, error) {
			if record != nil && record.EntryText != nil && *record.EntryText == plan.Text {
				for _, id := range plan.Previous {
					delete(record.Excluded, id)
				}
			}
			return record, nil
		}); err != nil {
			return err
		}
		r.emit("routed", map[string]any{"entryId": entryID, "deliver": plan.Targets})
	}
	if len(plan.Targets) == 0 {
		return nil
	}
	entries, err := store.SearchThread(outer, r.store, entry.ID)
	if err != nil {
		return err
	}
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	if len(thread) == 0 {
		return nil
	}
	if transientPlace != nil {
		thread[0].Place = transientPlace
	}
	ctx, cancel := context.WithTimeout(outer, 10*time.Minute)
	defer cancel()
	parent, hasParent := r.store.Entry(entry.ReplyTo)
	deep := ""
	if hasParent && !parent.Deleted && plan.ReplySpeaker != "" {
		if Escalate(ctx, client, lightningModel(settings), parent, entry) {
			deep = parent.Speaker
		}
	}
	// The same tool set the wake paths get. Without this a persona reacting to a
	// post was offered schedule and then told it was not configured, which is
	// why sona wrote its times into a note as prose instead of placing them.
	registry := r.registry(settings)
	var wave []Skill
	for _, skill := range skills {
		if contains(plan.Targets, skill.ID) {
			wave = append(wave, skill)
		}
	}
	// Everyone routed runs at once.
	var group sync.WaitGroup
	remarks := make([]*timeline.Entry, len(wave))
	// pending marks a persona whose answer was asked of the external agent;
	// its turn is carried on later and must not be counted as done yet, or a
	// routing sheet would call it quiet before it has started.
	pending := make([]bool, len(wave))
	for i, skill := range wave {
		group.Add(1)
		go func(i int, skill Skill) {
			defer group.Done()
			defer func() { _ = recover() }()
			instructions := ""
			if contains(plan.Previous, skill.ID) {
				instructions = editedInstructions
			}
			if skill.ID == plan.Asked {
				instructions += r.reviewReply(skill, parent, settings)
			}
			if term, ok := plan.Heard[skill.ID]; ok && !contains(plan.Named, skill.ID) && !contains(plan.Previous, skill.ID) && skill.ID != plan.Asked {
				instructions += heardInstructions(skill, term)
			}
			pending[i] = r.detaches(ctx, skill, settings)
			remarks[i] = r.run(ctx, skill, thread, entry, contains(plan.Named, skill.ID), false, instructions, skill.ID == deep, client, settings, registry)
		}(i, skill)
	}
	group.Wait()
	var ran []Skill
	spoke := map[string]bool{}
	for i, skill := range wave {
		if !pending[i] {
			ran = append(ran, skill)
		}
		if remarks[i] != nil {
			spoke[remarks[i].Speaker] = true
		}
	}
	r.turnsDone(entry.ID, ran, spoke)
	return nil
}

// isActive reports whether the persona is in the room.
func isActive(skills []Skill, id string) bool {
	for _, skill := range skills {
		if skill.ID == id {
			return true
		}
	}
	return false
}

// readCapture does the one pass over the image and writes the result back onto
// the post, which is a new line in the log rather than an edit.
func (r *Runner) readCapture(outer context.Context, entry timeline.Entry, client *llm.Client, settings store.Settings) timeline.Entry {
	data, mime, err := r.store.Blob(entry.Media.BlobID)
	if err != nil {
		entry.Media.Error = err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(outer), 3*time.Minute)
		defer cancel()
		ctx = llm.WithTrace(ctx, llm.Trace{Trigger: entry.ID, Wake: "post"})
		read := capture.Read(ctx, client, settings.VisionModel, data, mime)
		read.BlobID = entry.Media.BlobID
		read.MIME = mime
		read.Width, read.Height = entry.Media.Width, entry.Media.Height
		entry.Media = &read
	}
	updated, err := r.store.UpdateMedia(entry.ID, entry.Media)
	if err != nil {
		r.emit("error", map[string]any{"where": "capture", "message": err.Error()})
		return entry
	}
	entry = updated
	r.emit("entry", entry)
	return entry
}

// briefRule is how long a remark is, ordinarily.
const briefRule = "- 発言するときは短く。前に自分が覚えていたことと結びつくなら、それを言ってください。"

// namedRule replaces it when the user named the persona: it was asked
// something directly, so the answer is as long as the question needs - which
// may still be short.
const namedRule = "- 名指しで聞かれています。短くまとめる必要はありません。聞かれたことに答えるのに必要な長さで書いてください。前に自分が覚えていたことと結びつくなら、それを言ってください。"

// agentRule replaces it when an external agent plans the turn. It plans at
// leisure, with its own tools and the time to use them, and is not held to a
// short remark: what it found is worth saying in full - or nothing at all.
const agentRule = "- 短くまとめる必要はありません。調べて分かったこと、根拠、前に自分が覚えていたこととの結びつきを添えて、伝えるのに必要な長さで書いてください。発言は読みやすい Markdown で書いてください。言うことがなければ黙ってください。"

// deepRule replaces it when the user replied asking for a better or deeper
// answer than the one they got.
const deepRule = `- **今回ユーザーは、前の回答より良い・深い回答を求めています。** 短くまとめる必要はありません。
  前の回答を見直し、誤りや不足があれば認めて正してください。必要なら調べ直してください。
  必要な根拠・比較・具体例を添え、質問に答えるのに必要な長さで書いてください。口調はそのままで構いません。
  発言は読みやすい Markdown で書いてください。見出しや箇条書きは内容に役立つときに使ってください。
  前に自分が覚えていたことと結びつくなら、それも言ってください。`

// run wakes one persona and hands it the post. What to read, what to write and
// whether to say anything are still the persona's; it says so as a workflow,
// the app has the workflow checked, and only then does anything happen.
func (r *Runner) run(ctx context.Context, skill Skill, thread []timeline.Entry, trigger timeline.Entry, named, listening bool,
	wakeInstructions string, deep bool, client *llm.Client, settings store.Settings, registry *tools.Registry) *timeline.Entry {
	ctx = llm.WithTrace(ctx, llm.Trace{Persona: skill.ID, Trigger: trigger.ID, Wake: wakeKind(trigger, listening, wakeInstructions)})
	resumed := resumedFrom(ctx)
	skill = skill.forTurn()
	requestedDeep := deep
	deep = deep || skill.AlwaysUltra

	root := timeline.RootOf(trigger)
	// deep is a reply asking for a better or deeper answer: Ultra, and a
	// remark allowed to be as long as the answer needs.
	model, slot := personaModel(settings), "super"
	if deep {
		model, slot = ultraModel(settings), "ultra"
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return nil
	}
	if resumed == nil {
		// Carried on after an ask, the turn did this when it began.
		r.syncEntryPersonSchedules(ctx, skill, trigger)
	}
	if trigger.ID != "" {
		// Who was woken for this post, so that deleting it can tell them - the
		// app never looks inside a note to work that out.
		if err := r.store.RecordWake(root, skill.ID); err != nil {
			r.emit("error", map[string]any{"where": "wakes", "message": err.Error()})
		}
	}
	// Only what the store keeps about the persona: its memory is handed over
	// below, fields whole and the latest records.
	memory := r.store.MemoryMeta(skill.ID)
	if memory.JoinedAt.IsZero() {
		memory.JoinedAt = time.Now()
	}
	set := registry.For(skill.Tools).As(skill.ID)
	length := briefRule
	switch {
	case requestedDeep:
		length = deepRule
	case skill.AlwaysUltra:
		length = "- 常に熟考する設定です。必要な深さで十分に検討し、回答に必要な長さで書いてください。"
	case named && !listening:
		length = namedRule
	}
	if skill.ID == "infographic" {
		length += infographicRules(trigger, thread)
	}
	system := answerSystem(skill, length) + readSpec(set, deep) + capabilityLine(set)
	system += wakeInstructions

	names := map[string]string{}
	for _, other := range r.skills(ctx).All() {
		names[other.ID] = other.Name
	}
	registry = registry.WithPersonaNames(names)
	set = registry.For(skill.Tools).As(skill.ID)
	post := thread[0]
	// The clock a persona is told is the user's, not the container's.
	here := settings.Location()
	scheduleList, scheduleIDs := r.scheduleListFor(skill, set, names)
	// The memory is handed over every time: the fields whole, being short,
	// and the latest records. Older records are read on demand.
	memoryNote := r.memoryView(skill, here)
	// A tidy-up is told what it is by its own instructions; the line for a
	// turn that reads what was said after it would only mislead it.
	turn := turnLine(named, listening)
	if tidiesUp(wakeInstructions) {
		turn = ""
	}
	user := strings.Join([]string{
		"いまの日時: " + time.Now().In(here).Format("2006-01-02 15:04 MST"),
		placeLine(post.Place),
		"",
		"あなたの記憶（最初に必ず渡します）:",
		memoryNote,
		"",
		"自分の schedule（担当名（ID）付き。他の担当の予定は schedules_list で確認）:",
		scheduleList,
		"",
		"スレッド全体（古い順）:",
		timeline.Render(thread, names, here),
		"",
		"今回の対象発言（この発言をスレッド全体の文脈で解釈）:",
		trigger.Render(names, here),
		turn,
	}, "\n")

	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	rules := turnRules{named: named, listening: listening, set: set, schedules: scheduleIDs, cited: map[string]string{}}
	// referenced are the records read this turn, for their dates to move.
	referenced := map[string]bool{}

	record := runRecord{Persona: skill.ID, Trigger: trigger.ID, Wake: wakeKind(trigger, listening, wakeInstructions), Path: "workflow", Model: model, Reviewer: reviewModel(settings)}
	defer func() { logRun(record) }()
	failed := func(err error) *timeline.Entry {
		record.Error = err.Error()
		r.emit("error", map[string]any{"persona": skill.ID, "message": err.Error()})
		if named && !listening {
			return r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID, nil, "")
		}
		return r.hush(skill, memory)
	}

	// What the persona read goes to the phases too: a note the turn read
	// may be rewritten, one it did not is sent back before anything runs.
	var readSources []timeline.Source
	read := []string{"記憶（最初に渡したもの）:\n" + memoryNote}
	failures := map[string]string{}
	readLimit := turnReadLimit(deep)
	budget := newLookups(readLimit, turnSearchLimit(deep))
	var answer *Answer
	// reader does what a persona asked to look at, within its own count.
	// Research and the later phases count apart: research spending every
	// read must not leave a phase unable to read the note it has to rewrite.
	reader := func(used *int, limit int, budget func() *lookups, answering bool) func([]ReadStep) string {
		return func(steps []ReadStep) string {
			if *used >= limit {
				return readsRefused
			}
			text, sources := r.readAll(ctx, skill, steps, set, post, here, failures, budget(), rules.cited, referenced)
			// Only what the answer was researched with goes under it.
			if answering {
				readSources = append(readSources, sources...)
			}
			read = append(read, text)
			record.Reads = append(record.Reads, readRecord{Steps: steps, Output: preview(text)})
			*used++
			return text + readFooter(limit-*used, set)
		}
	}
	readsUsed := 0
	lookup := reader(&readsUsed, readLimit, func() *lookups { return budget }, true)
	// The phases decide changes, not research: they read notes and may look
	// again, but do not search the web.
	phaseReads := 0
	phaseBudget := newLookups(maxReads, 0)
	phaseLookup := reader(&phaseReads, maxReads, func() *lookups { return phaseBudget }, false)

	switch {
	case r.plansExternally(ctx, skill, settings):
		// The answer is asked of the external agent. This turn ends here;
		// once the ask is over, resumeAsk takes it up again from the top.
		record.Planner = "external"
		ask := Ask{ID: store.NewID("ask"), Persona: skill.ID, Name: skill.Name, Wake: record.Wake, Trigger: trigger.ID}
		// The agent is told the same as the built-in model, but not to be brief.
		agentLength := agentRule
		if requestedDeep {
			agentLength = deepRule
		}
		agentRules := fmt.Sprintf(answerRules, agentLength)
		if skill.ID == "infographic" {
			agentRules += infographicRules(trigger, thread)
		}
		brief := r.askContext(skill, set, agentRules, turnLine(named, listening), wakeInstructions, record.Wake, thread, trigger, memoryNote, scheduleList, names, here, deep)
		turn := askTurn{Persona: skill.ID, Trigger: trigger, Place: post.Place, Named: named, Listening: listening, Instructions: wakeInstructions, Deep: deep, SuppressNotification: !scheduleNotify(ctx)}
		if err := r.openAsk(ctx, settings, ask, brief, turn); err != nil {
			return failed(err)
		}
		record.Asked = ask.ID
		return nil
	case resumed != nil && resumed.Answer != nil:
		// What the agent read is what the answer was researched with.
		research := resumed.Research
		readsUsed = research.ReadsUsed
		budget = research.Lookups.restore(readLimit, turnSearchLimit(deep))
		for _, id := range research.Referenced {
			referenced[id] = true
		}
		read = append(read, research.Read...)
		readSources = append(readSources, research.ReadSources...)
		for id, line := range research.Cited {
			rules.cited[id] = line
		}
		for id, why := range research.Failures {
			failures[id] = why
		}
		answer = resumed.Answer
		record.Planner = "external"
		// The remark is marked as the agent's plan, not a model slot's.
		slot = "agent"
	case resumed != nil:
		// The in-process planner takes the turn back, from the start and
		// with its own allowance of drafts.
		record.Fallback = resumed.Fallback
		record.Planner = "external→builtin"
		r.emit("ask", map[string]any{"persona": skill.ID, "id": resumed.Ask.ID, "state": "fallback", "reason": resumed.Fallback, "trigger": trigger.ID})
	}

	if answer == nil && tidiesUp(wakeInstructions) {
		// Nothing to answer: the phases do the tidying, reading as they go.
		answer = &Answer{Sources: []string{}}
	}
	if answer == nil {
		var err error
		messages, err = research(ctx, client, model, messages, func() bool { return readsUsed < readLimit }, lookup)
		if err != nil {
			return failed(err)
		}
		answer, err = composeAnswer(ctx, client, model, messages, rules)
		if err != nil {
			return failed(err)
		}
	}
	record.Answer = answer
	encodedAnswer, _ := json.Marshal(answer)
	phaseContext := user + "\n\nこのターンに読んだ内容:\n" + strings.Join(read, "\n\n") + "\n\n完成した Answer:\n" + string(encodedAnswer)
	// The phases run on the model the turn runs on: one turn, one voice.
	log := turnLog{}
	plan, err := r.planPhases(ctx, client, model, skill, *answer, phaseContext, wakeInstructions, rules, phaseLookup,
		func(plan Workflow) (*Review, error) {
			return r.review(ctx, client, settings, skill, thread, trigger, *answer, plan,
				"既存の予定:\n"+scheduleList+"\n読んだ内容:\n"+clip(strings.Join(read, "\n\n"), 20000), names, here)
		}, &log)
	record.Phases, record.Reviews = log.phases, log.reviews
	if err == nil {
		record.Plan = &plan
	}
	if err != nil {
		return failed(err)
	}
	approved := plan.batch()

	record.Ran = true
	sources, steps := r.execute(ctx, skill, approved, set, rules, post)
	record.Steps = steps
	if trigger.ID != "" && !listening {
		// Only reading for a post counts: a check-in reading the record it is
		// about to ask about must not put off its own question.
		if err := r.extendRecords(ctx, skill.ID, referenced, time.Now(), here); err != nil {
			r.emit("error", map[string]any{"where": "extend", "persona": skill.ID, "message": err.Error()})
		}
	}
	if len(steps) > 0 && steps[len(steps)-1].Error != "" {
		return failed(fmt.Errorf("stopped at %s: %s", steps[len(steps)-1].Type, steps[len(steps)-1].Error))
	}
	spoken := strings.TrimSpace(answer.Text)
	record.Spoke = spoken
	sources = append(sources, citedSources(answer.Sources, rules.cited)...)
	var remark *timeline.Entry
	switch {
	case listening || spoken == "":
		if named && !listening {
			remark = r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID, nil, "")
		} else {
			// It had its turn; its notes are kept below either way.
			remark = r.hush(skill, memory)
		}
	default:
		remark = r.speak(skill, root, spoken, append(readSources, sources...), memory, trigger.ID, nil, slot)
		if remark != nil && record.Wake == "schedule" && scheduleNotify(ctx) {
			r.emit("reminder", *remark)
		}
	}
	// The remark is out. Notes come after it, so a note that cannot be written
	// costs the note and never the answer.
	// A persona that stayed quiet because the post is not its business has
	// nothing to keep. That is its call; the routing sheet shows it, and the
	// user can tell it otherwise there.
	unrelated := answer.Unrelated && spoken == "" && trigger.ID != "" && !named
	if unrelated {
		r.markUnrelated(trigger.ID, skill.ID)
	}
	if skill.Remembers() && keepsNotes(trigger, wakeInstructions) && !unrelated {
		keeper := keeperTurn{skill: skill, thread: thread, trigger: trigger, answer: spoken, wake: wakeInstructions, here: here, names: names}
		if remark != nil {
			keeper.replyID = remark.ID
		}
		snapshot := snapshotKeeper(keeper)
		if payload, err := json.Marshal(snapshot); err == nil {
			// This turn is still running when the periodic retry pass, here or
			// in another process, may see its job. Lease the first attempt for
			// the turn's ten-minute deadline; a failure replaces this with
			// normal backoff.
			job := store.NoteJob{ID: store.NewID("note"), Persona: skill.ID, Stage: "notes", Payload: payload,
				NextAttempt: time.Now().Add(11 * time.Minute)}
			if err := r.store.SaveNoteJob(job); err != nil {
				r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
				written, _ := r.keepNotes(ctx, client, settings, keeper)
				r.pointAtNotes(remark, written)
			} else {
				r.runKeeperJob(ctx, client, settings, job, snapshot)
			}
		} else {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
		}
	}
	return remark
}

// pointAtNotes adds the notes the keeper wrote to the remark that was already
// out, so the remark still leads to what it left behind - a check-in that
// asked about a note is found by the note it asked about.
func (r *Runner) pointAtNotes(remark *timeline.Entry, kept []string) {
	if remark == nil || len(kept) == 0 {
		return
	}
	current, ok := r.store.Entry(remark.ID)
	if !ok || current.Deleted {
		return
	}
	for _, path := range kept {
		if !isRecordsPath(path) && !contains(current.Notes, path) {
			current.Notes = append(current.Notes, path)
		}
	}
	if err := r.store.UpdateEntry(current); err != nil {
		return
	}
	*remark = current
	r.emit("entry", current)
}

// keepsNotes says whether a turn leaves the note keeper something to do: a
// kakera of the user's to record, or a check-in handed records due for
// review, whose dates it moves. After a deletion the app has already taken
// out the records and the facts that rested on it, so a tidy-up keeps nothing.
func keepsNotes(trigger timeline.Entry, instructions string) bool {
	return (trigger.ID != "" && trigger.Speaker == timeline.User && !tidiesUp(instructions)) || strings.Contains(instructions, reviewInstructions)
}

// wakeKind names why a persona was woken, for the log.
func wakeKind(trigger timeline.Entry, listening bool, instructions string) string {
	switch {
	case trigger.ID != "" && listening:
		return "listening"
	case trigger.ID != "" && strings.HasPrefix(instructions, editedInstructions):
		return "post-edited"
	case trigger.ID != "" && strings.Contains(instructions, reviewReplyInstructions):
		return "review-reply"
	case trigger.ID != "":
		return "post"
	case strings.HasPrefix(instructions, dailyHeader):
		return "daily"
	case strings.HasPrefix(instructions, scheduleInstructions), strings.HasPrefix(instructions, userScheduleInstructions):
		return "schedule"
	case instructions == deletedInstructions:
		return "post-deleted"
	}
	return "wake"
}

// hush saves what the persona learned and says nothing. Not speaking is an
// outcome, not a failure.
func (r *Runner) hush(skill Skill, memory store.Memory) *timeline.Entry {
	memory.Wakes++
	_ = r.store.SaveMemory(memory)
	r.emit("quiet", map[string]any{"persona": skill.ID})
	return nil
}

func (r *Runner) speak(skill Skill, root, text string, sources []timeline.Source, memory store.Memory, replyTo string, notes []string, slot string) *timeline.Entry {
	memory.Wakes++
	_ = r.store.SaveMemory(memory)
	if replyTo == "" {
		replyTo = root
	}
	remark := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: skill.ID,
		Text: text, At: time.Now(), Root: root, ReplyTo: replyTo, Sources: dedupe(sources), Notes: notes, Model: slot,
	}
	if root == "" {
		remark.Kind = timeline.KindPost
		remark.Root = remark.ID
		remark.ReplyTo = ""
	}
	if err := r.store.Append(remark); err != nil {
		return nil
	}
	r.emit("entry", remark)
	return &remark
}

func placeLine(place *timeline.Place) string {
	if place == nil {
		return "いまいる場所: 不明（PCからの投稿など、位置が無いことがあります）"
	}
	// The coordinates travel with the label: a built-in tool is handed the
	// position by the app, but an MCP tool gets only what the persona writes.
	line := "いまいる場所: " + place.Describe()
	if place.Label != "" {
		line += fmt.Sprintf("（北緯%.4f 東経%.4f）", place.Lat, place.Lon)
	}
	return line
}

func turnLine(named, listening bool) string {
	switch {
	case listening:
		return "\n※ **このターンは発言できません。** あなたの後に出た発言を読むだけです。" +
			"覚えておくことがあれば、あとでメモ係が書きます。Answer の text は空にしてください。"
	case named:
		return "\n※ あなたは名指しで呼ばれています。Answer の text を空にはできません。必ず何か答えてください。"
	}
	return ""
}

// personaModel is the model every persona answers on: Super, or whatever sits
// in the middle slot. An unset slot falls back to the light one so a
// single-model setup still works.
func personaModel(settings store.Settings) string {
	return firstSet(settings.MiddleModel, settings.LowModel)
}

// ultraModel answers a reply that asked for a better or deeper answer.
func ultraModel(settings store.Settings) string {
	return firstSet(settings.HighModel, settings.MiddleModel, settings.LowModel)
}

// lightningModel only decides whether a reply asks for Ultra.
func lightningModel(settings store.Settings) string {
	return strings.TrimSpace(settings.LowModel)
}

func firstSet(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

const maxReplySources = 3

// dedupe keeps the tools' relevance order and leaves a reply with only the
// few links a reader is likely to inspect. Tool results remain in the model's
// context; this limit applies only to links displayed beneath its reply.
func dedupe(sources []timeline.Source) []timeline.Source {
	seen := map[string]bool{}
	var out []timeline.Source
	for _, source := range sources {
		if source.URL == "" || seen[source.URL] {
			continue
		}
		seen[source.URL] = true
		out = append(out, source)
		if len(out) == maxReplySources {
			break
		}
	}
	return out
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func stringOf(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringsOf(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		if text := stringOf(item); text != "" {
			out = append(out, text)
		}
	}
	return out
}
