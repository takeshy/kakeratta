package persona

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// rounds is how many requests one turn may make: every read, both drafts, and
// a little room for a reply that called nothing. It follows from the limits in
// workflow.go rather than being a limit of its own.
const rounds = maxReads + maxDrafts + maxMalformed + 2

// Runner wakes personas. One runs per app.
//
// Reactions are asynchronous by default: a post is stored and shown
// immediately, and whatever anyone has to say about it arrives afterwards. The
// queue is what makes that true.
type reaction struct {
	entryID   string
	edited    bool
	place     *timeline.Place
	daily     bool
	schedules bool
	// forgotten is a note the user deleted: the persona is told, so it can put
	// its own memory back in order. The app never edits a note itself.
	forgotten *forgotten
	// deleted is a post the user removed that a persona had cited.
	deleted *deletedPost
}

type forgotten struct {
	skill string
	paths []string
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

	// routingMu makes a read-modify-write on one post's routing record atomic.
	// keepRouting, turnsDone and FinishAsyncTurn each read the record and write
	// back a change; Hermes reporting back over MCP runs on its own request,
	// concurrently with the wave that delivered it, so without this a write
	// racing the other could read a stale copy and silently drop the other
	// persona's just-recorded reply.
	routingMu sync.Mutex
}

func New(s store.Workspace, catalogue *Catalogue, clientOf func() (*llm.Client, store.Settings)) *Runner {
	r := &Runner{
		store: s, catalogue: catalogue, clientOf: clientOf,
		search: search.New(s),
		emit:   func(string, any) {},
		done:   make(chan struct{}),
	}
	r.wake = sync.NewCond(&r.stopMu)
	return r
}

// SetSearch shares the UI's indexing work with all personas. Set before Start.
func (r *Runner) SetSearch(service *search.Service) { r.search = service }

// SetTools is the operator's tool configuration: search key, map endpoints and
// MCP servers. Set before Start.
func (r *Runner) SetTools(config tools.Config) { r.tools = config }

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
		r.started = true
		go func() {
			defer close(r.done)
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
				if item.daily {
					r.dailyAt(time.Now())
				} else if item.schedules {
					r.schedulesAt(time.Now())
				} else if item.forgotten != nil {
					r.forgottenAt(*item.forgotten)
				} else if item.deleted != nil {
					r.deletedAt(*item.deleted)
				} else {
					r.reactEntry(item.entryID, item.place, item.edited)
				}
			}
		}()
	})
}

// Stop lets the reactions in hand finish and then returns.
//
// A reaction writes as it goes - a remark, a persona's notes - so killing one
// half way through leaves the workspace mid-thought. There is at most one in
// flight, and shutdown drains everything already accepted.
func (r *Runner) Stop() {
	r.stopMu.Lock()
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
	r.Start()
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped {
		return
	}
	r.queue = append(r.queue, reaction{entryID: entryID, place: place, edited: edited})
	r.wake.Signal()
}

func (r *Runner) react(entryID string) {
	r.reactAt(entryID, nil)
}

func (r *Runner) reactAt(entryID string, transientPlace *timeline.Place) {
	r.reactEntry(entryID, transientPlace, false)
}

// keepRouting writes down what the router decided about a post, for a person
// to look at later. What is kept is the router's own call: a persona reached
// by name is left out of the chosen, and anyone who got the post for whatever
// reason is left out of the passed-over. Nothing is kept when the router had
// no answer - "no record" and "nobody" are different things to read.
func (r *Runner) keepRouting(entry timeline.Entry, routing Routing, named []string, targets map[string]bool) {
	entryID := entry.ID
	r.routingMu.Lock()
	defer r.routingMu.Unlock()
	if !routing.Answered {
		if err := r.store.ForgetRouting(entryID); err != nil {
			r.emit("error", map[string]any{"where": "routing", "message": err.Error()})
		}
		return
	}
	record := store.Routing{Delivered: []string{}, Excluded: map[string]string{}, EntryText: &entry.Text, Tracked: true, Done: []string{}, Spoke: []string{}}
	for _, id := range routing.Deliver {
		if !contains(named, id) {
			record.Delivered = append(record.Delivered, id)
		}
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

// FinishAsyncTurn is turnsDone for a persona whose turn was left pending
// because its real work happens outside this process - Hermes, replying
// later through its own agent over MCP. Called once that work has actually
// concluded, so a routing sheet that has been saying "still thinking" can
// move on to what really happened.
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
	Error     string `json:"error,omitempty"`
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
	for _, skill := range skills {
		if skill.ID == skillID {
			found = true
		}
	}
	if !found {
		return RoutingCheck{}, errors.New("その担当は有効ではありません")
	}
	entries, err := r.store.Timeline()
	if err != nil {
		return RoutingCheck{}, err
	}
	thread := timeline.Thread(entries, timeline.RootOf(entry))
	routing := routeConfigured(ctx, client, settings, skills, thread, entry)
	check := RoutingCheck{Answered: routing.Answered, Error: routing.Error}
	if routing.Answered {
		check.Delivered = contains(routing.Deliver, skillID)
		check.Reason = routing.Excluded[skillID]
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

	entries, err := r.store.Timeline()
	if err != nil {
		return
	}
	var entry timeline.Entry
	for _, candidate := range entries {
		if candidate.ID == entryID {
			entry = candidate
		}
	}
	if entry.ID == "" || entry.Deleted {
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
		entries, _ = r.store.Timeline()
	}

	skills := r.catalogue.Active(settings.Active)
	if len(skills) == 0 {
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
	targets := map[string]bool{}
	for _, skill := range skills {
		if previous[skill.ID] {
			targets[skill.ID] = true
		}
	}
	for _, id := range named {
		targets[id] = true
	}
	if parent, ok := r.store.Entry(entry.ReplyTo); ok && parent.Speaker == "hermes" {
		for _, skill := range skills {
			if skill.ID == "hermes" {
				targets[skill.ID] = true
			}
		}
	}
	routing := routeConfigured(ctx, client, settings, skills, thread, entry)
	routing = hermesAsQuestionFallback(routing, targets)
	for _, id := range routing.Deliver {
		targets[id] = true
	}
	r.keepRouting(entry, routing, named, targets)
	if len(targets) == 0 {
		r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": []string{}})
		return
	}
	r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": keys(targets)})

	// Everyone answers on Super. A reply that asks a persona's answer to be
	// better or deeper - "違う", "もっと詳しく" - is answered on Ultra instead:
	// the larger model is what the user reaches for, not what a persona is.
	deep := false
	if parent, ok := r.store.Entry(entry.ReplyTo); ok && entry.Kind == timeline.KindReply && parent.Speaker != timeline.User {
		deep = Escalate(ctx, client, lightningModel(settings), parent, entry)
	}

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
	// pending marks a persona whose real turn happens later, out of process
	// (Hermes, queued as a durable task); such a turn must not be counted as
	// done yet, or a routing sheet would call it quiet before it has started.
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
			if skill.ID == "hermes" {
				remarks[i], pending[i] = r.queueHermes(ctx, entry, instructions)
				return
			}
			remarks[i] = r.run(ctx, skill, thread, entry, contains(named, skill.ID), false, instructions, deep, client, settings, registry)
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

// Hermes is the catch-all for questions that none of the focused personas
// own. The router still decides whether the post is a question (from Hermes's
// domain), but this final comparison is deliberately done here: persona
// conditions are otherwise classified independently, so Hermes cannot know
// reliably whether another condition matched. An explicit wake already in
// targets (a mention, a reply to Hermes, or an edited prior delivery) wins.
func hermesAsQuestionFallback(routing Routing, targets map[string]bool) Routing {
	if targets["hermes"] || !contains(routing.Deliver, "hermes") {
		return routing
	}
	other := false
	for id := range targets {
		if id != "hermes" {
			other = true
			break
		}
	}
	if !other {
		for _, id := range routing.Deliver {
			if id != "hermes" {
				other = true
				break
			}
		}
	}
	if !other {
		return routing
	}
	deliver := make([]string, 0, len(routing.Deliver)-1)
	for _, id := range routing.Deliver {
		if id != "hermes" {
			deliver = append(deliver, id)
		}
	}
	routing.Deliver = deliver
	if routing.Excluded == nil {
		routing.Excluded = map[string]string{}
	}
	routing.Excluded["hermes"] = "ほかの担当に割り当てられたため"
	return routing
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

const systemTemplate = `あなたは「%s」。%s

あなたの担当領域:
%s

あなた自身への指示:
%s

あなたのメモの分類（型はここにあるものだけを使います）:
%s

規則:
- **黙ってよい。** 全部に反応する必要はありません。誰も反応しない投稿があって構いません。
  解説するためのアプリではなく、ただ眺めていたいだけのこともあります。押し付けないでください。
- **喋らないことと覚えないことは別です。** 黙る場合でも、自分の領域のことならメモには書いてください。
- 渡されるのはこの投稿と、それに付いた発言だけです。**それ以外の過去は、メモや timeline_search で必要な分だけ調べてください。**
  次に必要になりそうなことは、いまメモに書いてください。
- **今回の対象発言に、スレッド全体の文脈を踏まえて対応してください。** ユーザーの追加発言は、元の投稿や担当の質問への補足・訂正・回答として読んでください。
  例: 「渋谷にいきたい」の後の「1時間後。」は渋谷への移動の時間に関する補足です。行き先を再度尋ねず、既知の条件と合わせて答えてください。
  出発時刻か到着時刻かなど、文脈でも特定できない点だけを確認してください。相対時刻は対象発言の日時を基準に解釈してください。
  ユーザーの訂正や追加条件を優先し、担当の推測をユーザーが決めた条件として扱わないでください。
  過去の回答があっても、追加条件で答えが変わる場合は更新して回答してください。
- **メモには tags を付けます。** frontmatter の tags に、探すときの手がかりになる語を2〜5つ（産地・品種・色・店の種類・主題など）。
  メモ一覧にはタグが出ます。探すときは notes_list を args の {"tag":"…"} で絞れます。
- **同じものにメモは1つ。** 書く前にメモ一覧と index.md を見て、同じ銘柄・店・本・出来事のメモがあれば、新しく作らずその path を書き直します。
  綴りの揺れや読み取りの誤りは新しいメモの理由になりません。同じ題名のメモを別の path に作った workflow は本体が差し戻します。
  型（type）が違っても、対象が同じなら1つのメモです。型は「メモの分類」にあるものだけを使い、
  「説明」「情報」のような書き方の種類を型にしません。分類に無い型のメモは本体が差し戻します。
- **メモはあなたが自分で読み書きします。** 下の workflow のステップで行います。何を概念にするか、
  何を残して何を捨てるか、いつまとめるかは、すべてあなたが決めます。
  本体は中身を読みませんし、数や長さの上限も持ちません。
- メモでは引用内容、本人の意見、他の担当の推測を区別し、推測は推測と明記してください。
  複数の記録を統合しても、後から訂正できるよう代表的な根拠と例外を少し残してください。
%s
- **発言は投稿と同じ言語で。** 日本語で書かれた投稿には日本語で、英語なら英語で返してください。
  投稿に文章がない（画像だけなど）ときは、あなたの担当条件と指示が書かれている言語で話してください。
  名前や口調はそのままで構いません。
- 他の担当の発言がすでにあるときは、繰り返さず、拾うか、別の角度から言ってください。

%s

---

%s`

// briefRule is how long a remark is, ordinarily.
const briefRule = "- 発言するときは短く。前に自分が覚えていたことと結びつくなら、それを言ってください。"

// deepRule replaces it when the user replied asking for a better or deeper
// answer than the one they got.
const deepRule = `- **今回ユーザーは、前の回答より良い・深い回答を求めています。** 短くまとめる必要はありません。
  前の回答を見直し、誤りや不足があれば認めて正してください。必要なら調べ直してください。
  根拠・比較・具体例を添え、求められた深さに見合うだけ詳しく答えてください。口調はそのままで構いません。
  発言は Markdown で書いてください。
  前に自分が覚えていたことと結びつくなら、それも言ってください。`

// run wakes one persona and hands it the post. What to read, what to write and
// whether to say anything are still the persona's; it says so as a workflow,
// the app has the workflow checked, and only then does anything happen.
func (r *Runner) run(ctx context.Context, skill Skill, thread []timeline.Entry, trigger timeline.Entry, named, listening bool,
	wakeInstructions string, deep bool, client *llm.Client, settings store.Settings, registry *tools.Registry) *timeline.Entry {
	if skill.ID == "hermes" {
		if listening {
			return nil
		}
		remark, _ := r.queueHermes(ctx, trigger, wakeInstructions)
		return remark
	}

	root := timeline.RootOf(trigger)
	daily := trigger.ID == ""
	// deep is a reply asking for a better or deeper answer: Ultra, and a
	// remark allowed to be as long as the answer needs.
	model := personaModel(settings)
	if deep {
		model = ultraModel(settings)
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return nil
	}
	if trigger.ID != "" {
		// Who was woken for this post, so that deleting it can tell them - the
		// app never looks inside a note to work that out.
		if err := r.store.RecordWake(root, skill.ID); err != nil {
			r.emit("error", map[string]any{"where": "wakes", "message": err.Error()})
		}
	}
	memory := r.store.Memory(skill.ID)
	if memory.JoinedAt.IsZero() {
		memory.JoinedAt = time.Now()
	}
	set := registry.For(skill.Tools).As(skill.ID)
	spec := workflowSpec(set, deep)
	layout := strings.TrimSpace(skill.Memory)
	if layout == "" {
		layout = "（指示に書かれたとおり。対象の種類ごとに型を1つ決め、同じ対象は型が違っても1つのメモにします）"
	}
	length := briefRule
	if deep {
		length = deepRule
	}
	system := fmt.Sprintf(systemTemplate, skill.Name, skill.Tone, skill.Domain, skill.Instructions, layout, length, spec, okfHost+okfSkill)
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
	user := strings.Join([]string{
		"いまの日時: " + time.Now().In(here).Format("2006-01-02 15:04 MST"),
		placeLine(post.Place),
		"",
		"あなたのメモ（一覧。中身は notes_read で読む）:",
		listNotes(memory),
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
		turnLine(named, listening),
	}, "\n")

	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	rules := turnRules{named: named, listening: listening, noteRequired: trigger.ID != "" && !listening, set: set, schedules: scheduleIDs, notes: memory.Notes, memory: skill.Memory, cited: map[string]string{}}
	// The triggering entry is known by the host; no model-supplied ID is needed.
	label := []rune(strings.Join(strings.Fields(trigger.Text), " "))
	if len(label) > 140 {
		label = append(label[:140], '…')
	}
	prov := newProvenance()
	if !daily {
		prov = newProvenance(store.NoteSource{EntryID: trigger.ID, Label: trigger.At.In(here).Format("2006-01-02 15:04 MST") + " — " + string(label)})
	}

	record := runRecord{Persona: skill.ID, Trigger: trigger.ID, Wake: wakeKind(trigger, listening, wakeInstructions), Path: "workflow", Model: model, Reviewer: reviewModel()}
	defer func() { logRun(record) }()
	failed := func(err error) *timeline.Entry {
		record.Error = err.Error()
		r.emit("error", map[string]any{"persona": skill.ID, "message": err.Error()})
		if named && !listening {
			return r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID, nil)
		}
		return r.hush(skill, memory)
	}

	// What the persona read goes to the reviewer too: whether a note says
	// something that did not happen can only be judged against what was there.
	var readSources []timeline.Source
	// The index was handed over at the start, so it counts as read: the review
	// should not call an index rewrite unread.
	read := []string{"index.md（最初に渡したもの）:\n" + indexNote}
	failures := map[string]string{}
	budget := newLookups(maxReads)
	var approved *Workflow
	submit := workflowToolFor(set)
	reviewed, malformed := 0, 0
	for round := 0; round < rounds && approved == nil && reviewed < maxDrafts && malformed <= maxMalformed; round++ {
		offered := []llm.Tool{submit}
		if len(record.Reads) < maxReads {
			offered = []llm.Tool{readTool, submit}
		}
		reply, err := client.Complete(ctx, llm.Request{
			Model: model, Messages: messages, Tools: offered, ToolChoice: "required",
		})
		if err != nil {
			return failed(err)
		}
		if len(reply.Calls) == 0 {
			record.Stalls++
			messages = append(messages,
				llm.Message{Role: "assistant", Content: reply.Content},
				llm.Message{Role: "user", Content: "read か submit_workflow を呼んでください。文章だけの返事は誰にも届きません。"})
			continue
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: reply.Content, Calls: reply.Calls})
		var submitted *llm.ToolCall
		for i := range reply.Calls {
			if reply.Calls[i].Name == submitWorkflow && submitted == nil {
				submitted = &reply.Calls[i]
			}
		}
		answers := map[string]string{}
		if submitted != nil {
			var workflow Workflow
			entry := draftRecord{}
			if err := json.Unmarshal([]byte(submitted.Args), &workflow); err != nil {
				entry.Problems = []string{"workflow を読めませんでした: " + err.Error()}
			} else {
				workflow = normaliseWorkflow(workflow)
				entry.Workflow = &workflow
				entry.Problems = checkWorkflow(workflow, rules)
			}
			switch {
			case len(entry.Problems) > 0:
				malformed++
			case !needsReview(workflow, rules.set.Allows("schedule")):
				// Staying quiet, or only keeping a note and speaking, is not
				// worth a review call - unless the persona can schedule, whose
				// typical slip is a promised time with nothing set to wake.
				approved = &workflow
			default:
				known := "この担当が置いている schedule:\n" + scheduleList + "\n\nこの担当のメモ一覧:\n" + listNotes(memory) +
					"\n\nこの担当がこのターンに読んだもの:\n" + clip(strings.Join(read, "\n\n"), 20000)
				reviewed++
				verdict, err := r.review(ctx, client, settings, skill, thread, trigger, workflow, known, names, here)
				switch {
				case err != nil:
					entry.Unseen = err.Error()
					approved = &workflow
				case verdict.passed():
					entry.Review = verdict
					approved = &workflow
				default:
					entry.Review = verdict
					for _, issue := range verdict.Issues {
						if issue.Severity == "high" {
							entry.Problems = append(entry.Problems, issue.Description)
						}
					}
					if len(entry.Problems) == 0 {
						entry.Problems = []string{verdict.Summary}
					}
				}
			}
			record.Drafts = append(record.Drafts, entry)
			if approved == nil {
				answers[submitted.ID] = "この workflow は実行されませんでした。直して、もう一度 submit_workflow を呼んでください:\n- " + strings.Join(entry.Problems, "\n- ")
			} else {
				answers[submitted.ID] = "受け付けました。"
			}
		}
		for _, call := range reply.Calls {
			if _, done := answers[call.ID]; done {
				continue
			}
			switch {
			case submitted != nil:
				answers[call.ID] = "workflow と一緒に出された呼び出しは行いませんでした。"
			case call.Name != readTool.Name || len(record.Reads) >= maxReads:
				answers[call.ID] = "これ以上は読めません。submit_workflow で workflow を出してください。"
			default:
				var request struct {
					Steps []ReadStep `json:"steps"`
				}
				if err := json.Unmarshal([]byte(call.Args), &request); err != nil {
					answers[call.ID] = "read を読めませんでした: " + err.Error()
					break
				}
				text, sources := r.readAll(ctx, skill, request.Steps, set, post, prov, failures, budget, rules.cited)
				readSources = append(readSources, sources...)
				read = append(read, text)
				record.Reads = append(record.Reads, readRecord{Steps: request.Steps, Output: preview(text)})
				answers[call.ID] = text + readFooter(maxReads-len(record.Reads), set)
			}
		}
		for _, call := range reply.Calls {
			messages = append(messages, llm.Message{Role: "tool", CallID: call.ID, Content: answers[call.ID]})
		}
	}
	if approved == nil {
		return failed(errors.New("承認された workflow がありません"))
	}

	record.Ran = true
	spoken, sources, steps := r.execute(ctx, skill, *approved, set, rules, post, prov)
	record.Steps = steps
	record.Spoke = spoken
	if len(steps) > 0 && steps[len(steps)-1].Error != "" {
		record.Error = "stopped at " + steps[len(steps)-1].Type + ": " + steps[len(steps)-1].Error
	}
	if listening || spoken == "" {
		if named && !listening {
			return r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID, nil)
		}
		// It had its turn. Whatever it wrote down is kept either way.
		return r.hush(skill, memory)
	}
	return r.speak(skill, root, spoken, append(readSources, sources...), memory, trigger.ID, notesWritten(steps))
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
	case trigger.ID != "" && instructions == editedInstructions:
		return "post-edited"
	case trigger.ID != "":
		return "post"
	case instructions == dailyInstructions:
		return "daily"
	case instructions == scheduleInstructions:
		return "schedule"
	case instructions == forgottenInstructions:
		return "note-deleted"
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

func (r *Runner) speak(skill Skill, root, text string, sources []timeline.Source, memory store.Memory, replyTo string, notes []string) *timeline.Entry {
	memory.Wakes++
	_ = r.store.SaveMemory(memory)
	if replyTo == "" {
		replyTo = root
	}
	remark := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: skill.ID,
		Text: text, At: time.Now(), Root: root, ReplyTo: replyTo, Sources: dedupe(sources), Notes: notes,
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
			"覚えておくことがあればメモに書いてください。最後の speak の text は空にしてください。"
	case named:
		return "\n※ あなたは名指しで呼ばれています。最後の speak の text を空にはできません。必ず何か答えてください。"
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

// notesTool is a persona reaching into its own notes.
//
// It reports whether it took the call, so anything it does not recognise falls
// through to the capabilities the skill declared. The split is the point: a
// declared tool goes outside, and these do not go anywhere.
//
// The host carries text without interpreting it, and retains source links
// when a persona reads and consolidates its notes.
func (r *Runner) notesTool(personaID, name string, args map[string]any, prov *provenance) (string, bool) {
	switch name {
	case "notes_list":
		return listNotesTagged(r.store.Memory(personaID), stringOf(args["tag"])), true

	case "notes_read":
		wanted := stringsOf(args["paths"])
		if single := stringOf(args["path"]); single != "" {
			wanted = append(wanted, single)
		}
		if len(wanted) == 0 {
			return "paths がありません。", true
		}
		var parts []string
		for _, path := range wanted {
			note, ok := r.store.Note(personaID, path)
			if !ok {
				parts = append(parts, "=== "+path+" ===\n（ありません）")
				continue
			}
			parts = append(parts, "=== "+note.Path+" ===\n"+note.Text)
			prov.noteRead(note.Path, note.Text)
		}
		return strings.Join(parts, "\n\n"), true

	case "notes_write":
		path, text := stringOf(args["path"]), repairFrontmatter(repairNewlines(stringOf(args["text"])))
		if path == "" || strings.TrimSpace(text) == "" {
			return "path と text が要ります。", true
		}
		if why := duplicateNote(r.store.Memory(personaID).Notes, path, text); why != "" {
			return "書きませんでした: " + why, true
		}
		if skill, ok := r.catalogue.Find(personaID); ok {
			if why := typeNotAllowed(skill.Memory, path, text); why != "" {
				return "書きませんでした: " + why, true
			}
		}
		if err := r.store.WriteNote(personaID, path, text, prov.forWrite(path, text)...); err != nil {
			return "書けませんでした: " + err.Error(), true
		}
		prov.noteWritten(path)
		return "書きました: " + path, true

	case "notes_forget":
		path := stringOf(args["path"])
		if path == "" {
			return "path がありません。", true
		}
		if err := r.store.DeleteNote(personaID, path); err != nil {
			return "捨てられませんでした: " + err.Error(), true
		}
		// A note thrown away leaves its posts to the notes written this turn
		// in its place, so a consolidation keeps the trail to the originals.
		if sources, to := prov.passOn(path); len(to) > 0 {
			for _, written := range to {
				if note, ok := r.store.Note(personaID, written); ok {
					_ = r.store.WriteNote(personaID, written, note.Text, sources...)
				}
			}
		}
		prov.noteForgotten(path)
		return "捨てました: " + path, true
	}
	return "", false
}

// repairNewlines undoes a model that escaped its own line breaks. A note whose
// text has no real newline but plenty of "\n" is unreadable in the memory view
// and unparseable as OKF, and the persona cannot see the difference from where
// it sits.
func repairNewlines(text string) string {
	// Trimmed, because a blank line around an otherwise unbroken note would
	// otherwise pass for a line break and leave the damage in place.
	body := strings.TrimSpace(text)
	if strings.Contains(body, "\n") || !strings.Contains(body, `\n`) {
		return text
	}
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t").Replace(body)
}

// repairFrontmatter mends the two ways a model breaks a frontmatter block
// without noticing: indenting it, so it reads as prose and the type is lost;
// and gluing the closing fence to the last value ("+09:00---") or to the first
// line of the body ("---基本情報"), so the block never closes and the whole
// note reads as one broken header. A live run kept a wine that way.
func repairFrontmatter(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return text
	}
	lines[0] = "---"
	out := []string{"---"}
	closed := false
	for i := 1; i < len(lines); i++ {
		line := strings.TrimLeft(lines[i], " \t")
		if closed {
			out = append(out, lines[i])
			continue
		}
		switch {
		case line == "---":
			closed = true
		case strings.HasSuffix(line, "---") && len(line) > 3 && !strings.HasPrefix(line, "---"):
			// The fence at the end of a value: the value keeps its line.
			line = strings.TrimRight(strings.TrimSuffix(line, "---"), " \t")
			out = append(out, line)
			line = "---"
			closed = true
		case strings.HasPrefix(line, "---") && len(line) > 3:
			// The fence at the start of the body: the body keeps its line.
			out = append(out, "---")
			line = strings.TrimLeft(strings.TrimPrefix(line, "---"), " \t")
			closed = true
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// listNotes is the one thing about the notes that is handed over unasked: their
// names and how long each one is. It is what makes deciding what to read
// possible without a round trip just to find out what exists.
func listNotes(memory store.Memory) string {
	return listNotesTagged(memory, "")
}

// listNotesTagged is the listing narrowed to one tag, or the whole listing
// when tag is empty. Each line carries the note's tags, so a persona can pick
// what to read by them without the index having said so.
func listNotesTagged(memory store.Memory, tag string) string {
	if len(memory.Notes) == 0 {
		return "（まだ何もありません）"
	}
	var lines []string
	for _, note := range memory.Notes {
		tags := noteTags(note.Text)
		if tag != "" && !hasTag(tags, tag) {
			continue
		}
		line := fmt.Sprintf("- %s（%d字）", note.Path, len([]rune(note.Text)))
		if !note.Written.IsZero() {
			line += " updated: " + note.Written.Format(time.RFC3339)
		}
		if len(tags) > 0 {
			line += " tags: " + strings.Join(tags, ", ")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "タグ「" + tag + "」のメモはありません。"
	}
	return strings.Join(lines, "\n")
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
