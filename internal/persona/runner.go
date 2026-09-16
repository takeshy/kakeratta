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

// rounds is how many times one persona may go round the tool loop before it has
// to answer. It reads its own notes, may look something up or look at the
// picture, writes what it learned, and then speaks or stays quiet - so there
// has to be room for all of that without the loop becoming a place to live in.
const rounds = 8

// Runner wakes personas. One runs per app.
//
// Reactions are asynchronous by default: a post is stored and shown
// immediately, and whatever anyone has to say about it arrives afterwards. The
// queue is what makes that true, and it is also what keeps the light tier
// ahead of the heavy one.
type reaction struct {
	entryID   string
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
					r.reactAt(item.entryID, item.place)
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
	r.Start()
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped {
		return
	}
	r.queue = append(r.queue, reaction{entryID: entryID, place: place})
	r.wake.Signal()
}

func (r *Runner) react(entryID string) {
	r.reactAt(entryID, nil)
}

func (r *Runner) reactAt(entryID string, transientPlace *timeline.Place) {
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

	// Who gets it is the app's one decision about a post.
	named := Named(skills, entry.Text)
	targets := map[string]bool{}
	for _, id := range named {
		targets[id] = true
	}
	for _, id := range Route(ctx, client, harnessModel(settings), skills, thread, entry) {
		targets[id] = true
	}
	if len(targets) == 0 {
		r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": []string{}})
		return
	}
	r.emit("routed", map[string]any{"entryId": entry.ID, "deliver": keys(targets)})

	// The tiers run one after the other, and that is what makes the exchange
	// work: what the low one said is in front of the ones above it, so they have
	// something to pick up on. Running them together would leave the larger
	// models with nothing but the post.
	// The same tool set the wake paths get. Without this a persona reacting to a
	// post was offered schedule and then told it was not configured, which is
	// why plana wrote its times into a note as prose instead of placing them.
	registry := r.registry(settings)
	var ran []Skill
	spoke := map[Tier]bool{}
	for _, tier := range Tiers {
		var wave []Skill
		for _, skill := range skills {
			if targets[skill.ID] && skill.Model == tier {
				wave = append(wave, skill)
			}
		}
		if len(wave) == 0 {
			continue
		}
		// Within a tier there is nothing to wait for, so they go together.
		var group sync.WaitGroup
		remarks := make([]*timeline.Entry, len(wave))
		for i, skill := range wave {
			group.Add(1)
			go func(i int, skill Skill) {
				defer group.Done()
				defer func() { _ = recover() }()
				remarks[i] = r.run(ctx, skill, thread, entry, contains(named, skill.ID), false, "", client, settings, registry)
			}(i, skill)
		}
		group.Wait()
		ran = append(ran, wave...)
		for _, remark := range remarks {
			if remark != nil {
				thread = append(thread, *remark)
				spoke[tier] = true
			}
		}
	}

	// A persona that already had its turn still gets to hear what was said
	// above it - not speaking and not remembering are different things - but it
	// cannot answer. Otherwise the two of them would keep going without anyone
	// asking, and the timeline would fill up with a conversation that is not
	// yours.
	for _, skill := range ran {
		if !spokeAfter(spoke, skill.Model) {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			r.run(ctx, skill, thread, entry, false, true, "", client, settings, registry)
		}()
	}
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
- **メモはあなたが自分で読み書きします。** 下の workflow のステップで行います。何を概念にするか、
  何を残して何を捨てるか、いつまとめるかは、すべてあなたが決めます。
  本体は中身を読みませんし、数や長さの上限も持ちません。
- メモでは引用内容、本人の意見、他の担当の推測を区別し、推測は推測と明記してください。
  複数の記録を統合しても、後から訂正できるよう代表的な根拠と例外を少し残してください。
- 発言するときは短く。前に自分が覚えていたことと結びつくなら、それを言ってください。
- **発言は投稿と同じ言語で。** 日本語で書かれた投稿には日本語で、英語なら英語で返してください。
  名前や口調はそのままで構いません。
- 他の担当の発言がすでにあるときは、繰り返さず、拾うか、別の角度から言ってください。

%s

---

%s`

// run wakes one persona and hands it the post. What to read, what to write and
// whether to say anything are still the persona's; it says so as a workflow,
// the app has the workflow checked, and only then does anything happen.
func (r *Runner) run(ctx context.Context, skill Skill, thread []timeline.Entry, trigger timeline.Entry, named, listening bool,
	wakeInstructions string, client *llm.Client, settings store.Settings, registry *tools.Registry) *timeline.Entry {

	root := timeline.RootOf(trigger)
	daily := trigger.ID == ""
	model := modelFor(skill.Model, settings)
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
	system := fmt.Sprintf(systemTemplate, skill.Name, skill.Tone, skill.Domain, skill.Instructions, workflowGuide(set), okfHost+okfSkill)
	system += wakeInstructions

	names := map[string]string{}
	for _, other := range r.catalogue.All() {
		names[other.ID] = other.Name
	}
	post := thread[0]
	// The clock a persona is told is the user's, not the container's.
	here := settings.Location()
	scheduleList, scheduleIDs := r.scheduleListFor(skill, set)
	user := strings.Join([]string{
		"いまの日時: " + time.Now().In(here).Format("2006-01-02 15:04 MST"),
		placeLine(post.Place),
		"",
		"あなたのメモ（一覧。中身は notes_read で読む）:",
		listNotes(memory),
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
	rules := turnRules{named: named, listening: listening, set: set, schedules: scheduleIDs}
	// The triggering entry is known by the host; no model-supplied ID is needed.
	label := []rune(strings.Join(strings.Fields(trigger.Text), " "))
	if len(label) > 140 {
		label = append(label[:140], '…')
	}
	var noteSources []store.NoteSource
	if !daily {
		noteSources = []store.NoteSource{{EntryID: trigger.ID, Label: trigger.At.In(here).Format("2006-01-02 15:04 MST") + " — " + string(label)}}
	}

	record := runRecord{Persona: skill.ID, Trigger: trigger.ID, Wake: wakeKind(trigger, listening, wakeInstructions), Model: model, Reviewer: harnessModel(settings)}
	defer func() { logRun(record) }()
	failed := func(err error) *timeline.Entry {
		record.Error = err.Error()
		r.emit("error", map[string]any{"persona": skill.ID, "message": err.Error()})
		if named && !listening {
			return r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID)
		}
		return r.hush(skill, memory)
	}

	// What the persona read goes to the reviewer too: whether a note says
	// something that did not happen can only be judged against what was there.
	var readSources []timeline.Source
	var read []string
	var files []llm.Attachment
	var approved *Workflow
	drafts := 0
	for round := 0; round < rounds && approved == nil && drafts < maxDrafts; round++ {
		offered := []llm.Tool{workflowTool}
		if len(record.Reads) < maxReads {
			offered = []llm.Tool{readTool, workflowTool}
		}
		reply, err := client.Complete(ctx, llm.Request{
			Model: model, Messages: messages, Files: files, Tools: offered, ToolChoice: "required",
		})
		files = nil
		if err != nil {
			return failed(err)
		}
		if len(reply.Calls) == 0 {
			messages = append(messages,
				llm.Message{Role: "assistant", Content: reply.Content},
				llm.Message{Role: "user", Content: "read か submit_workflow を呼んでください。文章だけの返事は誰にも届きません。"})
			continue
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: reply.Content, Calls: reply.Calls})
		var submitted *llm.ToolCall
		for i := range reply.Calls {
			if reply.Calls[i].Name == workflowTool.Name && submitted == nil {
				submitted = &reply.Calls[i]
			}
		}
		answers := map[string]string{}
		if submitted != nil {
			drafts++
			var workflow Workflow
			entry := draftRecord{}
			if err := json.Unmarshal([]byte(submitted.Args), &workflow); err != nil {
				entry.Problems = []string{"workflow を読めませんでした: " + err.Error()}
			} else {
				entry.Workflow = &workflow
				entry.Problems = checkWorkflow(workflow, rules)
			}
			switch {
			case len(entry.Problems) > 0:
			case len(workflow.Steps) == 0:
				// Staying quiet has nothing in it to check.
				approved = &workflow
			default:
				known := "この担当が置いている schedule:\n" + scheduleList + "\n\nこの担当のメモ一覧:\n" + listNotes(memory) +
					"\n\nこの担当がこのターンに読んだもの:\n" + clip(strings.Join(read, "\n\n"), 12000)
				if len(read) == 0 {
					known = "この担当が置いている schedule:\n" + scheduleList + "\n\nこの担当のメモ一覧:\n" + listNotes(memory) +
						"\n\nこの担当がこのターンに読んだもの:\n（何も読んでいません）"
				}
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
				text, sources, attached := r.readAll(ctx, skill, request.Steps, set, post, &noteSources)
				readSources = append(readSources, sources...)
				if attached != nil {
					files = attached
				}
				read = append(read, text)
				record.Reads = append(record.Reads, readRecord{Steps: request.Steps, Output: preview(text)})
				answers[call.ID] = text
			}
		}
		for _, call := range reply.Calls {
			messages = append(messages, llm.Message{Role: "tool", CallID: call.ID, Content: answers[call.ID]})
		}
		if files != nil {
			// A picture travels with a user message, so it gets one of its own.
			messages = append(messages, llm.Message{Role: "user", Content: "元の画像です。"})
		}
	}
	if approved == nil {
		return failed(errors.New("承認された workflow がありません"))
	}

	record.Ran = true
	spoken, sources, steps := r.execute(ctx, skill, *approved, set, rules, post, &noteSources)
	record.Steps = steps
	record.Spoke = spoken
	if len(steps) > 0 && steps[len(steps)-1].Error != "" {
		record.Error = "stopped at " + steps[len(steps)-1].Type + ": " + steps[len(steps)-1].Error
	}
	if listening || spoken == "" {
		if named && !listening {
			return r.speak(skill, root, "今はうまく答えられませんでした。", nil, memory, trigger.ID)
		}
		// It had its turn. Whatever it wrote down is kept either way.
		return r.hush(skill, memory)
	}
	return r.speak(skill, root, spoken, append(readSources, sources...), memory, trigger.ID)
}

// wakeKind names why a persona was woken, for the log.
func wakeKind(trigger timeline.Entry, listening bool, instructions string) string {
	switch {
	case trigger.ID != "" && listening:
		return "listening"
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

func (r *Runner) speak(skill Skill, root, text string, sources []timeline.Source, memory store.Memory, replyTo string) *timeline.Entry {
	memory.Wakes++
	_ = r.store.SaveMemory(memory)
	if replyTo == "" {
		replyTo = root
	}
	remark := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: skill.ID,
		Text: text, At: time.Now(), Root: root, ReplyTo: replyTo, Sources: dedupe(sources),
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
			"覚えておくことがあればメモに書いてください。workflow に speak は入れられません。"
	case named:
		return "\n※ あなたは名指しで呼ばれています。workflow に when の付かない speak を必ず入れてください。"
	}
	return ""
}

// modelFor is which model answers for a tier. An unset tier falls back to the
// one below it, so a single-model setup works - it just gives up the contrast.
func modelFor(tier Tier, settings store.Settings) string {
	switch tier {
	case TierHigh:
		return firstSet(settings.HighModel, settings.MiddleModel, settings.LowModel)
	case TierMiddle:
		return firstSet(settings.MiddleModel, settings.LowModel)
	default:
		return strings.TrimSpace(settings.LowModel)
	}
}

func firstSet(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// spokeAfter reports whether anything was said by a tier that runs later than
// this one.
func spokeAfter(spoke map[Tier]bool, tier Tier) bool {
	for other, said := range spoke {
		if said && other.after(tier) {
			return true
		}
	}
	return false
}

func dedupe(sources []timeline.Source) []timeline.Source {
	seen := map[string]bool{}
	var out []timeline.Source
	for _, source := range sources {
		if source.URL == "" || seen[source.URL] {
			continue
		}
		seen[source.URL] = true
		out = append(out, source)
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
func (r *Runner) notesTool(personaID, name string, args map[string]any, sources *[]store.NoteSource) (string, bool) {
	switch name {
	case "notes_list":
		return listNotes(r.store.Memory(personaID)), true

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
			*sources = append(*sources, store.NoteSources(note.Text)...)
		}
		return strings.Join(parts, "\n\n"), true

	case "notes_write":
		path, text := stringOf(args["path"]), repairFrontmatter(repairNewlines(stringOf(args["text"])))
		if path == "" || strings.TrimSpace(text) == "" {
			return "path と text が要ります。", true
		}
		if err := r.store.WriteNote(personaID, path, text, (*sources)...); err != nil {
			return "書けませんでした: " + err.Error(), true
		}
		return "書きました: " + path, true

	case "notes_forget":
		path := stringOf(args["path"])
		if path == "" {
			return "path がありません。", true
		}
		if err := r.store.DeleteNote(personaID, path); err != nil {
			return "捨てられませんでした: " + err.Error(), true
		}
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

// repairFrontmatter drops the indentation a model sometimes puts in front of
// the frontmatter block. Indented, it is not frontmatter at all: the note reads
// as prose and its type is lost.
func repairFrontmatter(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return text
	}
	lines[0] = "---"
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimLeft(lines[i], " \t")
		if lines[i] == "---" {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// listNotes is the one thing about the notes that is handed over unasked: their
// names and how long each one is. It is what makes deciding what to read
// possible without a round trip just to find out what exists.
func listNotes(memory store.Memory) string {
	if len(memory.Notes) == 0 {
		return "（まだ何もありません）"
	}
	var lines []string
	for _, note := range memory.Notes {
		lines = append(lines, fmt.Sprintf("- %s（%d字）", note.Path, len([]rune(note.Text))))
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
