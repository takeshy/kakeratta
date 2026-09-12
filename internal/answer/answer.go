// Package answer is the only path from a question to a reply.
//
// Which material a question gets is decided by the book's category, and only
// then by the question. A novel's paths all take the reader's boundary and
// build their context from text that ends at or before it; a technical book has
// one path, which may use the whole book and the open web.
//
// Two guards sit on top of the novel paths: the review is closed until the book
// is finished, and the prompt tells the model in plain words that it has read
// exactly this far. The first is a guarantee; the second is not, because a
// model may have read the book during training - see the leakage test in the
// README.
package answer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/llm"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/rag"
	"github.com/takeshy/dokking/internal/store"
	"github.com/takeshy/dokking/internal/websearch"
)

// Mode keeps the paths apart on purpose: a question about a person is answered
// from the text the reader has read, a half-remembered scene from the
// summaries, and they do not borrow each other's context.
type Mode string

const (
	ModePerson    Mode = "person"
	ModeRecap     Mode = "recap"
	ModeScene     Mode = "scene"
	ModeCurrent   Mode = "current"
	ModeReview    Mode = "review"
	ModeTechnical Mode = "technical"
)

// textBudget is how much raw body text one answer may carry.
//
// A question with a fixed range - who is this, what has happened so far - is
// answered from the text itself rather than from anything pre-digested, because
// pre-digesting is exactly what would have integrated the book ahead of the
// reader. Past this much, the summaries carry the earlier part and the raw text
// carries the recent part.
const textBudget = 48000

// Source is a place in the book an answer leans on, so the reader can go there.
type Source struct {
	Label    string            `json:"label"`
	Snippet  string            `json:"snippet"`
	Position position.Position `json:"position"`
	Range    position.Range    `json:"range"`
}

// Reply is what the app shows. Text may be present without a model when the
// mode can answer structurally; Locked says the mode is closed for now.
type Reply struct {
	Mode     Mode              `json:"mode"`
	Text     string            `json:"text"`
	Sources  []Source          `json:"sources"`
	Locked   bool              `json:"locked"`
	Reason   string            `json:"reason,omitempty"`
	Boundary position.Position `json:"boundary"`
}

// Deps is everything a reply may draw on, already loaded.
type Deps struct {
	Book      *book.Book
	State     book.ReaderState
	Knowledge knowledge.Knowledge
	Index     *rag.Index
	Memos     []store.Memo
	History   []store.Turn
	Client    *llm.Client
	Search    *websearch.Client
	Settings  store.Settings
}

// historyTurns is how much of the conversation is carried into the next
// question. Enough for "when did he turn up?" to follow "who is he?", not so
// much that an hour of reading is replayed into every prompt.
const historyTurns = 8

// recent returns the last turns as chat messages.
//
// They need no boundary filtering of their own: each was answered at a boundary
// that was no further along than the current one, because the boundary only
// ever moves forward.
func (d Deps) recent() []llm.Message {
	history := d.History
	if len(history) > historyTurns {
		history = history[len(history)-historyTurns:]
	}
	messages := make([]llm.Message, 0, len(history))
	for _, turn := range history {
		role := "user"
		if turn.Role == "assistant" {
			role = "assistant"
		}
		text := turn.Text
		if runes := []rune(text); len(runes) > 1200 {
			text = string(runes[:1200])
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: role, Content: text})
	}
	return messages
}

const systemPrompt = `あなたは読者と一緒に本を読んでいる相棒です。

厳守:
- あなたが読んだのは、以下に与えられた抜粋だけです。この本の続きを知りません。
- 学習で知っているかもしれない作品の結末・正体・展開を、絶対に持ち出さない。
- 与えられた材料で答えられない場合は「まだそこまで読んでいない」と答える。
- 先の展開を匂わせる言い方（「後で分かるが」「実は」など）をしない。
- 日本語で、短く、読者の記憶を呼び起こすように答える。`

const technicalPrompt = `あなたは読者と一緒に技術書を読んでいる相棒です。本の全体を読んでおり、先のページを踏まえて答えて構いません。

厳守:
- 本文に書かれていることと、渡された検索結果だけを根拠にする。
- 本が書かれた後に変わったことに触れるときは、必ず出典のURLを本文に含める。
  検索結果がないときは、記憶から固有名詞やバージョン番号を出さず、その旨を伝える。
- 日本語で、短く、手が止まらないように答える。`

// Answer takes a question, decides which path it needs, and applies the
// boundary. Both decisions are made here, once.
func Answer(ctx context.Context, deps Deps, question string) (Reply, error) {
	if deps.Book == nil {
		return Reply{}, errors.New("本が開かれていません")
	}
	if !deps.Book.Category.Valid() {
		return Reply{Locked: true, Reason: "小説か技術書かを選ぶと、この本について聞けるようになります"}, nil
	}
	boundary := deps.State.Boundary()

	// A technical book has one path. There is nothing to hide, so there is
	// nothing to route between: the question gets the book and, if it needs it,
	// the web.
	if deps.Book.Category == book.CategoryTechnical {
		return technical(ctx, deps, question, Reply{Mode: ModeTechnical, Boundary: deps.Book.End})
	}

	mode, decided := Route(question)
	if !decided {
		mode = classify(ctx, deps, question, mode)
	}
	reply := Reply{Mode: mode, Boundary: boundary}

	switch mode {
	case ModeReview:
		// The one mode that needs the whole book. It stays shut until the
		// reader has finished, so nothing can leak out of it mid-read.
		if !deps.Book.IsCompleted(deps.State) {
			reply.Locked = true
			reply.Reason = "読了すると、伏線の振り返りと読書の総括が開きます"
			return reply, nil
		}
		return review(ctx, deps, question, reply)
	case ModePerson:
		return person(ctx, deps, question, boundary, reply)
	case ModeScene:
		return scene(ctx, deps, question, boundary, reply)
	case ModeRecap:
		return recap(ctx, deps, boundary, reply)
	case ModeCurrent:
		return current(ctx, deps, question, boundary, reply)
	default:
		return Reply{}, fmt.Errorf("不明なモード: %s", mode)
	}
}

// readMaterial is the novel paths' view of the book: the text of the stretches
// the reader actually read, with the chapter summaries standing in for whatever
// did not fit.
//
// Nothing here was integrated ahead of the reader. Working out that two names
// belong to one person is left to the model, on the text, at the moment of
// asking - which is also the moment the reader could have worked it out.
func readMaterial(deps Deps, boundary position.Position) string {
	text := deps.Book.ReadText(deps.State, textBudget)
	summaries := knowledge.SummaryLines(deps.Knowledge.SummariesUpTo(boundary))
	if len([]rune(text)) < textBudget || summaries == "" {
		return "読者がここまでに読んだ本文:\n" + text
	}
	return strings.Join([]string{
		"ここまでの章要約:", summaries, "",
		"読者が読んだ本文（直近のぶん）:", text,
	}, "\n")
}

func person(ctx context.Context, deps Deps, question string, boundary position.Position, reply Reply) (Reply, error) {
	material := readMaterial(deps, boundary)
	if strings.TrimSpace(material) == "" {
		reply.Text = "まだ本文を読み進めていません。"
		return reply, nil
	}
	if section := deps.Book.SectionAt(boundary); section != nil {
		reply.Sources = append(reply.Sources, Source{Label: "ここまで", Position: boundary})
		_ = section
	}
	// Japanese calls one person by surname, given name, nickname and title, and
	// which of those are the same person is often the story. Nothing resolves
	// them in advance; the text up to here is handed over and the model works it
	// out from what the reader has also seen.
	text, err := complete(ctx, deps, systemPrompt, deps.Settings.ChatModel, strings.Join([]string{
		"読者の質問: " + question,
		"",
		"同じ人物が姓・名・愛称・敬称で呼び分けられていることがあります。",
		"本文でまだ同一人物だと明かされていない呼称を、勝手に結びつけないでください。",
		"",
		material,
	}, "\n"))
	if err != nil {
		return reply, err
	}
	reply.Text = text
	return reply, nil
}

// scene narrows in two steps: the chapter summaries say which chapter, that
// chapter's page digests say where in it, and the reader is taken to the text.
//
// There is no vector index and no embedding model. What the reader remembers -
// "the bit where he met the odd man at the station" - is an event, and an event
// is exactly what a page digest is a sentence about.
func scene(ctx context.Context, deps Deps, question string, boundary position.Position, reply Reply) (Reply, error) {
	digests := deps.Knowledge.DigestsUpTo(boundary)
	if len(digests) == 0 {
		return sceneByIndex(ctx, deps, question, boundary, reply)
	}
	if summaries := deps.Knowledge.SummariesUpTo(boundary); len(summaries) > 2 {
		if narrowed := narrow(ctx, deps, question, summaries); narrowed != nil {
			digests = deps.Knowledge.DigestsWithin(
				position.Range{Start: narrowed.Position, End: narrowed.End}, boundary)
		}
	}
	if len(digests) == 0 {
		return sceneByIndex(ctx, deps, question, boundary, reply)
	}
	text, err := complete(ctx, deps, systemPrompt, deps.Settings.ChatModel, strings.Join([]string{
		"読者が探している場面: " + question,
		"",
		"ここまでのページ要約です。どれがその場面かを選び、一言で示してください。",
		"該当がなければ、ないと言ってください。文字数は本文の位置です。",
		"",
		knowledge.Lines(digests),
	}, "\n"))
	if err != nil {
		return reply, err
	}
	reply.Text = text
	for _, digest := range pick(digests, text) {
		reply.Sources = append(reply.Sources, Source{
			Label:    "本文",
			Snippet:  digest.Text,
			Position: digest.Range.Start,
			Range:    digest.Range,
		})
	}
	return reply, nil
}

// narrow is the first of the two steps: which chapter was it in.
func narrow(ctx context.Context, deps Deps, question string, summaries []knowledge.SectionSummary) *knowledge.SectionSummary {
	if deps.Settings.LightModel == "" {
		return nil
	}
	var lines []string
	for i, summary := range summaries {
		title := summary.Title
		if title == "" {
			title = fmt.Sprintf("第%d章", i+1)
		}
		lines = append(lines, fmt.Sprintf("%d. 【%s】%s", i+1, title, summary.Text))
	}
	reply, err := deps.Client.Chat(ctx, llm.Request{
		Model: deps.Settings.LightModel,
		Messages: []llm.Message{
			{Role: "system", Content: "読者が探している場面がどの章にあるかを選び、番号だけを返してください。分からなければ 0 と返してください。"},
			{Role: "user", Content: question + "\n\n" + strings.Join(lines, "\n")},
		},
	})
	if err != nil {
		return nil
	}
	index := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(reply), "%d", &index); err != nil {
		return nil
	}
	if index < 1 || index > len(summaries) {
		return nil
	}
	return &summaries[index-1]
}

// pick finds which digests the answer pointed at, by the offsets it quoted.
func pick(digests []knowledge.PageDigest, text string) []knowledge.PageDigest {
	var out []knowledge.PageDigest
	for _, digest := range digests {
		if strings.Contains(text, fmt.Sprintf("%d", digest.Range.Start.Absolute)) {
			out = append(out, digest)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// sceneByIndex is the fallback for a book with no digests yet: a local, plain
// word index that needs no model at all, so a freshly imported book can still
// be searched with nothing configured.
func sceneByIndex(ctx context.Context, deps Deps, question string, boundary position.Position, reply Reply) (Reply, error) {
	if deps.Index == nil {
		reply.Text = "まだ要約も索引もできていません。少し読み進めてからもう一度どうぞ。"
		return reply, nil
	}
	hits := deps.Index.Search(question, boundary, 5)
	if len(hits) == 0 {
		reply.Text = "ここまでの範囲では見つかりませんでした。"
		return reply, nil
	}
	var passages []string
	for _, hit := range hits {
		label := hit.Passage.Title
		if label == "" {
			label = "本文"
		}
		reply.Sources = append(reply.Sources, Source{
			Label:    label,
			Snippet:  hit.Snippet,
			Position: hit.Passage.Range.Start,
			Range:    hit.Passage.Range,
		})
		passages = append(passages, fmt.Sprintf("[%s] %s", label, hit.Passage.Text))
	}
	text, err := complete(ctx, deps, systemPrompt, deps.Settings.ChatModel, strings.Join([]string{
		"読者が探している場面: " + question,
		"",
		"候補の本文:",
		strings.Join(passages, "\n\n"),
		"",
		"どれがその場面かを一言で示し、なぜそう考えたかを一文添えてください。該当がなければそう言ってください。",
	}, "\n"))
	if err != nil {
		// Retrieval alone is a complete answer: the hits are the destinations.
		reply.Text = "近そうな箇所です。"
		return reply, nil
	}
	reply.Text = text
	return reply, nil
}

func recap(ctx context.Context, deps Deps, boundary position.Position, reply Reply) (Reply, error) {
	summaries := deps.Knowledge.SummariesUpTo(boundary)
	var parts []string
	for _, summary := range summaries {
		title := summary.Title
		if title == "" {
			title = "章"
		}
		parts = append(parts, "【"+title+"】"+summary.Text)
		reply.Sources = append(reply.Sources, Source{Label: title, Position: summary.Position, Snippet: summary.Text})
	}
	// The chapter being read now has no summary yet - it is not finished - so
	// its pages come from the digests.
	if len(summaries) > 0 {
		last := summaries[len(summaries)-1].End
		if digests := deps.Knowledge.DigestsWithin(position.Range{Start: last, End: boundary}, boundary); len(digests) > 0 {
			parts = append(parts, "【読みかけの章】"+knowledge.Lines(digests))
		}
	} else if digests := deps.Knowledge.DigestsUpTo(boundary); len(digests) > 0 {
		parts = append(parts, knowledge.Lines(digests))
	}
	// The reader's own notes are part of the story they are in the middle of.
	for _, memo := range deps.Memos {
		if memo.Position.AtOrBefore(boundary) {
			parts = append(parts, "【あなたのメモ】"+memo.Text)
		}
	}
	if len(parts) == 0 {
		// Nothing summarised yet, but the text itself is always there.
		parts = append(parts, readMaterial(deps, boundary))
	}
	text, err := complete(ctx, deps, systemPrompt, deps.Settings.ChatModel, strings.Join([]string{
		"ここまでの要約と読者のメモです。読者が読書を再開できるように、流れを短くつないでください。",
		"",
		strings.Join(parts, "\n"),
	}, "\n"))
	if err != nil {
		reply.Text = strings.Join(parts, "\n\n")
		return reply, nil
	}
	reply.Text = text
	return reply, nil
}

func current(ctx context.Context, deps Deps, question string, boundary position.Position, reply Reply) (Reply, error) {
	from := deps.Book.At(maxInt(0, deps.State.Current.Absolute-2000))
	text := deps.Book.TextUpTo(from, boundary)
	if strings.TrimSpace(text) == "" {
		reply.Text = "まだ本文が読み込まれていません。"
		return reply, nil
	}
	reply.Sources = append(reply.Sources, Source{Label: "いま読んでいるあたり", Position: deps.State.Current})
	answer, err := complete(ctx, deps, systemPrompt, deps.Settings.ChatModel, strings.Join([]string{
		"読者の質問: " + question,
		"",
		"読者がいま読んでいる直前までの本文:",
		text,
	}, "\n"))
	if err != nil {
		return reply, err
	}
	reply.Text = answer
	return reply, nil
}

func review(ctx context.Context, deps Deps, question string, reply Reply) (Reply, error) {
	var parts []string
	for _, summary := range deps.Knowledge.Summaries {
		parts = append(parts, "【"+summary.Title+"】"+summary.Text)
	}
	memos := append([]store.Memo(nil), deps.Memos...)
	sort.Slice(memos, func(i, j int) bool { return memos[i].Position.Absolute < memos[j].Position.Absolute })
	for _, memo := range memos {
		parts = append(parts, fmt.Sprintf("【メモ %d文字目】%s（引用: %s）", memo.Position.Absolute, memo.Text, memo.Quote))
		reply.Sources = append(reply.Sources, Source{Label: "メモ", Position: memo.Position, Snippet: memo.Text})
	}
	if question == "" {
		question = "この本を私がどう読んだかを振り返ってください。どこで何を思い、それが後にどうなったかを、私のメモに沿って書いてください。"
	}
	text, err := complete(ctx, deps, systemPrompt, deps.Settings.HeavyModel, strings.Join([]string{
		"読者はこの本を読み終えました。全体を使って構いません。",
		"",
		"読者の依頼: " + question,
		"",
		strings.Join(parts, "\n"),
	}, "\n"))
	if err != nil {
		return reply, err
	}
	reply.Text = text
	return reply, nil
}

// technical answers from the whole book, and brings in what changed after it
// was written. Being told an API is deprecated has never spoiled anyone's
// reading.
func technical(ctx context.Context, deps Deps, question string, reply Reply) (Reply, error) {
	if strings.TrimSpace(question) == "" {
		question = "いま開いているページについて、知っておくべきことはありますか"
	}
	parts := []string{
		"読者の質問: " + question,
		"",
		"この本の章要約（全体）:",
		knowledge.SummaryLines(deps.Knowledge.Summaries),
		"",
		"読者がいま開いているあたりの本文:",
		deps.Book.TextUpTo(deps.State.Current, deps.Book.At(deps.State.Current.Absolute+4000)),
	}
	reply.Sources = append(reply.Sources, Source{Label: "いま開いているページ", Position: deps.State.Current})
	if deps.Search.Enabled() {
		if results, err := deps.Search.Search(ctx, question, 5); err == nil && len(results) > 0 {
			parts = append(parts, "", "Web検索の結果（固有名詞とバージョンはここからのみ引くこと）:", websearch.Lines(results))
			for _, result := range results {
				reply.Sources = append(reply.Sources, Source{Label: result.Title, Snippet: result.URL})
			}
		}
	} else {
		parts = append(parts, "", "（Web検索は設定されていません。本文にないバージョンや製品名を推測で書かないこと）")
	}
	text, err := complete(ctx, deps, technicalPrompt, deps.Settings.ChatModel, strings.Join(parts, "\n"))
	if err != nil {
		return reply, err
	}
	reply.Text = text
	return reply, nil
}

func complete(ctx context.Context, deps Deps, system, model, user string) (string, error) {
	if model == "" {
		model = deps.Settings.ChatModel
	}
	messages := []llm.Message{{Role: "system", Content: system}}
	messages = append(messages, deps.recent()...)
	messages = append(messages, llm.Message{Role: "user", Content: user})
	return deps.Client.Chat(ctx, llm.Request{Model: model, Messages: messages})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
