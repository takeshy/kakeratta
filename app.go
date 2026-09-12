package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/takeshy/dokking/internal/answer"
	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/llm"
	"github.com/takeshy/dokking/internal/persona"
	"github.com/takeshy/dokking/internal/pipeline"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/rag"
	"github.com/takeshy/dokking/internal/sample"
	"github.com/takeshy/dokking/internal/speech"
	"github.com/takeshy/dokking/internal/store"
	"github.com/takeshy/dokking/internal/websearch"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// App is the surface the window calls. It holds no reading state of its own:
// the store is the truth, so a crash costs nothing but the current screen.
type App struct {
	app       *application.App
	store     *store.Store
	runner    *pipeline.Runner
	personas  *persona.Runner
	catalogue *persona.Catalogue
	speech    *speech.Service
}

func NewApp(wailsApp *application.App, s *store.Store) *App {
	app := &App{app: wailsApp, store: s}
	emit := func(name string, data any) {
		if wailsApp != nil {
			wailsApp.Event.Emit(name, data)
		}
	}
	app.runner = pipeline.New(s, app.client)
	app.runner.OnProgress(emit)
	app.catalogue = persona.NewCatalogue(s.Root())
	app.personas = persona.NewRunner(s, app.catalogue, app.client)
	app.personas.OnSpoke(emit)
	app.speech = speech.New(
		context.Background(),
		s.Root(),
		func() store.SpeechSettings { return s.Settings().Speech },
		func(name string, data any) {
			if wailsApp != nil {
				wailsApp.Event.Emit(name, data)
			}
		},
		app.pickFile,
		func(target string) error {
			if wailsApp == nil {
				return errors.New("ウィンドウがありません")
			}
			return wailsApp.Browser.OpenURL(target)
		},
	)
	return app
}

func (a *App) pickFile(title, pattern string) (string, error) {
	if a.app == nil {
		return "", errors.New("ウィンドウがありません")
	}
	return a.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
		Filters:        []application.FileFilter{{DisplayName: pattern, Pattern: pattern}},
	}).PromptForSingleSelection()
}

// ---- dictation -------------------------------------------------------------
//
// The speech stack is carried over whole from speech-popup: the same engines,
// the same send phrase, the same replacement rules, the same live streaming.
// These forward to it so the frontend reaches it the way it always has.

func (a *App) SpeechHTTPRequest(request speech.ExternalHTTPRequest) (*speech.ExternalHTTPResponse, error) {
	return a.speech.SpeechHTTPRequest(request)
}

func (a *App) VertexSpeechHTTPRequest(request speech.ExternalHTTPRequest) (*speech.ExternalHTTPResponse, error) {
	return a.speech.VertexSpeechHTTPRequest(request)
}

func (a *App) StartLiveSpeech() (string, error) { return a.speech.StartLiveSpeech() }

func (a *App) SendLiveSpeechAudio(sessionID, audio string) error {
	return a.speech.SendLiveSpeechAudio(sessionID, audio)
}

func (a *App) FinishLiveSpeech(sessionID string) error { return a.speech.FinishLiveSpeech(sessionID) }

func (a *App) StopLiveSpeech() { a.speech.StopLiveSpeech() }

func (a *App) SelectVertexOAuthClient() (*speech.VertexOAuthClient, error) {
	return a.speech.SelectVertexOAuthClient()
}

func (a *App) ConnectVertexOAuth(clientID, clientSecret, projectID string) (*speech.VertexOAuthStatus, error) {
	return a.speech.ConnectVertexOAuth(clientID, clientSecret, projectID)
}

func (a *App) GetVertexOAuthStatus() (*speech.VertexOAuthStatus, error) {
	return a.speech.GetVertexOAuthStatus()
}

func (a *App) DisconnectVertexOAuth() error { return a.speech.DisconnectVertexOAuth() }

func (a *App) client() (*llm.Client, store.Settings) {
	settings := a.store.Settings()
	client := llm.New(settings.BaseURL, settings.APIKey)
	client.Session = settings.Provider == store.ProviderLocal && settings.LocalFramework == "opencode"
	return client, settings
}

// search is the app's web search, handed to the paths that declared they need
// it. Nothing else reaches the network.
func (a *App) search() *websearch.Client {
	settings := a.store.Settings().Search
	return websearch.New(settings.Provider, settings.BaseURL, settings.APIKey)
}

// Models lists what the configured endpoint serves, for the settings picker.
func (a *App) Models() ([]string, error) {
	client, _ := a.client()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return client.Models(ctx)
}

// Providers is the list the settings screen offers, with each one's usual
// address so choosing a provider fills the endpoint in.
func (a *App) Providers() map[string]any {
	frameworks := map[string]any{}
	for id, entry := range store.LocalFrameworks {
		frameworks[string(id)] = map[string]string{"label": entry.Label, "endpoint": entry.Endpoint}
	}
	return map[string]any{
		"providers": []map[string]string{
			{"id": string(store.ProviderNebius), "label": "Nebius Token Factory", "endpoint": store.NebiusBaseURL},
			{"id": string(store.ProviderOpenAI), "label": "OpenAI 互換", "endpoint": "https://api.openai.com/v1"},
			{"id": string(store.ProviderLocal), "label": "ローカルLLM", "endpoint": store.LocalFrameworks["ollama"].Endpoint},
		},
		"frameworks":   frameworks,
		"nemotron":     store.NemotronSuper,
		"nebiusModels": []string{store.NemotronSuper},
		"search":       websearch.Providers,
	}
}

// ---- shelf ----------------------------------------------------------------

// Shelf is the home screen: which books, and how far into each one.
func (a *App) Shelf() ([]store.Progress, error) { return a.store.Shelf() }

// ImportDialog asks for a file and imports it.
func (a *App) ImportDialog() (store.BookMeta, error) {
	if a.app == nil {
		return store.BookMeta{}, errors.New("ウィンドウがありません")
	}
	path, err := a.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          "本を開く",
		CanChooseFiles: true,
		Filters: []application.FileFilter{
			{DisplayName: "本 (*.epub, *.pdf, *.txt)", Pattern: "*.epub;*.pdf;*.txt"},
			{DisplayName: "すべてのファイル", Pattern: "*.*"},
		},
	}).PromptForSingleSelection()
	if err != nil || strings.TrimSpace(path) == "" {
		return store.BookMeta{}, err
	}
	return a.ImportPath(path)
}

// ImportPath parses a file and starts preparing it. Preparation runs in the
// background: the book can be read and annotated while it is still going.
func (a *App) ImportPath(path string) (store.BookMeta, error) {
	parsed, err := ingest.FromFile(path)
	if err != nil {
		return store.BookMeta{}, err
	}
	meta, err := a.adopt(parsed)
	if err != nil {
		return meta, err
	}
	// A technical book may be handed to the model as the file itself, so the
	// file has to outlive wherever the reader downloaded it to.
	if parsed.Format == book.FormatPDF {
		_ = a.store.SaveSourceFile(parsed.ID, path)
	}
	return meta, nil
}

// ImportText takes pasted text, which is how a Gutenberg or Aozora download
// gets in without leaving the app.
func (a *App) ImportText(title, author, text string) (store.BookMeta, error) {
	if strings.TrimSpace(text) == "" {
		return store.BookMeta{}, errors.New("本文が空です")
	}
	parsed, err := ingest.Finalise(ingest.FromText(title, author, text), "pasted", text)
	if err != nil {
		return store.BookMeta{}, err
	}
	return a.adopt(parsed)
}

// ImportSample loads the bundled public-domain-style sample so the app can be
// tried immediately, with no file and no key.
func (a *App) ImportSample() (store.BookMeta, error) {
	return a.ImportText(sample.Title, sample.Author, sample.Text)
}

func (a *App) adopt(parsed *book.Book) (store.BookMeta, error) {
	if err := a.store.SaveBook(parsed); err != nil {
		return store.BookMeta{}, err
	}
	state := a.store.State(parsed.ID)
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now()
		_ = a.store.SaveState(state)
	}
	// Nothing is sent to a model until the reader says what kind of book this
	// is: the category is what decides how much of it may be looked at, so
	// guessing it would be guessing at the boundary.
	if parsed.Category.Valid() {
		a.runner.Start(parsed)
	}
	shelf, err := a.store.Shelf()
	if err != nil {
		return store.BookMeta{}, err
	}
	for _, entry := range shelf {
		if entry.Book.ID == parsed.ID {
			return entry.Book, nil
		}
	}
	return store.BookMeta{}, errors.New("取り込みに失敗しました")
}

func (a *App) DeleteBook(id string) error {
	a.runner.Cancel(id)
	a.personas.Cancel(id)
	return a.store.DeleteBook(id)
}

// ---- category and companions ----------------------------------------------

// SetCategory is the first choice about a book, and the one that settles every
// other: what the AI may see, when it may see it, and which companions are
// offered. Preparation only starts once it is made.
func (a *App) SetCategory(bookID string, category book.Category) (Opened, error) {
	if !category.Valid() {
		return Opened{}, errors.New("小説か技術書かを選んでください")
	}
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return Opened{}, err
	}
	if loaded.Category != category {
		loaded.Category = category
		if err := a.store.SaveBook(loaded); err != nil {
			return Opened{}, err
		}
	}
	a.runner.Start(loaded)
	return a.OpenBook(bookID)
}

// StartReading marks the opening choices as made.
//
// Reading with nobody is a proper way to read, so the number of companions
// cannot say whether the reader has decided. This can.
func (a *App) StartReading(bookID string) (Opened, error) {
	state := a.store.State(bookID)
	if !state.Ready {
		state.Ready = true
		if err := a.store.SaveState(state); err != nil {
			return Opened{}, err
		}
	}
	return a.OpenBook(bookID)
}

// Personas lists what can be put in this book: the companions that declared
// this category. Choosing the category first is what keeps the list short.
func (a *App) Personas(category book.Category) []persona.Plugin {
	if !category.Valid() {
		return a.catalogue.All()
	}
	return a.catalogue.For(category)
}

// AddPersona puts a companion in a book. Its notes and its side of the thread
// are still there if it has read this book before.
func (a *App) AddPersona(bookID, personaID string) (Opened, error) {
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return Opened{}, err
	}
	plugin, ok := a.catalogue.Find(personaID)
	if !ok {
		return Opened{}, errors.New("そのペルソナが見つかりません")
	}
	if !plugin.Suits(loaded.Category) {
		return Opened{}, errors.New("このペルソナはこのカテゴリの本には入れません")
	}
	state := a.store.State(bookID)
	if state.HasPersona(personaID) {
		return a.OpenBook(bookID)
	}
	if len(state.Personas) >= book.MaxPersonas {
		return Opened{}, fmt.Errorf("ペルソナは%d体までです", book.MaxPersonas)
	}
	state.Personas = append(state.Personas, personaID)
	if err := a.store.SaveState(state); err != nil {
		return Opened{}, err
	}
	return a.OpenBook(bookID)
}

// RemovePersona takes a companion out of a book without erasing it. Deleting
// its notes would mean that adding it back gets someone who has to start the
// book again, which is not the same person.
func (a *App) RemovePersona(bookID, personaID string) (Opened, error) {
	state := a.store.State(bookID)
	kept := state.Personas[:0]
	for _, id := range state.Personas {
		if id != personaID {
			kept = append(kept, id)
		}
	}
	state.Personas = kept
	if err := a.store.SaveState(state); err != nil {
		return Opened{}, err
	}
	return a.OpenBook(bookID)
}

// InstallPersona takes a persona definition the reader was given and adds it to
// the workspace.
func (a *App) InstallPersona(definition string) (persona.Plugin, error) {
	return a.catalogue.Install([]byte(definition))
}

// InstallPersonaDialog asks for a persona.json and installs it.
func (a *App) InstallPersonaDialog() (persona.Plugin, error) {
	path, err := a.pickFile("ペルソナを追加", "*.json")
	if err != nil || strings.TrimSpace(path) == "" {
		return persona.Plugin{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return persona.Plugin{}, err
	}
	return a.catalogue.Install(raw)
}

func (a *App) UninstallPersona(personaID string) error {
	return a.catalogue.Uninstall(personaID)
}

// ---- reading --------------------------------------------------------------

// Opened is everything the reading screen needs in one call.
type Opened struct {
	Book     *book.Book           `json:"book"`
	State    book.ReaderState     `json:"state"`
	Memos    []store.Memo         `json:"memos"`
	Status   store.PipelineStatus `json:"status"`
	Finished bool                 `json:"finished"`
	// Personas are the companions in this book, in the order they were added.
	Personas []persona.Plugin `json:"personas"`
	// Dwell is how long a page has to stay in front of the reader before it
	// counts as read. The screen does the timing; the rule lives in Go.
	Dwell int `json:"dwell"`
}

func (a *App) OpenBook(id string) (Opened, error) {
	loaded, err := a.store.LoadBook(id)
	if err != nil {
		return Opened{}, err
	}
	state := a.store.State(id)
	memos, err := a.store.Memos(id)
	if err != nil {
		return Opened{}, err
	}
	var plugins []persona.Plugin
	for _, personaID := range state.Personas {
		if plugin, ok := a.catalogue.Find(personaID); ok {
			plugins = append(plugins, plugin)
		}
	}
	return Opened{
		Book:     loaded,
		State:    state,
		Memos:    memos,
		Status:   a.store.PipelineStatus(id),
		Finished: loaded.IsCompleted(state),
		Personas: plugins,
		Dwell:    book.DwellSeconds,
	}, nil
}

// Seek records where the reader is looking.
//
// It moves the current position and nothing else. Going back fifty pages to
// check something is the feature the app exists for, and it must not change
// what has been read.
func (a *App) Seek(bookID string, at position.Position) (book.ReaderState, error) {
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return book.ReaderState{}, err
	}
	state := a.store.State(bookID)
	state.Current = loaded.Resolve(at)
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now()
	}
	if err := a.store.SaveState(state); err != nil {
		return book.ReaderState{}, err
	}
	return state, nil
}

// MarkRead is the one way the read range grows.
//
// The screen reports a stretch and how long the reader stayed on it. Flicking
// through is not reading, so anything under the dwell threshold is dropped
// here - the same data then answers "where was I", "how much have I read",
// "which days did I read" and "what may the AI see".
func (a *App) MarkRead(bookID string, span position.Range, seconds float64) (book.ReaderState, error) {
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return book.ReaderState{}, err
	}
	state := a.store.State(bookID)
	resolved := position.Range{Start: loaded.Resolve(span.Start), End: loaded.Resolve(span.End)}
	state.Current = resolved.End
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now()
	}
	if !resolved.End.After(resolved.Start) || seconds < book.RequiredDwell(resolved) {
		// Not read. The current position still follows the eye.
		if err := a.store.SaveState(state); err != nil {
			return book.ReaderState{}, err
		}
		return state, nil
	}
	wasFinished := loaded.IsCompleted(state)
	fresh := state.Record(resolved, time.Now())
	if !wasFinished && loaded.IsCompleted(state) {
		now := time.Now()
		state.FinishedAt = &now
		_ = a.store.AppendEvent(store.Event{
			ID: store.NewID("evt"), BookID: bookID, Kind: "book.finished",
			At: now, Position: state.Boundary(),
		})
	}
	if err := a.store.SaveState(state); err != nil {
		return book.ReaderState{}, err
	}
	if len(fresh) > 0 {
		// Summaries follow the reader; companions wake up and catch up on what
		// was just read. Both are buffered, so a steady reader is one pass.
		a.runner.Follow(bookID)
		a.personas.OnPage(bookID)
	}
	return state, nil
}

// ---- memos ----------------------------------------------------------------

func (a *App) AddMemo(bookID, text, spoken, quote string, quoteRange position.Range) (store.Memo, error) {
	if strings.TrimSpace(text) == "" {
		return store.Memo{}, errors.New("メモが空です")
	}
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return store.Memo{}, err
	}
	memos, err := a.store.Memos(bookID)
	if err != nil {
		return store.Memo{}, err
	}
	at := loaded.Resolve(quoteRange.Start)
	if quote == "" {
		at = a.store.State(bookID).Current
	}
	memo := store.Memo{
		ID:        store.NewID("memo"),
		BookID:    bookID,
		Text:      strings.TrimSpace(text),
		Spoken:    strings.TrimSpace(spoken),
		Quote:     quote,
		Range:     position.Range{Start: at, End: loaded.Resolve(quoteRange.End)},
		Position:  at,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	memos = append(memos, memo)
	if err := a.store.SaveMemos(bookID, memos); err != nil {
		return store.Memo{}, err
	}
	_ = a.store.AppendEvent(store.Event{
		ID: store.NewID("evt"), BookID: bookID, Kind: "memo.created", At: memo.CreatedAt,
		MemoID: memo.ID, Text: memo.Text, Quote: memo.Quote, Position: memo.Position,
	})
	// "This one's suspicious" is the remark most worth a reply. The answer comes
	// back a little later, in the thread, the way a message would.
	a.personas.OnMemo(bookID, memo.Text)
	return memo, nil
}

func (a *App) UpdateMemo(bookID, memoID, text string) (store.Memo, error) {
	memos, err := a.store.Memos(bookID)
	if err != nil {
		return store.Memo{}, err
	}
	for i := range memos {
		if memos[i].ID != memoID {
			continue
		}
		memos[i].Text = strings.TrimSpace(text)
		memos[i].UpdatedAt = time.Now()
		if err := a.store.SaveMemos(bookID, memos); err != nil {
			return store.Memo{}, err
		}
		_ = a.store.AppendEvent(store.Event{
			ID: store.NewID("evt"), BookID: bookID, Kind: "memo.updated", At: memos[i].UpdatedAt,
			MemoID: memoID, Text: memos[i].Text, Quote: memos[i].Quote, Position: memos[i].Position,
		})
		return memos[i], nil
	}
	return store.Memo{}, errors.New("メモが見つかりません")
}

func (a *App) DeleteMemo(bookID, memoID string) error {
	memos, err := a.store.Memos(bookID)
	if err != nil {
		return err
	}
	kept := memos[:0]
	var removed *store.Memo
	for i := range memos {
		if memos[i].ID == memoID {
			memo := memos[i]
			removed = &memo
			continue
		}
		kept = append(kept, memos[i])
	}
	if removed == nil {
		return errors.New("メモが見つかりません")
	}
	if err := a.store.SaveMemos(bookID, kept); err != nil {
		return err
	}
	// The log keeps deletions: how the reading went includes what was taken back.
	return a.store.AppendEvent(store.Event{
		ID: store.NewID("evt"), BookID: bookID, Kind: "memo.deleted", At: time.Now(),
		MemoID: memoID, Text: removed.Text, Quote: removed.Quote, Position: removed.Position,
	})
}

func (a *App) Memos(bookID string) ([]store.Memo, error) { return a.store.Memos(bookID) }

func (a *App) Timeline(bookID string) ([]store.Event, error) { return a.store.Timeline(bookID) }

// ---- asking ---------------------------------------------------------------

// Recall finds a half-remembered scene. It needs no model, so it works on a
// freshly imported book with nothing configured.
func (a *App) Recall(bookID, query string) ([]rag.Hit, error) {
	index, err := a.store.Index(bookID)
	if err != nil {
		return nil, errors.New("索引がまだありません")
	}
	state := a.store.State(bookID)
	return index.Search(query, state.Boundary(), 8), nil
}

// Conversation is the thread about a book, oldest first.
func (a *App) Conversation(bookID string) []store.Turn { return a.store.Conversation(bookID) }

// ClearConversation starts the thread again. The book, the memos and the
// reading log are untouched: only the working memory of the chat goes.
func (a *App) ClearConversation(bookID string) error {
	return a.store.SaveConversation(bookID, nil)
}

// Ask answers a question and keeps it in the thread so the next one can follow
// on from it. Which path the question takes is decided in internal/answer, not
// by the reader.
func (a *App) Ask(bookID, question string) ([]store.Turn, error) {
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return nil, err
	}
	memos, err := a.store.Memos(bookID)
	if err != nil {
		return nil, err
	}
	index, _ := a.store.Index(bookID)
	client, settings := a.client()
	state := a.store.State(bookID)
	turns := a.store.Conversation(bookID)
	deps := answer.Deps{
		Book:      loaded,
		State:     state,
		Knowledge: a.store.Knowledge(bookID),
		Index:     index,
		Memos:     memos,
		History:   turns,
		Client:    client,
		Search:    a.search(),
		Settings:  settings,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	reply, err := answer.Answer(ctx, deps, question)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if strings.TrimSpace(question) != "" {
		asked := store.Turn{
			ID: store.NewID("turn"), Role: "user", Speaker: "user", Mode: string(reply.Mode),
			Text: strings.TrimSpace(question), Anchor: state.Current, Boundary: state.Boundary(), At: now,
		}
		turns = append(turns, asked)
		// The timeline is the reader's own words, so a question belongs in it
		// as much as a memo does. It points back at the message, which has the
		// answer directly underneath it.
		_ = a.store.AppendEvent(store.Event{
			ID: store.NewID("evt"), BookID: bookID, Kind: "question.asked", At: now,
			TurnID: asked.ID, Text: asked.Text, Position: asked.Anchor,
		})
	}
	assistant := store.Turn{
		ID: store.NewID("turn"), Role: "assistant", Speaker: "app", Mode: string(reply.Mode),
		Text: reply.Text, Anchor: state.Current, Boundary: reply.Boundary, At: now,
	}
	if reply.Locked {
		assistant.Text = reply.Reason
	}
	for _, source := range reply.Sources {
		assistant.Sources = append(assistant.Sources, store.TurnSource{
			Label: source.Label, Snippet: source.Snippet, Position: source.Position,
		})
	}
	turns = append(turns, assistant)
	if err := a.store.SaveConversation(bookID, turns); err != nil {
		return nil, err
	}
	// The companions read the room too: what the reader just asked is something
	// they may have a view on.
	if strings.TrimSpace(question) != "" {
		a.personas.OnSaid(bookID, strings.TrimSpace(question))
	}
	return a.store.Conversation(bookID), nil
}

// Say puts a remark in the thread without asking the app anything.
//
// Talking to the companions is not the same as asking the book a question, and
// making the reader phrase everything as a question would be the app deciding
// how they are allowed to think out loud.
func (a *App) Say(bookID, text string) ([]store.Turn, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return a.store.Conversation(bookID), nil
	}
	state := a.store.State(bookID)
	turns := a.store.Conversation(bookID)
	said := store.Turn{
		ID: store.NewID("turn"), Role: "user", Speaker: "user", Mode: "say",
		Text: text, Anchor: state.Current, Boundary: state.Boundary(), At: time.Now(),
	}
	turns = append(turns, said)
	if err := a.store.SaveConversation(bookID, turns); err != nil {
		return nil, err
	}
	_ = a.store.AppendEvent(store.Event{
		ID: store.NewID("evt"), BookID: bookID, Kind: "said", At: said.At,
		TurnID: said.ID, Text: said.Text, Position: said.Anchor,
	})
	a.personas.OnSaid(bookID, text)
	return a.store.Conversation(bookID), nil
}

// Calendar folds the reading log into days, across every book.
func (a *App) Calendar() ([]store.Day, error) { return a.store.Calendar() }

// AllTimeline is the reader's own entries across every book, newest first.
func (a *App) AllTimeline() ([]store.TimelineEntry, error) { return a.store.AllTimeline() }

const memoTidyPrompt = `読書中の口述メモを、本人の言い回しを保ったまま読み返せる文に整えてください。
言い淀み・繰り返し・認識誤りらしき箇所だけを直し、内容を足さないこと。出力は整えた本文のみ。`

// TidyMemo turns a spoken memo into something worth reading back.
//
// The raw utterance is returned with it and stored beside the tidied text: the
// tidying is one model's guess, and a better one may come along.
func (a *App) TidyMemo(spoken string) (map[string]string, error) {
	spoken = strings.TrimSpace(spoken)
	result := map[string]string{"text": spoken, "spoken": spoken}
	if spoken == "" {
		return result, nil
	}
	client, settings := a.client()
	if settings.LightModel == "" || !settings.Speech.TidyWithModel {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	text, err := client.Chat(ctx, llm.Request{
		Model: settings.LightModel,
		Messages: []llm.Message{
			{Role: "system", Content: memoTidyPrompt},
			{Role: "user", Content: spoken},
		},
	})
	if err == nil && strings.TrimSpace(text) != "" {
		result["text"] = strings.TrimSpace(text)
	}
	return result, nil
}

// ---- settings and preparation --------------------------------------------

func (a *App) Settings() store.Settings { return a.store.Settings() }

func (a *App) SaveSettings(settings store.Settings) error { return a.store.SaveSettings(settings) }

func (a *App) Status(bookID string) store.PipelineStatus { return a.store.PipelineStatus(bookID) }

// Prepare runs the summaries again, keeping the chapters that already
// succeeded. For a novel that means catching up on whatever the reader has read
// and no digest covers; for a technical book it means the import pass again.
func (a *App) Prepare(bookID string) error {
	loaded, err := a.store.LoadBook(bookID)
	if err != nil {
		return err
	}
	a.runner.Start(loaded)
	if loaded.Category == book.CategoryNovel {
		a.runner.Follow(bookID)
	}
	return nil
}

// Workspace is where everything is stored, shown in settings so the reader can
// find, copy or back up their books and memos.
func (a *App) Workspace() string { return a.store.Root() }

func (a *App) Version() string { return fmt.Sprintf("dokking %s", appVersion()) }
