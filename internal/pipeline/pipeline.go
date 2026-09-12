// Package pipeline makes a book's summaries, and it makes them at two very
// different times depending on what kind of book it is.
//
// A novel is summarised as the reader reads it. Processing the whole text first
// would put integrated knowledge - who turns out to be whom - on the far side
// of the boundary before the reader gets there, and no amount of filtering
// afterwards takes that back out. Built page by page, behind the reader, a
// summary covering unread text is never written at all.
//
// A technical book is processed whole at import. There is nothing to hide, the
// text does not grow as the reader moves, and the reader is better served by
// having every chapter ready the moment they open it.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/llm"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/rag"
	"github.com/takeshy/dokking/internal/store"
)

const (
	sectionWorkers = 3

	// pageRunes is what counts as "a page" for a digest. It is a screen of
	// Japanese prose: small enough that one or two lines describe it, large
	// enough that a chapter does not turn into fifty calls.
	pageRunes = 1200

	// buffer is how long the reader has to stop moving before their pages are
	// summarised. Firing per page would queue up a call for every screen of a
	// long sitting and walk straight into a rate limit; the digests only have to
	// keep up with the reader, not with each page turn.
	buffer = 6 * time.Second

	// digestBudget is how many pages one pass will summarise. A reader who
	// imports a book and jumps to the last chapter should not set off a hundred
	// calls; the rest is picked up on the next pass.
	digestBudget = 8
)

// Runner owns both timings. One runs per app.
type Runner struct {
	store    *store.Store
	clientOf func() (*llm.Client, store.Settings)
	emit     func(name string, data any)

	mu      sync.Mutex
	running map[string]context.CancelFunc
	pending map[string]*time.Timer
}

func New(s *store.Store, clientOf func() (*llm.Client, store.Settings)) *Runner {
	return &Runner{
		store: s, clientOf: clientOf,
		emit:    func(string, any) {},
		running: map[string]context.CancelFunc{},
		pending: map[string]*time.Timer{},
	}
}

// OnProgress is how the reading screen hears that a summary landed.
func (r *Runner) OnProgress(emit func(name string, data any)) {
	if emit != nil {
		r.emit = emit
	}
}

func (r *Runner) status(status store.PipelineStatus) {
	_ = r.store.SavePipelineStatus(status)
	r.emit("pipeline", status)
}

// Start prepares a book in the background. Reading, listening and memos are
// available the whole time; only the AI features wait for this.
func (r *Runner) Start(b *book.Book) {
	if !r.begin(b.ID) {
		return
	}
	go func() {
		defer r.end(b.ID)
		r.prepare(r.context(b.ID), b)
	}()
}

// Follow is the novel path: the reader has just read something, so the digests
// have catching up to do. Calls are coalesced - a reader turning pages steadily
// produces one pass, not one per page.
func (r *Runner) Follow(bookID string) {
	r.mu.Lock()
	if _, queued := r.pending[bookID]; queued {
		r.mu.Unlock()
		return
	}
	r.pending[bookID] = time.AfterFunc(buffer, func() {
		r.mu.Lock()
		delete(r.pending, bookID)
		r.mu.Unlock()
		r.catchUp(bookID)
	})
	r.mu.Unlock()
}

func (r *Runner) begin(bookID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.running[bookID]; busy {
		return false
	}
	r.running[bookID] = func() {}
	return true
}

func (r *Runner) context(bookID string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.running[bookID] = cancel
	r.mu.Unlock()
	return ctx
}

func (r *Runner) end(bookID string) {
	r.mu.Lock()
	delete(r.running, bookID)
	r.mu.Unlock()
}

func (r *Runner) Cancel(bookID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.running[bookID]; ok {
		cancel()
	}
	if timer, ok := r.pending[bookID]; ok {
		timer.Stop()
		delete(r.pending, bookID)
	}
}

// prepare is what happens at import, and what it does depends on the category.
func (r *Runner) prepare(ctx context.Context, b *book.Book) {
	total := len(b.Sections)

	// Retrieval is local and needs no model, so it is built first: with no
	// endpoint configured at all, "take me to that scene" still works.
	index := rag.Build(b)
	if err := r.store.SaveIndex(index); err != nil {
		r.status(store.PipelineStatus{BookID: b.ID, Stage: "error", Total: total, Error: err.Error()})
		return
	}

	switch b.Category {
	case book.CategoryTechnical:
		r.prepareTechnical(ctx, b)
	case book.CategoryNovel:
		// Nothing is summarised ahead of the reader. The digests start when they
		// start reading.
		r.status(store.PipelineStatus{
			BookID: b.ID, Stage: "following", Total: total,
			Message: "読んだところまでを要約していきます",
		})
	default:
		r.status(store.PipelineStatus{
			BookID: b.ID, Stage: "uncategorised", Total: total,
			Message: "小説か技術書かを選ぶと準備を始めます",
		})
	}
}

// prepareTechnical passes the whole book through once, chapter by chapter.
func (r *Runner) prepareTechnical(ctx context.Context, b *book.Book) {
	total := len(b.Sections)
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" || settings.LightModel == "" {
		r.status(store.PipelineStatus{
			BookID: b.ID, Stage: "no-model", Total: total,
			Message: "接続先が未設定のため、章要約は保留しています",
		})
		return
	}
	r.status(store.PipelineStatus{BookID: b.ID, Stage: "sections", Total: total})

	k := r.store.Knowledge(b.ID)
	k.BookID = b.ID

	// The whole book in one go, as the file itself, is the right shape for a
	// technical book: it is passed once and never added to, and the figures and
	// code listings that matter most are exactly what extracted text loses.
	if summaries, ok := r.passWholeBook(ctx, b, client, settings); ok {
		for i, section := range b.Sections {
			text, found := summaries[i+1]
			if !found || strings.TrimSpace(text) == "" {
				continue
			}
			k.SetSummary(knowledge.SectionSummary{
				SectionID: section.ID, Title: section.Title,
				Position: section.Range.Start, End: section.Range.End,
				Text: strings.TrimSpace(text), At: time.Now(),
			})
		}
		if err := r.store.SaveKnowledge(k); err == nil && len(k.Summaries) > 0 {
			r.status(store.PipelineStatus{BookID: b.ID, Stage: "ready", Done: len(k.Summaries), Total: total})
			return
		}
	}
	var (
		mu     sync.Mutex
		done   int
		failed []string
	)
	work := make(chan int)
	var group sync.WaitGroup
	for worker := 0; worker < sectionWorkers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := range work {
				section := b.Sections[i]
				mu.Lock()
				_, already := k.Summary(section.ID)
				mu.Unlock()
				if already {
					mu.Lock()
					done++
					mu.Unlock()
					continue
				}
				text, err := summariseText(ctx, client, settings.LightModel, technicalSectionPrompt,
					fmt.Sprintf("章タイトル: %s\n\n%s", section.Title, sectionText(section)))
				mu.Lock()
				if err != nil {
					failed = append(failed, section.ID)
				} else if text != "" {
					k.SetSummary(knowledge.SectionSummary{
						SectionID: section.ID, Title: section.Title,
						Position: section.Range.Start, End: section.Range.End,
						Text: text, At: time.Now(),
					})
					_ = r.store.SaveKnowledge(k)
				}
				done++
				r.status(store.PipelineStatus{BookID: b.ID, Stage: "sections", Done: done, Total: total})
				mu.Unlock()
			}
		}()
	}
	for i := range b.Sections {
		select {
		case <-ctx.Done():
			close(work)
			group.Wait()
			return
		case work <- i:
		}
	}
	close(work)
	group.Wait()
	if ctx.Err() != nil {
		return
	}
	if err := r.store.SaveKnowledge(k); err != nil {
		r.status(store.PipelineStatus{BookID: b.ID, Stage: "error", Done: done, Total: total, Error: err.Error()})
		return
	}
	status := store.PipelineStatus{BookID: b.ID, Stage: "ready", Done: done, Total: total}
	if len(failed) > 0 {
		status.Stage = "partial"
		status.Message = fmt.Sprintf("%d章の要約に失敗しました。再実行できます", len(failed))
	}
	r.status(status)
}

// catchUp summarises whatever the reader has read and no digest covers yet.
func (r *Runner) catchUp(bookID string) {
	if !r.begin(bookID) {
		// Already working; whatever is left is picked up by the next pass.
		r.Follow(bookID)
		return
	}
	defer r.end(bookID)
	ctx := r.context(bookID)

	b, err := r.store.LoadBook(bookID)
	if err != nil || b.Category != book.CategoryNovel {
		return
	}
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" || settings.LightModel == "" {
		r.status(store.PipelineStatus{
			BookID: bookID, Stage: "no-model", Total: len(b.Sections),
			Message: "接続先が未設定のため、ページ要約は保留しています",
		})
		return
	}
	state := r.store.State(bookID)
	k := r.store.Knowledge(bookID)
	k.BookID = bookID
	structured := len(b.Sections) > 1

	budget := digestBudget
	changed := false
	for _, span := range state.ReadRanges() {
		for _, page := range pages(b, span) {
			if budget <= 0 {
				break
			}
			if k.Covers(page) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			text := b.TextUpTo(page.Start, page.End)
			if strings.TrimSpace(text) == "" {
				continue
			}
			digest, isBreak, err := digestPage(ctx, client, settings.LightModel, k, page, text, !structured)
			budget--
			if err != nil {
				r.status(store.PipelineStatus{
					BookID: bookID, Stage: "partial", Total: len(b.Sections),
					Message: "ページ要約に失敗しました。読み進めれば再試行します",
				})
				return
			}
			sectionID := ""
			if section := b.SectionAt(page.Start); section != nil {
				sectionID = section.ID
			}
			k.AddDigest(knowledge.PageDigest{
				SectionID: sectionID, Range: page, Text: digest, At: time.Now(),
			})
			if isBreak && !structured {
				k.AddBreak(page.End)
			}
			changed = true
			_ = r.store.SaveKnowledge(k)
		}
	}

	// A chapter the reader has finished is confirmed: it will not gain any more
	// pages, so it is the one thing safe to compress.
	boundary := state.Boundary()
	for _, chapter := range chapters(b, k, structured) {
		if !chapter.Range.End.AtOrBefore(boundary) {
			continue
		}
		if _, done := k.Summary(chapter.ID); done {
			continue
		}
		digests := k.DigestsWithin(chapter.Range, boundary)
		if len(digests) == 0 {
			continue
		}
		text, err := summariseText(ctx, client, settings.LightModel, chapterPrompt,
			fmt.Sprintf("章タイトル: %s\n\n%s", chapter.Title, knowledge.Lines(digests)))
		if err != nil || text == "" {
			continue
		}
		k.SetSummary(knowledge.SectionSummary{
			SectionID: chapter.ID, Title: chapter.Title,
			Position: chapter.Range.Start, End: chapter.Range.End,
			Text: text, At: time.Now(),
		})
		changed = true
		_ = r.store.SaveKnowledge(k)
	}

	if changed {
		r.emit("knowledge", map[string]any{"bookId": bookID})
	}
	r.status(store.PipelineStatus{
		BookID: bookID, Stage: "following", Done: len(k.Summaries), Total: len(b.Sections),
		Message: fmt.Sprintf("%dページ分を要約しました", len(k.Digests)),
	})
}

// chapter is a stretch that gets one summary: a section when the file has
// chapters, a stretch between two guessed edges when it does not.
type chapter struct {
	ID    string
	Title string
	Range position.Range
}

func chapters(b *book.Book, k knowledge.Knowledge, structured bool) []chapter {
	if structured {
		out := make([]chapter, 0, len(b.Sections))
		for _, section := range b.Sections {
			out = append(out, chapter{ID: section.ID, Title: section.Title, Range: section.Range})
		}
		return out
	}
	var out []chapter
	start := position.Zero
	for _, at := range k.Breaks {
		if at.AtOrBefore(start) {
			continue
		}
		out = append(out, chapter{
			ID:    fmt.Sprintf("seg-%d", start.Absolute),
			Range: position.Range{Start: start, End: at},
		})
		start = at
	}
	return out
}

// pages cuts a read stretch into digest-sized pieces on block edges, so a
// digest is never about half a sentence.
func pages(b *book.Book, span position.Range) []position.Range {
	var out []position.Range
	start := span.Start
	for _, section := range b.Sections {
		if !section.Range.Overlaps(span) {
			continue
		}
		for _, block := range section.Blocks {
			if !block.Range.Overlaps(span) {
				continue
			}
			end := position.Min(block.Range.End, span.End)
			if end.Absolute-start.Absolute >= pageRunes {
				out = append(out, position.Range{Start: start, End: end})
				start = end
			}
		}
	}
	if span.End.After(start) {
		out = append(out, position.Range{Start: start, End: span.End})
	}
	return out
}

const digestPrompt = `読書アプリのページ要約です。与えられた本文の範囲について、1〜2行の日本語で何が起きたかを書いてください。

規則:
- 与えられた範囲の中身だけを書く。先の展開を匂わせない。
- 学習で知っているかもしれない作品の結末・正体を絶対に持ち出さない。
- 誰が・どこで・何をしたかを、後から探せる言葉で書く（「駅で変な男に会う」のように）。
- 直前までの要約は文脈として渡すが、そこに書かれた内容を繰り返さない。

{"digest":"1〜2行の要約"} の形式で返すこと。`

const digestPromptWithBreak = `読書アプリのページ要約です。与えられた本文の範囲について、1〜2行の日本語で何が起きたかを書いてください。
あわせて、この範囲の終わりが章の切れ目かどうかも答えてください。

規則:
- 与えられた範囲の中身だけを書く。先の展開を匂わせない。
- 学習で知っているかもしれない作品の結末・正体を絶対に持ち出さない。
- 誰が・どこで・何をしたかを、後から探せる言葉で書く（「駅で変な男に会う」のように）。
- 場面が大きく変わり、話の区切りだと感じたときだけ break を true にする。

{"digest":"1〜2行の要約","break":false} の形式で返すこと。`

const chapterPrompt = `読書アプリの章要約です。1ページずつの要約が並んでいます。これを章ひとつぶんの流れとして200字程度にまとめてください。

規則:
- 与えられた要約に書かれていることだけを使う。補ったり推測したりしない。
- 先の展開を匂わせない。作品について学習で知っていることを混ぜない。
- 出力は要約本文のみ。`

const technicalSectionPrompt = `技術書の章要約です。この章が何を説明しているかを300字程度の日本語でまとめてください。

規則:
- 扱っている技術・API・バージョン・製品名は、本文に出てきた表記のまま残す。後で「今も正しいか」を確かめるための手がかりになる。
- 本文にないことを足さない。
- 出力は要約本文のみ。`

// passWholeBook hands the source file to the model once and asks for every
// chapter at the same time.
//
// Whether that means a PDF or a set of page images is the provider's business,
// not this function's: it asks, and if the answer is no, the caller falls back
// to the text path. Nothing above here knows which happened.
func (r *Runner) passWholeBook(ctx context.Context, b *book.Book, client *llm.Client, settings store.Settings) (map[int]string, bool) {
	if !settings.SendBookAsFile || b.Format != book.FormatPDF {
		return nil, false
	}
	path, ok := r.store.SourceFile(b.ID)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 24<<20 {
		return nil, false
	}
	var listing []string
	for i, section := range b.Sections {
		title := section.Title
		if title == "" {
			title = "（無題）"
		}
		listing = append(listing, fmt.Sprintf("%d. %s", i+1, title))
	}
	reply, err := client.Chat(ctx, llm.Request{
		Model: settings.LightModel, JSON: true,
		Files: []llm.Attachment{{Name: "book.pdf", MIME: "application/pdf", Data: data}},
		Messages: []llm.Message{
			{Role: "system", Content: wholeBookPrompt},
			{Role: "user", Content: "章の一覧:\n" + strings.Join(listing, "\n")},
		},
	})
	if err != nil {
		return nil, false
	}
	var parsed struct {
		Summaries []struct {
			Index int    `json:"index"`
			Text  string `json:"text"`
		} `json:"summaries"`
	}
	if err := llm.DecodeJSON(reply, &parsed); err != nil || len(parsed.Summaries) == 0 {
		return nil, false
	}
	out := map[int]string{}
	for _, summary := range parsed.Summaries {
		out[summary.Index] = summary.Text
	}
	return out, true
}

const wholeBookPrompt = `技術書のPDFを渡します。与えられた章の一覧に対応させて、それぞれの章の要約を300字程度の日本語で書いてください。

規則:
- 扱っている技術・API・バージョン・製品名は、本文に出てきた表記のまま残す。後で「今も正しいか」を確かめるための手がかりになる。
- コード・表・図から読み取れることも要約に含めてよい。
- 本文にないことを足さない。

{"summaries":[{"index":1,"text":"…"}]} の形式で、一覧のすべての番号について返すこと。`

type digestReply struct {
	Digest string `json:"digest"`
	Break  bool   `json:"break"`
}

func digestPage(ctx context.Context, client *llm.Client, model string, k knowledge.Knowledge, page position.Range, text string, askBreak bool) (string, bool, error) {
	prompt := digestPrompt
	if askBreak {
		prompt = digestPromptWithBreak
	}
	// Only the last few digests go along: enough to keep the thread of the
	// scene, not so much that every page replays the book so far.
	recent := k.DigestsUpTo(page.Start)
	if len(recent) > 3 {
		recent = recent[len(recent)-3:]
	}
	user := text
	if len(recent) > 0 {
		user = "直前までの要約:\n" + knowledge.Lines(recent) + "\n\n今回の本文:\n" + text
	}
	reply, err := client.Chat(ctx, llm.Request{
		Model: model, JSON: true,
		Messages: []llm.Message{{Role: "system", Content: prompt}, {Role: "user", Content: user}},
	})
	if err != nil {
		return "", false, err
	}
	var parsed digestReply
	if err := llm.DecodeJSON(reply, &parsed); err != nil {
		// A model that ignored the shape still said something useful.
		return strings.TrimSpace(reply), false, nil
	}
	return strings.TrimSpace(parsed.Digest), parsed.Break, nil
}

func summariseText(ctx context.Context, client *llm.Client, model, prompt, user string) (string, error) {
	if strings.TrimSpace(user) == "" {
		return "", nil
	}
	reply, err := client.Chat(ctx, llm.Request{
		Model:    model,
		Messages: []llm.Message{{Role: "system", Content: prompt}, {Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply), nil
}

func sectionText(section book.Section) string {
	var parts []string
	for _, block := range section.Blocks {
		if block.Plain != "" {
			parts = append(parts, block.Plain)
		}
	}
	text := strings.Join(parts, "\n")
	runes := []rune(text)
	if len(runes) > 24000 {
		text = string(runes[:24000])
	}
	return text
}
