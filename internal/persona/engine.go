package persona

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/llm"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/store"
	"github.com/takeshy/dokking/internal/websearch"
)

// How long an event waits before the personas are woken.
//
// A page event waits longer than a remark: the reader turning pages is not
// asking for anything, and a companion that fires on every screen is only
// noise. A remark waits a little too - the delay is what makes the reply arrive
// the way a message from someone else reading alongside would.
const (
	pageDelay   = 20 * time.Second
	replyDelay  = 2 * time.Second
	notesKept   = 24
	historyKept = 8

	// freshRunes is how much newly read text one wake-up is given. Past that it
	// is the tail: what the persona has not caught up on yet stays for the next
	// event, and its own notes carry the rest.
	freshRunes = 6000
)

// Trigger is why a persona woke up.
type Trigger string

const (
	TriggerPage Trigger = "page"
	TriggerSaid Trigger = "said"
	TriggerMemo Trigger = "memo"
)

// Runner wakes personas on events and puts them back to sleep.
//
// There is no resident process and no per-persona goroutine: state is read from
// disk, one model call happens, state is written back. Nothing runs while the
// reader is not reading.
type Runner struct {
	store     *store.Store
	catalogue *Catalogue
	clientOf  func() (*llm.Client, store.Settings)
	emit      func(name string, data any)

	mu      sync.Mutex
	pending map[string]*time.Timer
	busy    map[string]bool
}

func NewRunner(s *store.Store, catalogue *Catalogue, clientOf func() (*llm.Client, store.Settings)) *Runner {
	return &Runner{
		store: s, catalogue: catalogue, clientOf: clientOf,
		emit:    func(string, any) {},
		pending: map[string]*time.Timer{},
		busy:    map[string]bool{},
	}
}

// OnSpoke is how the reading screen hears that a persona said something.
func (r *Runner) OnSpoke(emit func(name string, data any)) {
	if emit != nil {
		r.emit = emit
	}
}

// OnPage is the reader having read something. It is the event that keeps a
// persona level with the reader, so that when they finally ask a question the
// answer is already informed rather than read up on the spot.
func (r *Runner) OnPage(bookID string) { r.schedule(bookID, TriggerPage, "", pageDelay) }

// OnSaid is the reader having said something in the thread.
func (r *Runner) OnSaid(bookID, text string) { r.schedule(bookID, TriggerSaid, text, replyDelay) }

// OnMemo is the reader having written a memo, which is the event most worth
// reacting to: "this one's suspicious" deserves an answer.
func (r *Runner) OnMemo(bookID, text string) { r.schedule(bookID, TriggerMemo, text, replyDelay) }

func (r *Runner) schedule(bookID string, trigger Trigger, text string, delay time.Duration) {
	key := bookID + "|" + string(trigger)
	r.mu.Lock()
	defer r.mu.Unlock()
	if timer, queued := r.pending[key]; queued {
		timer.Stop()
	}
	r.pending[key] = time.AfterFunc(delay, func() {
		r.mu.Lock()
		delete(r.pending, key)
		r.mu.Unlock()
		r.wake(bookID, trigger, text)
	})
}

// Cancel stops anything queued for a book, for when it is deleted.
func (r *Runner) Cancel(bookID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, timer := range r.pending {
		if strings.HasPrefix(key, bookID+"|") {
			timer.Stop()
			delete(r.pending, key)
		}
	}
}

func (r *Runner) wake(bookID string, trigger Trigger, text string) {
	r.mu.Lock()
	if r.busy[bookID] {
		r.mu.Unlock()
		return
	}
	r.busy[bookID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.busy, bookID)
		r.mu.Unlock()
	}()

	b, err := r.store.LoadBook(bookID)
	if err != nil || !b.Category.Valid() {
		return
	}
	state := r.store.State(bookID)
	if len(state.Personas) == 0 {
		return
	}
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" || settings.ChatModel == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	for _, id := range state.Personas {
		plugin, ok := r.catalogue.Find(id)
		if !ok || !plugin.Suits(b.Category) {
			continue
		}
		r.run(ctx, plugin, b, state, trigger, text, client, settings)
	}
}

func (r *Runner) run(ctx context.Context, plugin Plugin, b *book.Book, state book.ReaderState, trigger Trigger, said string, client *llm.Client, settings store.Settings) {
	memory := r.store.PersonaState(b.ID, plugin.ID)
	k := r.store.Knowledge(b.ID)
	boundary := state.Boundary()

	material, anchor, ok := r.material(ctx, plugin, b, state, k, trigger, settings)
	if !ok {
		return
	}

	prompt := novelSystem
	if b.Category == book.CategoryTechnical {
		prompt = technicalSystem
	}
	user := strings.Join(nonEmpty(
		"この本: "+b.Title,
		occasion(trigger, said),
		"",
		"あなたのこれまでのメモ:",
		noteLines(memory.Notes),
		"",
		material,
		"",
		"読者との直近のやりとり:",
		r.historyLines(b.ID),
	), "\n")

	reply, err := client.Chat(ctx, llm.Request{
		Model: settings.ChatModel, JSON: true,
		Messages: []llm.Message{
			{Role: "system", Content: prompt + "\n\nあなたの人物像:\n" + plugin.Tone},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return
	}
	var parsed struct {
		Notes []string `json:"notes"`
		Speak string   `json:"speak"`
	}
	if err := llm.DecodeJSON(reply, &parsed); err != nil {
		return
	}

	now := time.Now()
	for _, note := range parsed.Notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		memory.Notes = append(memory.Notes, store.PersonaNote{Text: note, Position: anchor, At: now})
	}
	if len(memory.Notes) > notesKept {
		memory.Notes = memory.Notes[len(memory.Notes)-notesKept:]
	}
	if trigger == TriggerPage {
		memory.LastProcessed = boundary
	}
	_ = r.store.SavePersonaState(memory)

	// Silence is the default and not a failure. Most pages are just pages.
	speak := strings.TrimSpace(parsed.Speak)
	if speak == "" {
		return
	}
	turns := r.store.Conversation(b.ID)
	turns = append(turns, store.Turn{
		ID: store.NewID("turn"), Role: "assistant", Speaker: plugin.ID,
		Mode: "persona", Text: speak, Anchor: anchor, Boundary: boundary, At: now,
	})
	if err := r.store.SaveConversation(b.ID, turns); err != nil {
		return
	}
	r.emit("persona", map[string]any{"bookId": b.ID, "personaId": plugin.ID, "name": plugin.Name})
}

// material is what this persona is given to look at, and it is the whole of the
// boundary mechanism: a novel persona is handed the stretch the reader just
// read and the summaries behind it, and a technical one is handed the whole
// book plus whatever the web says about the page in front of the reader.
func (r *Runner) material(ctx context.Context, plugin Plugin, b *book.Book, state book.ReaderState, k knowledge.Knowledge, trigger Trigger, settings store.Settings) (string, position.Position, bool) {
	if b.Category == book.CategoryTechnical {
		return r.technicalMaterial(ctx, plugin, b, state, k, trigger, settings)
	}
	boundary := state.Boundary()
	memory := r.store.PersonaState(b.ID, plugin.ID)
	from := memory.LastProcessed
	if from.After(boundary) {
		from = boundary
	}
	// Only what the reader stayed on: a stretch they flicked past is not
	// something the companion beside them read either.
	fresh := b.ReadTextSince(state, from, 0)
	if runes := []rune(fresh); len(runes) > freshRunes {
		fresh = string(runes[len(runes)-freshRunes:])
	}
	if trigger == TriggerPage && strings.TrimSpace(fresh) == "" {
		// Nothing new to read. Waking up to say nothing would only cost a call.
		return "", position.Position{}, false
	}
	parts := []string{
		"ここまでの章要約（あなたが読んだ範囲）:",
		knowledge.SummaryLines(k.SummariesUpTo(boundary)),
		"",
		"読者が新しく読んだところ:",
		fresh,
	}
	return strings.Join(nonEmpty(parts...), "\n"), boundary, true
}

func (r *Runner) technicalMaterial(ctx context.Context, plugin Plugin, b *book.Book, state book.ReaderState, k knowledge.Knowledge, trigger Trigger, settings store.Settings) (string, position.Position, bool) {
	current := state.Current
	page := b.TextUpTo(current, b.At(current.Absolute+pageRunesTechnical))
	parts := []string{
		"この本の章要約（全体）:",
		knowledge.SummaryLines(k.Summaries),
		"",
		"読者がいま開いているページ:",
		page,
	}
	if trigger == TriggerPage {
		// "This may be out of date" is worse than silence when it is wrong, so
		// the page event only fires on the kinds of statement that go stale.
		terms := stale(page)
		if len(terms) == 0 {
			return "", position.Position{}, false
		}
		if !plugin.WebSearch {
			return "", position.Position{}, false
		}
		search := websearch.New(settings.Search.Provider, settings.Search.BaseURL, settings.Search.APIKey)
		if !search.Enabled() {
			return "", position.Position{}, false
		}
		results, err := search.Search(ctx, strings.Join(terms, " ")+" 最新 バージョン 非推奨", 5)
		if err != nil || len(results) == 0 {
			return "", position.Position{}, false
		}
		parts = append(parts,
			"",
			"このページに出てくる「"+strings.Join(terms, "・")+"」について、いま Web にある情報:",
			websearch.Lines(results),
		)
	}
	return strings.Join(nonEmpty(parts...), "\n"), current, true
}

// pageRunesTechnical is how much of the current page a technical persona looks
// at. It reacts to what is in front of the reader, not to the whole chapter.
const pageRunesTechnical = 1500

var stalePattern = regexp.MustCompile(
	`[A-Za-z][A-Za-z0-9_.+#-]{1,30}\s*(?:v(?:er(?:sion)?)?\.?\s*)?\d+\.\d+(?:\.\d+)?` +
		`|\bv\d+\.\d+(?:\.\d+)?\b` +
		`|非推奨|廃止予定|サポート終了|deprecated|obsolete|end of life`)

// stale picks out the statements that go out of date: version numbers, and the
// words a book uses when it says something is on its way out.
func stale(text string) []string {
	found := stalePattern.FindAllString(text, -1)
	seen := map[string]bool{}
	var out []string
	for _, term := range found {
		term = strings.TrimSpace(term)
		if term == "" || seen[strings.ToLower(term)] {
			continue
		}
		seen[strings.ToLower(term)] = true
		out = append(out, term)
		if len(out) == 3 {
			break
		}
	}
	return out
}

const novelSystem = `あなたは読者と一緒に小説を読んでいる相棒です。読者が読み終えたところまでしか渡されていません。

厳守:
- 渡された抜粋と自分のメモだけが、あなたがこの本について知っていることのすべてです。
- 学習で知っているかもしれない作品の結末・正体・展開を絶対に持ち出さない。
- 先を匂わせる言い方（「後で分かるが」「実は」）をしない。
- **黙るのが既定です。** 読者の読書を中断してでも言う価値があるときだけ発言してください。
  毎回何か言う相手は鬱陶しいだけです。何も言わない回があるのが正常です。
- 発言するなら短く（2文まで）。要約の繰り返しはしない。
- メモには、後から自分が思い出せる形で気づいたことを書く。位置はアプリが記録します。

{"notes":["内部メモ（0〜2件）"],"speak":"発言。黙るときは空文字"} の形式で返すこと。`

const technicalSystem = `あなたは読者と一緒に技術書を読んでいる相棒です。本の全体を読んでおり、先のページを踏まえて構いません。

厳守:
- **黙るのが既定です。** 言う価値があるときだけ発言してください。
- 「古いかもしれない」は、外れると黙っているより有害です。根拠は渡された検索結果からのみ出し、
  自分の記憶から固有名詞やバージョン番号を出さないこと。
- 発言するときは必ず出典のURLを本文に含めること。検索結果がないなら発言しない。
- 発言は短く（3文まで）。

{"notes":["内部メモ（0〜2件）"],"speak":"発言。黙るときは空文字"} の形式で返すこと。`

func occasion(trigger Trigger, said string) string {
	switch trigger {
	case TriggerSaid:
		return "読者がいま言ったこと: " + said
	case TriggerMemo:
		return "読者がいまメモに書いたこと: " + said
	default:
		return "きっかけ: 読者がページを読み進めました"
	}
}

func noteLines(notes []store.PersonaNote) string {
	var lines []string
	for _, note := range notes {
		lines = append(lines, fmt.Sprintf("- [%d文字目] %s", note.Position.Absolute, note.Text))
	}
	if len(lines) == 0 {
		return "（まだありません）"
	}
	return strings.Join(lines, "\n")
}

func (r *Runner) historyLines(bookID string) string {
	turns := r.store.Conversation(bookID)
	if len(turns) > historyKept {
		turns = turns[len(turns)-historyKept:]
	}
	var lines []string
	for _, turn := range turns {
		who := "読者"
		if turn.Role != "user" {
			who = turn.Speaker
			if who == "" {
				who = "AI"
			}
		}
		text := turn.Text
		if runes := []rune(text); len(runes) > 400 {
			text = string(runes[:400])
		}
		lines = append(lines, who+": "+text)
	}
	if len(lines) == 0 {
		return "（まだありません）"
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(part) == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
