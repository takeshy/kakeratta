package persona

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// fake stands in for the inference endpoint. It records every prompt, which is
// how the ordering rules are checked: what the heavy model was given is the
// evidence that it saw the light one's remark.
type fake struct {
	mu      sync.Mutex
	prompts []string
	reply   func(system, user string) string
}

func (f *fake) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		system, user := "", ""
		for _, message := range body.Messages {
			text, _ := message.Content.(string)
			if message.Role == "system" {
				system += text
			} else {
				user += text + "\n"
			}
		}
		f.mu.Lock()
		f.prompts = append(f.prompts, system+"\n----\n"+user)
		f.mu.Unlock()
		content := f.reply(system, user)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fake) seen(needle string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, prompt := range f.prompts {
		if strings.Contains(prompt, needle) {
			return true
		}
	}
	return false
}

// promptFor returns the prompt sent to the persona with this name.
func (f *fake) promptFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, prompt := range f.prompts {
		if strings.HasPrefix(prompt, "あなたは「"+name+"」") {
			return prompt
		}
	}
	return ""
}

func setup(t *testing.T, reply func(system, user string) string, active ...string) (*Runner, *store.Store, *fake) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &fake{reply: reply}
	server := stub.start(t)
	settings := s.Settings()
	settings.BaseURL = server.URL
	settings.APIKey = "test"
	settings.Active = active
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	catalogue := NewCatalogue(s.SkillDir())
	runner := New(s, catalogue, func() (*llm.Client, store.Settings) {
		return llm.New(server.URL, "test"), s.Settings()
	})
	return runner, s, stub
}

func post(t *testing.T, s *store.Store, text string) timeline.Entry {
	t.Helper()
	entry := timeline.Entry{
		ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User,
		Text: text, At: time.Now(),
	}
	entry.Root = entry.ID
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func remarks(t *testing.T, s *store.Store) []timeline.Entry {
	t.Helper()
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	var out []timeline.Entry
	for _, entry := range entries {
		if entry.Kind == timeline.KindRemark {
			out = append(out, entry)
		}
	}
	return out
}

// The rule the exchange rests on: the light model goes first, and the heavy one
// reads what it said. Run together, the considered reply would have nothing to
// pick up on.
func TestTheHeavyOneReadsWhatTheLightOneSaid(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move","gourmet"]}`
		case strings.Contains(system, "あなたは「みち」"):
			return `{"action":"speak","text":"渋谷まで20分","notes":"渋谷によく行く"}`
		default:
			return `{"action":"speak","text":"それなら店も見ておく","notes":"渋谷の店を探した"}`
		}
	}, "move", "gourmet")

	entry := post(t, s, "20時に渋谷に着きたい")
	runner.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 2 {
		t.Fatalf("remarks = %+v", said)
	}
	if said[0].Speaker != "move" || said[1].Speaker != "gourmet" {
		t.Fatalf("order = %s then %s, want the light one first", said[0].Speaker, said[1].Speaker)
	}
	heavy := stub.promptFor("たべ")
	if heavy == "" {
		t.Fatal("the heavy persona was never asked")
	}
	if !strings.Contains(heavy, "渋谷まで20分") {
		t.Fatalf("the heavy one could not see the light one's remark:\n%s", heavy)
	}
	// And the light one cannot have seen a remark that had not happened yet.
	if strings.Contains(stub.promptFor("みち"), "それなら店も見ておく") {
		t.Fatal("the light one saw the future")
	}
}

// Not speaking is an outcome, not a failure - and it is not the same as not
// remembering.
func TestSilenceStillWritesNotes(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["tech"]}`
		}
		if strings.Contains(user, "notes_write の結果") {
			return `{"action":"silent"}`
		}
		return `{"action":"tool","tool":"notes_write","args":{"path":"threads/errors.md","text":"Goのエラーハンドリングを追いかけている"}}`
	}, "tech")

	entry := post(t, s, "errors.Join の話")
	runner.react(entry.ID)

	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("something was said when it should have been quiet: %+v", said)
	}
	memory := s.Memory("tech")
	if len(memory.Notes) != 1 || !strings.Contains(memory.Notes[0].Text, "エラーハンドリング") {
		t.Fatalf("notes = %+v", memory.Notes)
	}
	if memory.Wakes != 1 {
		t.Fatalf("wakes = %d", memory.Wakes)
	}
}

// Being named is the one thing a persona does not get to decline, and it skips
// the routing entirely: choosing who to ask is the reason the domains are far
// apart in the first place.
func TestBeingNamedSkipsRoutingAndForcesAnAnswer(t *testing.T) {
	tries := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		tries++
		if tries == 1 {
			return `{"action":"silent","notes":"ひとまず黙る"}`
		}
		return `{"action":"speak","text":"呼ばれたので答えます","notes":"呼ばれた"}`
	}, "gourmet", "tech")

	entry := post(t, s, "@たべ これどう思う")
	runner.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 1 || said[0].Speaker != "gourmet" {
		t.Fatalf("remarks = %+v, want the named one to have answered", said)
	}
	// Routing said nobody, so the persona that was not named must not have run.
	if stub.promptFor("くだ") != "" {
		t.Fatal("a persona the routing excluded was woken anyway")
	}
	if !strings.Contains(stub.promptFor("たべ"), "名指し") {
		t.Fatal("the persona was not told it had been named")
	}
}

// A post only reaches the personas it was routed to. That is what lets the
// number of personas grow with the number of domains.
func TestRoutingKeepsThePostFromEveryoneElse(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["tech"]}`
		}
		return `{"action":"speak","text":"ふむ","notes":"見た"}`
	}, "tech", "gourmet", "book")

	entry := post(t, s, "Go 1.25 のリリースノート")
	runner.react(entry.ID)

	if stub.promptFor("たべ") != "" || stub.promptFor("しおり") != "" {
		t.Fatal("a post reached a persona it was not routed to")
	}
	if stub.promptFor("くだ") == "" {
		t.Fatal("the persona it was routed to never saw it")
	}
	// Nothing was even written for the others: no wake, no notes.
	if s.Memory("gourmet").Wakes != 0 {
		t.Fatal("an unrouted persona was woken")
	}
}

// Routing is an optimisation. When it cannot be done, a post must not be
// swallowed: everyone gets it and decides for themselves.
func TestRoutingFailureDeliversToEveryone(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return "モデルが壊れた返事"
		}
		return `{"action":"silent","notes":"見た"}`
	}, "tech", "gourmet")

	entry := post(t, s, "何か")
	runner.react(entry.ID)

	if stub.promptFor("くだ") == "" || stub.promptFor("たべ") == "" {
		t.Fatal("a post was lost because the routing failed")
	}
}

// A persona reads and writes its own notes. The app hands over the names and
// nothing else - which is also why the notes of a long-lived persona do not
// have to be paid for on every wake.
func TestAPersonaReadsAndWritesItsOwnNotes(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["tech"]}`
		case strings.Contains(user, "notes_write の結果"):
			return `{"action":"silent"}`
		case strings.Contains(user, "notes_read の結果"):
			return `{"action":"tool","tool":"notes_write","args":{"path":"threads/old.md","text":"前から追いかけていること\n* 続き"}}`
		default:
			return `{"action":"tool","tool":"notes_read","args":{"paths":["threads/old.md"]}}`
		}
	}, "tech")

	if err := s.WriteNote("tech", "threads/old.md", "前から追いかけていること"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("tech", "index.md", "最初の目次"); err != nil {
		t.Fatal(err)
	}
	_ = post(t, s, "関係のない古い投稿")
	entry := post(t, s, "新しい投稿")
	runner.react(entry.ID)

	// The first prompt has the names, not the contents: reading is the
	// persona's own call, and a memory it does not need costs nothing.
	prompt := stub.promptFor("くだ")
	if !strings.Contains(prompt, "threads/old.md") {
		t.Fatalf("the persona was not told what it has:\n%s", prompt)
	}
	if strings.Contains(prompt, "前から追いかけていること") {
		t.Fatalf("the contents were handed over unasked:\n%s", prompt)
	}
	if strings.Contains(prompt, "関係のない古い投稿") {
		t.Fatalf("another post was handed over:\n%s", prompt)
	}
	// It read one, wrote one, and left the other alone.
	if !stub.seen("前から追いかけていること") {
		t.Fatal("notes_read never handed the contents back")
	}
	notes := map[string]string{}
	for _, note := range s.Memory("tech").Notes {
		notes[note.Path] = note.Text
	}
	if !strings.Contains(notes["threads/old.md"], "続き") {
		t.Fatalf("the note it wrote = %q", notes["threads/old.md"])
	}
	if notes["index.md"] != "最初の目次" {
		t.Fatalf("an untouched note changed: %q", notes["index.md"])
	}
}

// Tools are the declared ones and nothing else. The declaration is the boundary
// and it is checked where the call is made.
func TestAToolThatWasNotDeclaredIsRefused(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["tech"]}`
		}
		if strings.Contains(user, "route の結果") {
			return `{"action":"speak","text":"使えなかった","notes":"道具の確認"}`
		}
		return `{"action":"tool","tool":"route","args":{"to":"渋谷"},"notes":"道具を試す"}`
	}, "tech")

	entry := post(t, s, "どこか")
	runner.react(entry.ID)

	if !stub.seen("宣言されていません") {
		t.Fatal("a tool outside the declaration was allowed through")
	}
}

// The memo format is handed over as a skill, verbatim. The app does not parse
// what comes back - so the only thing to check is that the persona was actually
// given it, along with its own policy for what to make a concept of.
func TestThePersonaIsHandedTheMemoSkill(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		if strings.Contains(user, "notes_write の結果") {
			return `{"action":"silent"}`
		}
		return `{"action":"tool","tool":"notes_write","args":{"path":"index.md","text":"# 傾向\n"}}`
	}, "gourmet")

	entry := post(t, s, "ラーメン")
	runner.react(entry.ID)

	prompt := stub.promptFor("たべ")
	for _, want := range []string{
		"Open Knowledge Format", // the skill itself, as it ships upstream
		"type",                  // the one required field
		"type: Preference",      // and this persona's own policy for using it
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("%q missing from the prompt", want)
		}
	}
	// Whatever it wrote is stored as it wrote it. Nothing here inspects it.
	notes := s.Memory("gourmet").Notes
	if len(notes) != 1 || notes[0].Path != "index.md" {
		t.Fatalf("notes = %+v", notes)
	}
}
