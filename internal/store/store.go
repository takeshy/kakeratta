// Package store keeps everything on disk as plain JSON under one directory.
//
// No database and no account: a book, where the reader is in it, their memos
// and what the pipeline learned are files they can read, copy and back up. A
// prepared public-domain book is therefore just a directory that can be shared.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/rag"
)

// Provider is where the models run.
//
// All three speak the OpenAI chat-completions shape except OpenCode, which has
// its own session API; internal/llm hides that difference so the rest of the
// app only ever asks for a model by name.
type Provider string

const (
	ProviderNebius Provider = "nebius"
	ProviderOpenAI Provider = "openai-compatible"
	ProviderLocal  Provider = "local"
)

// LocalFramework is which local server is listening, and only decides the
// default address and whether the OpenCode session API is used.
type LocalFramework string

// LocalFrameworks are the local servers with their usual addresses.
var LocalFrameworks = map[LocalFramework]struct {
	Label    string `json:"label"`
	Endpoint string `json:"endpoint"`
}{
	"ollama":      {"Ollama", "http://127.0.0.1:11434/v1"},
	"lm-studio":   {"LM Studio", "http://127.0.0.1:1234/v1"},
	"anythingllm": {"AnythingLLM", "http://127.0.0.1:3001/api/v1/openai"},
	"vllm":        {"vLLM", "http://127.0.0.1:8000/v1"},
	"opencode":    {"OpenCode (Local)", "http://127.0.0.1:4096"},
}

// Nebius Token Factory is the default: it is where this app's models run, and
// its inference API is OpenAI-compatible, so nothing else in the app changes.
const (
	NebiusBaseURL   = "https://api.tokenfactory.nebius.com/v1"
	NemotronSuper   = "nvidia/nemotron-3-super-120b-a12b"
	DefaultLightTag = "nvidia/nemotron-3-super-120b-a12b"
)

// Settings are the endpoints and models. Credentials stay here and are never
// sent anywhere but the endpoint they belong to.
type Settings struct {
	Provider       Provider       `json:"provider"`
	LocalFramework LocalFramework `json:"localFramework"`
	BaseURL        string         `json:"baseUrl"`
	APIKey         string         `json:"apiKey"`
	ChatModel      string         `json:"chatModel"`
	LightModel     string         `json:"lightModel"`
	HeavyModel     string         `json:"heavyModel"`
	Speech         SpeechSettings `json:"speech"`
	Search         SearchSettings `json:"search"`
	// SendBookAsFile passes a technical book to the model as the file itself
	// rather than as extracted text, so code listings, tables and figures
	// survive. Not every provider takes a file; when one refuses, the app falls
	// back to text on its own, so leaving this on costs at most one attempt.
	SendBookAsFile bool    `json:"sendBookAsFile"`
	ReadAloudRate  float64 `json:"readAloudRate"`
	ReadAloudVoice string  `json:"readAloudVoice"`
}

// SearchSettings is the one way out to the open web.
//
// A persona never holds this: it declares that it needs search, and the app
// makes the call on its behalf. Swapping the provider, and insisting on a
// source link for every claim, are then things the app can guarantee rather
// than things each plugin has to be trusted to do.
type SearchSettings struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
}

// SpeechSettings are the dictation settings, ported whole from speech-popup so
// the same engines, phrases and rules work here.
type SpeechSettings struct {
	Provider        string `json:"provider"`
	EndpointType    string `json:"endpointType"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey"`
	Model           string `json:"model"`
	Language        string `json:"language"`
	SilenceSeconds  int    `json:"silenceSeconds"`
	SendPhrase      string `json:"sendPhrase"`
	Replacements    string `json:"replacements"`
	SymbolCommands  string `json:"symbolCommands"`
	VertexProjectID string `json:"vertexProjectId"`
	TidyWithModel   bool   `json:"tidyWithModel"`
}

func defaultSettings() Settings {
	return Settings{
		Provider:   ProviderNebius,
		BaseURL:    NebiusBaseURL,
		ChatModel:  NemotronSuper,
		LightModel: DefaultLightTag,
		HeavyModel: NemotronSuper,
		Speech: SpeechSettings{
			Provider:       "browser",
			EndpointType:   "openai",
			BaseURL:        "https://api.openai.com/v1",
			Model:          "whisper-1",
			Language:       "auto",
			SilenceSeconds: 3,
			SendPhrase:     "over, オーバー",
			TidyWithModel:  true,
		},
		ReadAloudRate:  1,
		SendBookAsFile: true,
	}
}

// EndpointFor is the address a provider listens on by default.
func EndpointFor(provider Provider, framework LocalFramework) string {
	switch provider {
	case ProviderLocal:
		if entry, ok := LocalFrameworks[framework]; ok {
			return entry.Endpoint
		}
		return LocalFrameworks["ollama"].Endpoint
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	default:
		return NebiusBaseURL
	}
}

// Memo is a note with the text it was taken from, so the reader can go back to
// the passage rather than to a page number.
type Memo struct {
	ID        string            `json:"id"`
	BookID    string            `json:"bookId"`
	Text      string            `json:"text"`
	Spoken    string            `json:"spoken,omitempty"`
	Quote     string            `json:"quote"`
	Range     position.Range    `json:"range"`
	Position  position.Position `json:"position"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// Event is one entry of the reading log. Memo edits and deletions are recorded
// too: the log is the record of how this reader read this book.
type Event struct {
	ID     string    `json:"id"`
	BookID string    `json:"bookId"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	MemoID string    `json:"memoId,omitempty"`
	// TurnID points at the message in the thread this entry came from, so the
	// timeline needs no copy of the text's context: following it lands on the
	// question with the answer right underneath.
	TurnID   string            `json:"turnId,omitempty"`
	Text     string            `json:"text,omitempty"`
	Quote    string            `json:"quote,omitempty"`
	Position position.Position `json:"position"`
}

// Turn is one message of the conversation about a book.
//
// The boundary it was answered at is kept with it: the same question asked a
// hundred pages later is a different question, and an old answer should read as
// something said earlier, not as something still true.
type Turn struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	// Speaker is who said it: "user", or the id of the persona that did. One
	// thread carries every voice in the book, which is what makes a persona
	// speaking up unprompted land in the same place as an answer.
	Speaker string       `json:"speaker"`
	Mode    string       `json:"mode"`
	Text    string       `json:"text"`
	Sources []TurnSource `json:"sources,omitempty"`
	// Anchor is the place in the book the message is about, so every message in
	// the thread can take the reader back to the text.
	Anchor   position.Position `json:"anchor"`
	Boundary position.Position `json:"boundary"`
	At       time.Time         `json:"at"`
}

// TurnSource is a place in the book an answer pointed at.
type TurnSource struct {
	Label    string            `json:"label"`
	Snippet  string            `json:"snippet,omitempty"`
	Position position.Position `json:"position"`
}

// Progress is what the shelf shows: a book and how far into it the reader is.
type Progress struct {
	Book     BookMeta         `json:"book"`
	State    book.ReaderState `json:"state"`
	Fraction float64          `json:"fraction"`
	Finished bool             `json:"finished"`
	Memos    int              `json:"memos"`
	// Read is how much of the book has actually been read, which is not the
	// same as how far in the reader has got when they skipped something.
	Read int `json:"read"`
}

// BookMeta is the shelf-sized part of a book, kept separately so opening the
// shelf does not load every book's text.
type BookMeta struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Author     string            `json:"author"`
	Category   book.Category     `json:"category"`
	Format     book.Format       `json:"format"`
	Source     string            `json:"source"`
	AddedAt    time.Time         `json:"addedAt"`
	End        position.Position `json:"end"`
	ContentEnd position.Position `json:"contentEnd"`
	Sections   int               `json:"sections"`
}

func metaOf(b *book.Book) BookMeta {
	return BookMeta{
		ID: b.ID, Title: b.Title, Author: b.Author, Category: b.Category, Format: b.Format, Source: b.Source,
		AddedAt: b.AddedAt, End: b.End, ContentEnd: b.ContentEnd, Sections: len(b.Sections),
	}
}

// Store is the on-disk workspace.
type Store struct {
	root string
	mu   sync.RWMutex
}

func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("store root is empty")
	}
	if err := os.MkdirAll(filepath.Join(root, "books"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// DefaultRoot is where the workspace lives when none is given.
func DefaultRoot() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "dokking")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dokking")
}

func (s *Store) Root() string { return s.root }

func (s *Store) bookDir(id string) string { return filepath.Join(s.root, "books", sanitise(id)) }

func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := defaultSettings()
	_ = readJSON(filepath.Join(s.root, "settings.json"), &settings)
	if settings.ReadAloudRate <= 0 {
		settings.ReadAloudRate = 1
	}
	if settings.Provider == "" {
		settings.Provider = ProviderNebius
	}
	if strings.TrimSpace(settings.BaseURL) == "" {
		settings.BaseURL = EndpointFor(settings.Provider, settings.LocalFramework)
	}
	return settings
}

func (s *Store) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.root, "settings.json"), settings)
}

func (s *Store) SaveBook(b *book.Book) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.bookDir(b.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "book.json"), b); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "meta.json"), metaOf(b))
}

func (s *Store) LoadBook(id string) (*book.Book, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b book.Book
	if err := readJSON(filepath.Join(s.bookDir(id), "book.json"), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) DeleteBook(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.bookDir(id))
}

// Shelf lists every book with where the reader is in it, most recently read
// first: the shelf is a list of current positions, not of covers.
func (s *Store) Shelf() ([]Progress, error) {
	s.mu.RLock()
	entries, err := os.ReadDir(filepath.Join(s.root, "books"))
	s.mu.RUnlock()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var shelf []Progress
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var meta BookMeta
		if err := readJSON(filepath.Join(s.root, "books", entry.Name(), "meta.json"), &meta); err != nil {
			continue
		}
		state := s.State(meta.ID)
		memos, _ := s.Memos(meta.ID)
		boundary := state.Boundary()
		shelf = append(shelf, Progress{
			Book:     meta,
			State:    state,
			Fraction: position.Progress(boundary, meta.End),
			Finished: meta.Category != book.CategoryTechnical && meta.ContentEnd.Absolute > 0 && boundary.AtOrAfter(meta.ContentEnd),
			Memos:    len(memos),
			Read:     state.ReadChars(),
		})
	}
	sort.Slice(shelf, func(i, j int) bool {
		return shelf[i].State.UpdatedAt.After(shelf[j].State.UpdatedAt)
	})
	return shelf, nil
}

func (s *Store) State(bookID string) book.ReaderState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := book.ReaderState{BookID: bookID}
	_ = readJSON(filepath.Join(s.bookDir(bookID), "state.json"), &state)
	state.BookID = bookID
	return state
}

func (s *Store) SaveState(state book.ReaderState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.UpdatedAt = time.Now()
	return writeJSON(filepath.Join(s.bookDir(state.BookID), "state.json"), state)
}

func (s *Store) Memos(bookID string) ([]Memo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var memos []Memo
	if err := readJSON(filepath.Join(s.bookDir(bookID), "memos.json"), &memos); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(memos, func(i, j int) bool {
		return memos[i].Position.Absolute < memos[j].Position.Absolute
	})
	return memos, nil
}

func (s *Store) SaveMemos(bookID string, memos []Memo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.bookDir(bookID), "memos.json"), memos)
}

func (s *Store) Timeline(bookID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var events []Event
	if err := readJSON(filepath.Join(s.bookDir(bookID), "timeline.json"), &events); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	return events, nil
}

func (s *Store) AppendEvent(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.bookDir(event.BookID), "timeline.json")
	var events []Event
	_ = readJSON(path, &events)
	events = append(events, event)
	return writeJSON(path, events)
}

func (s *Store) Conversation(bookID string) []Turn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var turns []Turn
	_ = readJSON(filepath.Join(s.bookDir(bookID), "chat.json"), &turns)
	return turns
}

func (s *Store) SaveConversation(bookID string, turns []Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A conversation is a working memory, not an archive: the oldest turns are
	// dropped so the file cannot grow without bound.
	if len(turns) > 200 {
		turns = turns[len(turns)-200:]
	}
	return writeJSON(filepath.Join(s.bookDir(bookID), "chat.json"), turns)
}

func (s *Store) Knowledge(bookID string) knowledge.Knowledge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := knowledge.Knowledge{BookID: bookID}
	_ = readJSON(filepath.Join(s.bookDir(bookID), "knowledge.json"), &k)
	return k
}

func (s *Store) SaveKnowledge(k knowledge.Knowledge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.bookDir(k.BookID), "knowledge.json"), k)
}

func (s *Store) SaveIndex(index *rag.Index) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.bookDir(index.BookID), "index.json"), index)
}

func (s *Store) Index(bookID string) (*rag.Index, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var index rag.Index
	if err := readJSON(filepath.Join(s.bookDir(bookID), "index.json"), &index); err != nil {
		return nil, err
	}
	return &index, nil
}

// PipelineStatus is how far the ingest pipeline got, kept on disk so a restart
// shows "第3章まで完了" instead of starting again.
type PipelineStatus struct {
	BookID    string    `json:"bookId"`
	Stage     string    `json:"stage"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Store) PipelineStatus(bookID string) PipelineStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := PipelineStatus{BookID: bookID, Stage: "pending"}
	_ = readJSON(filepath.Join(s.bookDir(bookID), "pipeline.json"), &status)
	status.BookID = bookID
	return status
}

func (s *Store) SavePipelineStatus(status PipelineStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status.UpdatedAt = time.Now()
	return writeJSON(filepath.Join(s.bookDir(status.BookID), "pipeline.json"), status)
}

func readJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// writeJSON writes through a temporary file: a crash mid-write must not cost
// the reader their position or their memos.
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func sanitise(id string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// NewID returns an identifier for a memo or event.
func NewID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
