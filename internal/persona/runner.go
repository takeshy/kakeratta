package persona

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
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

// okfSkill is how a persona is told to write its notes: as an Open Knowledge
// Format bundle, one concept per file.
//
// It is a format, not a policy. What to keep and when to compress stay in each
// persona's own instructions, and nothing in this app parses what it writes -
// a persona that ignores the format still has its notes kept, and that is the
// bargain.
// The format is offered because one blob of prose rewritten every time loses
// things quietly, while named concepts each led by a description can be reread,
// and changed one at a time.
//
//go:embed skills/okf/SKILL.md
var okfSkill string

// okfHost is the one line of introduction the skill needs. Everything else it
// says already fits this app, because the skill was written for it.
const okfHost = "# メモの書き方\n\nメモを書くときは、次のスキルに従ってください。\n\n---\n\n"

// Runner wakes personas. One runs per app.
//
// Reactions are asynchronous by default: a post is stored and shown
// immediately, and whatever anyone has to say about it arrives afterwards. The
// queue is what makes that true.
type reaction struct {
	// ids are the durable records (store.Reaction) behind this item. The
	// periodic checks have none: they are asked for again every minute.
	ids        []string
	entryID    string
	edited     bool
	place      *timeline.Place
	retryNotes bool
	daily      bool
	schedules  bool
	// forgotten is a note the user deleted: the persona is told, so it can put
	// its own memory back in order. The app never edits a note itself.
	forgotten *forgotten
	// deleted is a post the user removed that a persona had cited.
	deleted *deletedPost
}

type forgotten struct {
	skill string
	paths []string
	// expired: the app deleted these itself because their expires date had
	// passed, rather than the user removing them. The persona is told which,
	// since one is a choice to respect and the other its own arrangement.
	expired bool
}

type Runner struct {
	store     store.Workspace
	catalogue *Catalogue
	clientOf  func() (*llm.Client, store.Settings)
	search    *search.Service
	tools     tools.Config
	emit      func(name string, data any)

	queue   []reaction
	wake    *sync.Cond
	done    chan struct{}
	once    sync.Once
	started bool

	// stopMu guards the unbounded in-memory queue and coordinates its worker.
	stopMu  sync.Mutex
	stopped bool
	// known names the durable reactions in the queue or in hand, so rereading
	// the store does not queue one twice.
	known map[string]bool
	// recovered is set once the reactions a previous process left behind have
	// been taken up.
	recovered bool
	// follower is a runner on an instance that does not own the workspace. It
	// keeps what it is given durable for the owner, and works nothing itself.
	follower atomic.Bool

	// routingMu makes a read-modify-write on one post's routing record atomic.
	// keepRouting, turnsDone and FinishAsyncTurn each read the record and write
	// back a change; a turn planned by an external agent finishes on its own
	// goroutine, concurrently with the wave that delivered it, so without this a write
	// racing the other could read a stale copy and silently drop the other
	// persona's just-recorded reply.
	routingMu     sync.Mutex
	trialMu       sync.Mutex
	routingTrials map[string]routingTrial
	replyTrials   map[string]replyTrial

	// desk holds the asks waiting on an external planner.
	desk *desk
	// apart ends the waits of turns running apart from the queue - those
	// waiting on an external planner. Stop cancels it and waits for them.
	apart     context.Context
	stopApart context.CancelFunc
	detached  sync.WaitGroup

	// keepers holds one lock per persona, so its note keeper runs one at a time.
	keepers       sync.Map
	calendarLocks sync.Map
	// agentSeen is when the external agent last called in over MCP, as Unix
	// nanoseconds; zero until it first does after a start.
	agentSeen atomic.Int64
	// waitsForAsk overrides the settings' claim and plan waits, for tests.
	waitsForAsk func() (time.Duration, time.Duration)
	// connectionWaits are the waits of the connection in use; a zero one
	// falls back to the settings.
	connectionWaits func() (time.Duration, time.Duration)
}

func New(s store.Workspace, catalogue *Catalogue, clientOf func() (*llm.Client, store.Settings)) *Runner {
	r := &Runner{
		store: s, catalogue: catalogue, clientOf: clientOf,
		search:        search.New(s),
		emit:          func(string, any) {},
		done:          make(chan struct{}),
		routingTrials: map[string]routingTrial{}, replyTrials: map[string]replyTrial{},
		known: map[string]bool{},
		desk:  newDesk(),
	}
	r.apart, r.stopApart = context.WithCancel(context.Background())
	r.wake = sync.NewCond(&r.stopMu)
	return r
}

// SetSearch shares the UI's indexing work with all personas. Set before Start.
func (r *Runner) SetSearch(service *search.Service) { r.search = service }

// SetTools is the operator's tool configuration: search key, map endpoints and
// MCP servers. Set before Start.
func (r *Runner) SetTools(config tools.Config) { r.tools = config }

// SetFollower makes this runner keep reactions for the instance that owns the
// workspace instead of working them. Set before anything is queued.
func (r *Runner) SetFollower(follower bool) { r.follower.Store(follower) }

// OnEvent is how the screen hears that something landed.
func (r *Runner) OnEvent(emit func(name string, data any)) {
	if emit != nil {
		r.emit = emit
	}
}

// Start begins draining the queue. Posts are handled one at a time: two posts
// reacting at once would interleave remarks from different threads and make the
// ordering inside each one impossible to reason about.
func (r *Runner) Start() {
	r.once.Do(func() {
		r.stopMu.Lock()
		r.started = true
		r.stopMu.Unlock()
		// What a previous process or owner left unfinished is taken up first.
		r.Poke()
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-r.done:
					return
				case <-ticker.C:
					r.stopMu.Lock()
					if !r.stopped && !r.queuedLocked(func(item reaction) bool { return item.retryNotes }) {
						r.queue = append(r.queue, reaction{retryNotes: true})
						r.wake.Signal()
					}
					r.stopMu.Unlock()
					// A reaction another instance kept while the change
					// feed was down is found here at the latest.
					r.Poke()
				}
			}
		}()
		go func() {
			defer close(r.done)
			r.retryNoteJobs(time.Now())
			for {
				r.stopMu.Lock()
				for len(r.queue) == 0 && !r.stopped {
					r.wake.Wait()
				}
				if len(r.queue) == 0 && r.stopped {
					r.stopMu.Unlock()
					return
				}
				item := r.queue[0]
				r.queue = r.queue[1:]
				r.stopMu.Unlock()
				r.work(item)
			}
		}()
	})
}

// work does one queued item and then lets its durable records go.
func (r *Runner) work(item reaction) {
	defer r.finish(item)
	switch {
	case item.retryNotes:
		r.retryNoteJobs(time.Now())
	case item.daily:
		r.dailyAt(time.Now())
	case item.schedules:
		r.schedulesAt(time.Now())
	case item.forgotten != nil:
		r.forgottenAt(*item.forgotten)
	case item.deleted != nil:
		r.deletedAt(*item.deleted)
	default:
		r.reactEntry(item.entryID, item.place, item.edited)
	}
}

func (r *Runner) finish(item reaction) {
	for _, id := range item.ids {
		if err := r.store.FinishReaction(id); err != nil {
			r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		}
	}
	r.stopMu.Lock()
	for _, id := range item.ids {
		delete(r.known, id)
	}
	r.stopMu.Unlock()
}

// maxReactionAttempts bounds how often a reaction left behind is taken up
// again. One that brought its process down each time would otherwise do so
// forever.
const maxReactionAttempts = 3

// Poke rereads the durable queue for reactions this runner has not got in
// hand: ones another instance kept for it, or ones a previous process left
// unfinished. The first reread after starting counts each of those as another
// attempt.
func (r *Runner) Poke() {
	// The store is read with the queue held: a reaction finishing meanwhile
	// is still known when the read comes back, so it is not queued again.
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped || r.follower.Load() || !r.started {
		return
	}
	recovering := !r.recovered
	r.recovered = true
	items, err := r.store.Reactions(context.Background())
	if err != nil {
		r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
		return
	}
	for _, record := range items {
		if r.known[record.ID] {
			continue
		}
		if recovering {
			record.Attempts++
			if record.Attempts > maxReactionAttempts {
				_ = r.store.FinishReaction(record.ID)
				r.emit("error", map[string]any{"where": "queue", "message": "反応を" + strconv.Itoa(maxReactionAttempts) + "回試みても終わらなかったため、取りやめました"})
				continue
			}
			_ = r.store.SaveReaction(record)
		}
		r.known[record.ID] = true
		r.takeLocked(reactionFrom(record))
	}
	r.wake.Signal()
}

// takeLocked puts an item in the queue, folding deleted notes and posts into
// a wake already waiting for the same persona.
func (r *Runner) takeLocked(item reaction) {
	for i := range r.queue {
		queued := &r.queue[i]
		switch {
		case item.forgotten != nil && queued.forgotten != nil && queued.forgotten.skill == item.forgotten.skill && queued.forgotten.expired == item.forgotten.expired:
			queued.forgotten.paths = append(queued.forgotten.paths, item.forgotten.paths...)
		case item.deleted != nil && queued.deleted != nil && queued.deleted.skill == item.deleted.skill && queued.deleted.entryID == item.deleted.entryID:
		default:
			continue
		}
		queued.ids = append(queued.ids, item.ids...)
		return
	}
	r.queue = append(r.queue, item)
}

// acceptLocked keeps a reaction durable and, on a runner that works the
// queue, in hand. A stopped runner still keeps it: whoever starts next - this
// process again, or another instance - takes it up. Callers hold stopMu.
func (r *Runner) acceptLocked(item reaction) {
	record := item.record()
	record.ID, record.At = store.NewID("r"), time.Now()
	if err := r.store.SaveReaction(record); err != nil {
		r.emit("error", map[string]any{"where": "queue", "message": err.Error()})
	} else {
		item.ids = []string{record.ID}
	}
	if r.stopped || r.follower.Load() {
		return
	}
	for _, id := range item.ids {
		r.known[id] = true
	}
	r.takeLocked(item)
	r.wake.Signal()
}

func (item reaction) record() store.Reaction {
	record := store.Reaction{Entry: item.entryID, Edited: item.edited, Place: item.place}
	if item.forgotten != nil {
		record.Forgotten = &store.ForgottenReaction{Skill: item.forgotten.skill, Paths: slices.Clone(item.forgotten.paths), Expired: item.forgotten.expired}
	}
	if item.deleted != nil {
		d := item.deleted
		record.Deleted = &store.DeletedReaction{EntryID: d.entryID, Label: d.label, Skill: d.skill, Notes: slices.Clone(d.notes), Removed: slices.Clone(d.removed)}
	}
	return record
}

func reactionFrom(record store.Reaction) reaction {
	item := reaction{ids: []string{record.ID}, entryID: record.Entry, edited: record.Edited, place: record.Place}
	if f := record.Forgotten; f != nil {
		item.forgotten = &forgotten{skill: f.Skill, paths: slices.Clone(f.Paths), expired: f.Expired}
	}
	if d := record.Deleted; d != nil {
		item.deleted = &deletedPost{entryID: d.EntryID, label: d.Label, skill: d.Skill, notes: slices.Clone(d.Notes), removed: slices.Clone(d.Removed)}
	}
	return item
}

// Stop lets the reactions in hand finish and then returns.
//
// A reaction writes as it goes - a remark, a persona's notes - so killing one
// half way through leaves the workspace mid-thought. There is at most one in
// flight, and shutdown drains everything already accepted.
//
// Turns waiting on an external planner are abandoned: an agent may take many
// minutes, and a restart ends the ask it would hand its workflow to anyway.
func (r *Runner) Stop() { r.stop(false) }

// Halt finishes the item in hand and stops without working the rest of the
// queue. Those reactions stay kept, for this process when it starts again or
// for another instance taking the workspace over - which is what a shutdown
// with seconds to spare, or an instance giving a workspace up, wants.
func (r *Runner) Halt() { r.stop(true) }

func (r *Runner) stop(halt bool) {
	defer func() {
		r.stopApart()
		r.detached.Wait()
	}()
	r.stopMu.Lock()
	if halt {
		r.queue = nil
	}
	if r.stopped || !r.started {
		r.stopped = true
		r.stopMu.Unlock()
		return
	}
	r.stopped = true
	r.wake.Broadcast()
	r.stopMu.Unlock()
	<-r.done
}

// React queues an entry. It never blocks the request that made the post: the
// user has already seen their capture land.
func (r *Runner) React(entryID string, place *timeline.Place) {
	r.enqueueEntry(entryID, place, false)
}

// EntryEdited notifies previous recipients and routes the current content anew.
func (r *Runner) EntryEdited(entryID string) {
	r.enqueueEntry(entryID, nil, true)
}

func (r *Runner) enqueueEntry(entryID string, place *timeline.Place, edited bool) {
	if !r.follower.Load() {
		r.Start()
	}
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	r.acceptLocked(reaction{entryID: entryID, place: place, edited: edited})
}

// queuedLocked reports whether a matching item is already waiting, so a
// periodic check held up behind a long reaction is queued once, not once for
// every minute it waited.
func (r *Runner) queuedLocked(match func(reaction) bool) bool {
	for _, item := range r.queue {
		if match(item) {
			return true
		}
	}
	return false
}

func (r *Runner) react(entryID string) {
	r.reactAt(entryID, nil)
}

func (r *Runner) reactAt(entryID string, transientPlace *timeline.Place) {
	r.reactEntry(entryID, transientPlace, false)
}

// keepRouting writes down who a post went to, for a person to look at later:
// the personas the router chose, the ones the user called by @ or by replying
// to them (with how), and the ones the router passed over. Anyone who got the
// post for another reason is left out of the passed-over. Nothing is kept when
// the router had no answer and nobody was called - "no record" and "nobody"
// are different things to read.
func (r *Runner) keepRouting(entry timeline.Entry, routing Routing, called map[string]string, targets map[string]bool) {
	entryID := entry.ID
	r.routingMu.Lock()
	defer r.routingMu.Unlock()
	if !routing.Answered && len(called) == 0 {
		if err := r.store.ForgetRouting(entryID); err != nil {
			r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
		}
		return
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
	// Routing takes a while; a post deleted meanwhile keeps no record.
	if current, ok := r.store.Entry(entryID); !ok || current.Deleted || current.Text != entry.Text {
		return
	}
	if err := r.store.SaveRouting(entryID, record); err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
}

// turnsDone notes on the post's routing record that these personas have had
// their turn - spoken or not - and tells the page, so a sheet showing the
// router's call can stop saying the persona is still thinking.
func (r *Runner) turnsDone(entryID string, wave []Skill, spoke map[string]bool) {
	r.routingMu.Lock()
	defer r.routingMu.Unlock()
	record, err := r.store.Routing(entryID)
	if err != nil || record == nil {
		return
	}
	if record.Done == nil {
		record.Done = []string{}
	}
	if record.Spoke == nil {
		record.Spoke = []string{}
	}
	var ids []string
	for _, skill := range wave {
		ids = append(ids, skill.ID)
		if !contains(record.Done, skill.ID) {
			record.Done = append(record.Done, skill.ID)
		}
		if contains(record.Delivered, skill.ID) && spoke[skill.ID] && !contains(record.Spoke, skill.ID) {
			record.Spoke = append(record.Spoke, skill.ID)
		}
	}
	if err := r.store.SaveRouting(entryID, *record); err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
	r.emit("turn", map[string]any{"entryId": entryID, "personas": ids})
}

// markUnrelated notes on the post's routing record that this persona stayed
// quiet because the post was not its business.
func (r *Runner) markUnrelated(entryID, personaID string) {
	r.routingMu.Lock()
	defer r.routingMu.Unlock()
	record, err := r.store.Routing(entryID)
	if err != nil || record == nil || contains(record.Unrelated, personaID) {
		return
	}
	record.Unrelated = append(record.Unrelated, personaID)
	if err := r.store.SaveRouting(entryID, *record); err != nil {
		r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
	}
}

// FinishAsyncTurn is turnsDone for a persona whose turn was left pending
// because it runs apart from the queue - waiting on an external planner.
// Called once that turn has actually concluded, so a routing sheet that has
// been saying "still thinking" can move on to what really happened.
func (r *Runner) FinishAsyncTurn(entryID, personaID string, spoke bool) {
	if entryID == "" {
		return
	}
	r.routingMu.Lock()
	defer r.routingMu.Unlock()
	record, err := r.store.Routing(entryID)
	if err != nil || record == nil {
		return
	}
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
	if err := r.store.SaveRouting(entryID, *record); err != nil {
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
	return settings.JevEnabled && strings.TrimSpace(settings.OpenRouterAPIKey) != ""
}

// CheckRouting routes a post again without delivering it, so a person who has
// just changed a persona's conditions can see whether the change did what
// they meant. It reads the conditions as they are now saved; nothing is woken
// and nothing is written down.
func (r *Runner) CheckRouting(ctx context.Context, entryID, skillID string) (RoutingCheck, error) {
	entry, ok := r.store.Entry(entryID)
	if !ok || entry.Deleted {
		return RoutingCheck{}, errors.New("投稿が見つかりません")
	}
	client, settings := r.clientOf()
	skills := r.catalogue.Active(settings.Active)
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
		r.trialMu.Lock()
		r.pruneTrials(time.Now())
		r.routingTrials[check.Token] = routingTrial{entryID: entryID, personaID: skillID, text: entry.Text, delivered: check.Delivered, reason: check.Reason, heard: check.Heard, at: time.Now()}
		r.trialMu.Unlock()
	}
	return check, nil
}

const editedInstructions = "\n\n以前あなたに渡した投稿が編集されました。今回の対象発言は編集後の内容です。以前の内容より優先し、必要に応じて回答・メモ・予定を更新してください。担当領域の内容が削除された場合も、その変更を反映してください。"

func (r *Runner) reactEntry(entryID string, transientPlace *timeline.Place, edited bool) {
	defer func() {
		// A persona is a model doing as it pleases with a prompt. One of them
		// panicking must not take the queue with it.
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "react", "message": fmt.Sprint(problem)})
		}
	}()

	entry, exists := r.store.Entry(entryID)
	if !exists || entry.Deleted {
		return
	}
	client, settings := r.clientOf()
	root := timeline.RootOf(entry)

	// The picture is read once, here, before anyone is woken. Everyone gets the
	// same text out of it.
	if entry.Kind == timeline.KindPost && entry.Media != nil && !entry.Media.Ready() {
		entry = r.readCapture(entry, client, settings)
		if entry.Deleted {
			return
		}
	}

	skills := r.catalogue.Active(settings.Active)
	if len(skills) == 0 {
		return
	}
	entries, err := store.SearchThread(context.Background(), r.store, entry.ID)
	if err != nil {
		return
	}
	thread := timeline.Thread(entries, root)
	if len(thread) == 0 {
		return
	}
	// A place may be used for this wake without becoming part of the saved
	// timeline. The post copy is shared by prompts and tools below only.
	if transientPlace != nil && len(thread) > 0 {
		thread[0].Place = transientPlace
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Read before running anyone: new recipients must get an ordinary first delivery.
	previous := map[string]bool{}
	if edited {
		ids, err := r.store.Woke(root)
		if err != nil {
			r.emit("error", map[string]any{"where": "wakes", "message": err.Error()})
			return
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
	// A reply to a persona is said to it: it hears it whether or not routing
	// would have chosen it. A post that persona started on its own - a
	// check-in, a schedule, a review - is its question, and a reply under it
	// is the answer.
	asked := ""
	if hasParent && !parent.Deleted && entry.Kind == timeline.KindReply && isActive(skills, parent.Speaker) {
		targets[parent.Speaker] = true
		if _, ok := called[parent.Speaker]; !ok {
			called[parent.Speaker] = "reply"
		}
		if parent.Root == parent.ID {
			asked = parent.Speaker
		}
	}
	routing := routeSubjects(ctx, client, settings, skills, thread, entry)
	for _, id := range routing.Deliver {
		targets[id] = true
	}
	// Everyone answers on Super. A reply that asks a persona's answer to be
	// better or deeper - "違う", "もっと詳しく" - is answered on Ultra by the
	// persona it was said to. Anyone else it reaches answers as usual.
	deep := ""
	if hasParent && !parent.Deleted && entry.Kind == timeline.KindReply && isActive(skills, parent.Speaker) {
		if Escalate(ctx, client, lightningModel(settings), parent, entry) {
			deep = parent.Speaker
		}
	}
	r.keepRouting(entry, routing, called, targets)
	if len(targets) == 0 {
		r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": []string{}})
		return
	}
	r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": keys(targets)})

	// The same tool set the wake paths get. Without this a persona reacting to a
	// post was offered schedule and then told it was not configured, which is
	// why plana wrote its times into a note as prose instead of placing them.
	registry := r.registry(settings)
	var wave []Skill
	for _, skill := range skills {
		if targets[skill.ID] {
			wave = append(wave, skill)
		}
	}
	// Everyone routed runs at once.
	var group sync.WaitGroup
	remarks := make([]*timeline.Entry, len(wave))
	// pending marks a persona whose turn runs apart from the queue, waiting
	// on an external planner; such a turn must not be counted as done yet,
	// or a routing sheet would call it quiet before it has started.
	pending := make([]bool, len(wave))
	for i, skill := range wave {
		group.Add(1)
		go func(i int, skill Skill) {
			defer group.Done()
			defer func() { _ = recover() }()
			instructions := ""
			if previous[skill.ID] {
				instructions = editedInstructions
			}
			if skill.ID == asked {
				instructions += r.reviewReply(skill, parent, settings)
			}
			if term, ok := routing.Heard[skill.ID]; ok && !contains(named, skill.ID) && !previous[skill.ID] && skill.ID != asked {
				instructions += heardInstructions(skill, term)
			}
			pending[i] = r.detaches(ctx, skill, settings)
			remarks[i] = r.run(ctx, skill, thread, entry, contains(named, skill.ID), false, instructions, skill.ID == deep, client, settings, registry)
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
func (r *Runner) readCapture(entry timeline.Entry, client *llm.Client, settings store.Settings) timeline.Entry {
	data, mime, err := r.store.Blob(entry.Media.BlobID)
	if err != nil {
		entry.Media.Error = err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
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
	if r.detaches(ctx, skill, settings) {
		r.detach(skill, trigger, settings, func(ctx context.Context) *timeline.Entry {
			return r.run(ctx, skill, thread, trigger, named, listening, wakeInstructions, deep, client, settings, registry)
		})
		return nil
	}
	skill = skill.forTurn()

	root := timeline.RootOf(trigger)
	daily := trigger.ID == ""
	// deep is a reply asking for a better or deeper answer: Ultra, and a
	// remark allowed to be as long as the answer needs.
	model, slot := personaModel(settings), "super"
	if deep {
		model, slot = ultraModel(settings), "ultra"
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return nil
	}
	r.syncEntryPersonSchedules(ctx, skill, trigger)
	if trigger.ID != "" {
		// Who was woken for this post, so that deleting it can tell them - the
		// app never looks inside a note to work that out.
		if err := r.store.RecordWake(root, skill.ID); err != nil {
			r.emit("error", map[string]any{"where": "wakes", "message": err.Error()})
		}
	}
	// Only what the store keeps about the persona: the notes themselves are
	// read on demand, through index.md, which is what it is for.
	memory := r.store.MemoryMeta(skill.ID)
	if memory.JoinedAt.IsZero() {
		memory.JoinedAt = time.Now()
	}
	set := registry.For(skill.Tools).As(skill.ID)
	layout := strings.TrimSpace(skill.Memory)
	if layout == "" {
		layout = "（指示に書かれたとおり。対象の種類ごとに型を1つ決め、同じ対象は型が違っても1つのメモにします）"
	}
	length := briefRule
	switch {
	case deep:
		length = deepRule
	case named && !listening:
		length = namedRule
	}
	system := answerSystem(skill, length) + readSpec(set, deep) + capabilityLine(set)
	system += wakeInstructions

	names := map[string]string{}
	for _, other := range r.catalogue.All() {
		names[other.ID] = other.Name
	}
	post := thread[0]
	// The clock a persona is told is the user's, not the container's.
	here := settings.Location()
	scheduleList, scheduleIDs := r.scheduleListFor(skill, set)
	// The index is handed over every time, whole. It is the persona's own table
	// of contents, and deciding what to read without it means reading blind.
	indexNote := "（まだありません）"
	if note, ok := r.store.Note(skill.ID, "index.md"); ok {
		indexNote = note.Text
	}
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
		r.noteCount(skill.ID),
		"",
		"あなたの index.md（最初に必ず渡します）:",
		indexNote,
		"",
		"あなたが置いている schedule:",
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
	// referenced are the notes read this turn, for their dates to move - and
	// for a plan that rewrites an existing note unread to be sent back. The
	// index was handed over at the start, so it counts as read.
	referenced := map[string]bool{"index.md": true}
	rules := turnRules{named: named, listening: listening, noteRequired: trigger.ID != "" && !listening, set: set, schedules: scheduleIDs, notes: func(path string) []store.Note { return r.siblingNotes(skill.ID, path) }, memory: skill.Memory, cited: map[string]string{},
		readState: func(path string) (bool, bool) {
			note, ok := r.store.Note(skill.ID, strings.TrimPrefix(strings.TrimSpace(path), "/"))
			if !ok {
				return false, false
			}
			return true, referenced[note.Path]
		}}
	// The triggering entry is known by the host; no model-supplied ID is needed.
	label := []rune(strings.Join(strings.Fields(trigger.Text), " "))
	if len(label) > 140 {
		label = append(label[:140], '…')
	}
	prov := newProvenance()
	if !daily {
		prov = newProvenance(store.NoteSource{EntryID: trigger.ID, Label: trigger.At.In(here).Format("2006-01-02 15:04 MST") + " — " + string(label)})
	}

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
	read := []string{"index.md（最初に渡したもの）:\n" + indexNote}
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
			for _, step := range steps {
				if step.Type == "notes_read" || (step.Type == "tool" && step.Tool == "notes_read") {
					for _, path := range step.Paths {
						referenced[strings.TrimPrefix(path, "/")] = true
					}
				}
			}
			text, sources := r.readAll(ctx, skill, steps, set, post, prov, failures, budget(), rules.cited)
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

	if r.plansExternally(ctx, skill, settings) {
		record.Planner = "external"
		ask := Ask{ID: store.NewID("ask"), Persona: skill.ID, Name: skill.Name, Wake: record.Wake, Trigger: trigger.ID}
		// The agent is told the same as the built-in model, but not to be brief.
		agentLength := agentRule
		if deep {
			agentLength = deepRule
		}
		agentRules := fmt.Sprintf(answerRules, agentLength)
		brief := r.askContext(skill, set, agentRules, turnLine(named, listening), wakeInstructions, record.Wake, thread, trigger, indexNote, scheduleList, names, here, deep)
		externalAnswer, fallback, err := r.planExternally(ctx, settings, ask, brief, externalPlan{
			read: lookup,
			consider: func(candidate *Answer, parseErr error) (bool, []string, error) {
				if parseErr != nil {
					return false, []string{"Answer の形式が不正です: " + parseErr.Error()}, nil
				}
				if candidate == nil {
					return false, []string{"Answer の形式が不正です"}, nil
				}
				problems := checkAnswer(*candidate, rules)
				return len(problems) == 0, problems, nil
			},
		})
		switch {
		case errors.Is(err, errAborted):
			record.Error = "aborted while waiting for the external planner"
			return nil
		case externalAnswer != nil:
			answer = externalAnswer
			// The remark is marked as the agent's plan, not a model slot's.
			slot = "agent"
		default:
			// The in-process planner takes the turn back, from the start and
			// with its own allowance of drafts.
			record.Fallback = fallback
			record.Planner = "external→builtin"
			r.emit("ask", map[string]any{"persona": skill.ID, "id": ask.ID, "state": "fallback", "reason": fallback, "trigger": trigger.ID})
			readsUsed = 0
			budget = newLookups(readLimit, turnSearchLimit(deep))
		}
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
	plan, err := r.planPhases(ctx, client, model, skill, *answer, phaseContext, layout, wakeInstructions, rules, phaseLookup,
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
	sources, steps := r.execute(ctx, skill, approved, set, rules, post, prov)
	record.Steps = steps
	if trigger.ID != "" && !listening {
		r.extendReferenced(skill.ID, referenced, steps, here)
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
		remark = r.speak(skill, root, spoken, append(readSources, sources...), memory, trigger.ID, notesWritten(steps), slot)
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
	if keepsNotes(trigger, wakeInstructions) && !unrelated {
		keeper := keeperTurn{
			skill: skill, set: set, rules: rules, thread: thread, trigger: trigger, post: post,
			answer: spoken, wake: wakeInstructions, here: here, names: names, own: prov.own, layout: layout, schedules: plan.Schedules, preread: prereadNotes(referenced),
			handed: r.handedNotes(skill.ID, trigger, wakeInstructions),
		}
		snapshot := snapshotKeeper(keeper, remark)
		if payload, err := json.Marshal(snapshot); err == nil {
			// A detached turn may still be running when the queue's periodic
			// retry pass sees its job. Lease the first attempt for the turn's
			// ten-minute deadline; a failure replaces this with normal backoff.
			job := store.NoteJob{ID: store.NewID("note"), Persona: skill.ID, Stage: "notes", Payload: payload,
				NextAttempt: time.Now().Add(11 * time.Minute)}
			if err := r.store.SaveNoteJob(job); err != nil {
				r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
				written, _, _ := r.keepNotes(ctx, client, settings, keeper)
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

// prereadNotes are the notes a turn opened while it worked, in name order and no
// more than the keeper takes.
func prereadNotes(referenced map[string]bool) []string {
	var paths []string
	for path := range referenced {
		if !reservedNote(path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	if len(paths) > maxKeeperReads {
		paths = paths[:maxKeeperReads]
	}
	return paths
}

// maxHanded caps the notes the app hands the keeper on its own account. A
// tidy-up can touch more notes than a turn chooses, so it is larger than
// maxKeeperReads.
const maxHanded = 12

// notePathMention finds note paths named in a wake's text: the notes a review
// is due for, or the notes a deletion took away or left behind.
var notePathMention = regexp.MustCompile(`[^\s,()（）、「」:：]+\.md`)

// handedNotes are the notes the keeper must see whole without having to find
// them: the ones the app itself named in the wake - due for review, left behind
// or taken away by a deletion - the notes that still link to one taken away,
// and, for the user's reply to a post of the persona's own, the notes that post
// was about. The keeper cannot read, so a note it needs and is not handed is a
// note it cannot fix.
func (r *Runner) handedNotes(personaID string, trigger timeline.Entry, instructions string) []string {
	var named []string
	if trigger.Speaker == "system" || trigger.ID == "" {
		named = notePathMention.FindAllString(trigger.Text, -1)
	}
	if parent, ok := r.store.Entry(trigger.ReplyTo); ok && trigger.Kind == timeline.KindReply && parent.Speaker == personaID && parent.Root == parent.ID {
		named = append(named, parent.Notes...)
	}
	var out, gone []string
	seen := map[string]bool{}
	for _, path := range named {
		path = strings.TrimPrefix(strings.TrimSpace(path), "/")
		if path == "" || seen[path] || reservedNote(path) {
			continue
		}
		seen[path] = true
		if _, ok := r.store.Note(personaID, path); ok {
			out = append(out, path)
		} else {
			gone = append(gone, path)
		}
	}
	// A note that links to one that is gone has a reference to take out, and
	// the index does not say which notes those are.
	if len(gone) > 0 && tidiesUp(instructions) {
		for _, note := range r.allNotes(personaID) {
			if seen[note.Path] || reservedNote(note.Path) {
				continue
			}
			for _, path := range gone {
				if mentions(note.Text, path) {
					seen[note.Path] = true
					out = append(out, note.Path)
					break
				}
			}
		}
	}
	if len(out) > maxHanded {
		out = out[:maxHanded]
	}
	return out
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
		if !reservedNote(path) && !contains(current.Notes, path) {
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
// post or reply of the user's, a tidy-up after something was deleted or
// expired, the daily check-in, where a persona may sort its memory, and a review,
// where the note asked about has its date moved.
func keepsNotes(trigger timeline.Entry, instructions string) bool {
	return trigger.ID != "" || tidiesUp(instructions) || strings.HasPrefix(instructions, dailyHeader) || instructions == reviewInstructions
}

// notesWritten lists the notes a turn wrote, for the remark to point at.
func notesWritten(steps []stepRecord) []string {
	var paths []string
	for _, step := range steps {
		if (step.Type == "write_note" || step.Type == "write_schedule") && step.Error == "" && step.Path != "" {
			paths = append(paths, step.Path)
		}
	}
	return paths
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
	case instructions == reviewInstructions:
		return "review"
	case strings.HasPrefix(instructions, dailyHeader):
		return "daily"
	case instructions == scheduleInstructions:
		return "schedule"
	case instructions == forgottenInstructions:
		return "note-deleted"
	case instructions == deletedInstructions:
		return "post-deleted"
	case instructions == expiredInstructions:
		return "note-expired"
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
