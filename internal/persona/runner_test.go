package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// fake stands in for the inference endpoint. It records every prompt, which is
// how the ordering rules are checked: what the heavy model was given is the
// evidence that it saw the light one's remark.
type fake struct {
	mu      sync.Mutex
	prompts []string
	reply   func(system, user string) string
	// reviews are the app's own checks, kept apart from what the personas were
	// asked so a test counting persona calls is not counting those. review
	// scripts a verdict; unset, every workflow passes.
	reviews []string
	review  func(user string) string
}

func (f *fake) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    any    `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		system, user := "", ""
		for _, message := range body.Messages {
			text, _ := message.Content.(string)
			switch {
			case message.Role == "system":
				system += text
				continue
			case message.ToolCallID != "":
				// A result is bound to its call by id rather than by anything in
				// the text, so the transcript is put back together from the id
				// this fake minted below.
				text = strings.TrimPrefix(message.ToolCallID, "call_") + " の結果:\n" + text
			}
			for _, call := range message.ToolCalls {
				text += call.Function.Name + " " + call.Function.Arguments
			}
			user += text + "\n"
		}
		// What a persona may call travels in the request now rather than in the
		// prompt, so it is recorded beside it - a test asking whether a persona
		// was told about a tool is asking about this - but kept out of what the
		// reply sees, where a tool name would match every round.
		offered := ""
		for _, tool := range body.Tools {
			offered += tool.Function.Name + " — " + tool.Function.Description + "\n"
		}
		w.Header().Set("Content-Type", "application/json")
		if names := toolNames(body.Tools); names["submit_review"] {
			f.mu.Lock()
			f.reviews = append(f.reviews, user)
			script := f.review
			f.mu.Unlock()
			verdict := `{"verdict":"pass","summary":"ok","issues":[]}`
			if script != nil {
				verdict = script(user)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": asCall(verdict, names)}},
			})
			return
		}
		f.mu.Lock()
		f.prompts = append(f.prompts, system+"\n----\n"+user+"\n----\n"+offered)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": asCall(f.reply(system, user), toolNames(body.Tools))}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func toolNames[T any](tools []T) map[string]bool {
	out := map[string]bool{}
	raw, _ := json.Marshal(tools)
	var listed []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	_ = json.Unmarshal(raw, &listed)
	for _, tool := range listed {
		out[tool.Function.Name] = true
	}
	return out
}

// asCall turns what a test wrote into what the runner reads, depending on what
// the request was for.
//
// Tests still describe a persona's turn in the old {"action":...} shape, which
// is the clearest way to say "it says this" or "it stays quiet"; for a request
// that wants a workflow that becomes the one-step workflow doing it. A test that
// needs more writes the workflow itself as {"steps":[...]}. A review nobody
// scripted passes, and a compose step gets the text of a scripted speak.
func asCall(content string, offered map[string]bool) map[string]any {
	call := func(name string, args any) map[string]any {
		raw, _ := json.Marshal(args)
		return map[string]any{"content": "", "tool_calls": []any{map[string]any{
			"id": "call_" + name, "type": "function",
			"function": map[string]any{"name": name, "arguments": string(raw)},
		}}}
	}
	var parsed struct {
		Action  string         `json:"action"`
		Text    string         `json:"text"`
		Tool    string         `json:"tool"`
		Args    map[string]any `json:"args"`
		Deliver []string       `json:"deliver"`
		Steps   []any          `json:"steps"`
		Verdict string         `json:"verdict"`
	}
	scripted := json.Unmarshal([]byte(content), &parsed) == nil
	switch {
	case offered["deliver"]:
		if scripted && parsed.Deliver != nil {
			// The scripted reply is the call as written, reasons included.
			return call("deliver", json.RawMessage(content))
		}
		return map[string]any{"content": content}

	case offered["submit_review"]:
		if scripted && parsed.Verdict != "" {
			return call("submit_review", json.RawMessage(content))
		}
		return call("submit_review", map[string]any{"verdict": "pass", "summary": "ok", "issues": []any{}})

	case offered["submit_workflow"]:
		if !scripted {
			return map[string]any{"content": content}
		}
		if parsed.Steps != nil {
			return call("submit_workflow", json.RawMessage(content))
		}
		empty := map[string]any{"type": "", "path": "", "text": "", "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""}
		step := func(fields map[string]any) map[string]any {
			out := map[string]any{}
			for key, value := range empty {
				out[key] = value
			}
			for key, value := range fields {
				out[key] = value
			}
			return out
		}
		steps := []any{}
		switch parsed.Action {
		case "speak":
			steps = append(steps, step(map[string]any{"type": "speak", "text": parsed.Text}))
		case "tool":
			args, _ := json.Marshal(parsed.Args)
			switch parsed.Tool {
			case "notes_write":
				steps = append(steps, step(map[string]any{"type": "write_note", "path": parsed.Args["path"], "text": parsed.Args["text"]}))
			case "notes_forget":
				steps = append(steps, step(map[string]any{"type": "forget_note", "path": parsed.Args["path"]}))
			case "schedule":
				if parsed.Args["action"] == "cancel" {
					steps = append(steps, step(map[string]any{"type": "cancel_schedule", "id": parsed.Args["id"]}))
				} else {
					steps = append(steps, step(map[string]any{"type": "tool", "tool": "schedule", "args": string(args)}))
				}
			default:
				// Anything else is a look before deciding.
				if !offered["read"] {
					break
				}
				read := map[string]any{"type": "tool", "paths": []any{}, "tool": parsed.Tool, "args": string(args)}
				switch parsed.Tool {
				case "notes_read":
					read = map[string]any{"type": "notes_read", "paths": parsed.Args["paths"], "tool": "", "args": "{}"}
				case "notes_list":
					read = map[string]any{"type": "notes_list", "paths": []any{}, "tool": "", "args": "{}"}
				}
				return call("read", map[string]any{"steps": []any{read}})
			}
		case "silent":
			// Staying quiet is said as such.
		case "":
			return map[string]any{"content": content}
		}
		if parsed.Action != "speak" {
			steps = append(steps, step(map[string]any{"type": "speak", "text": ""}))
		}
		return call("submit_workflow", map[string]any{"summary": "test", "steps": steps})

	case offered["silent"]:
		// The low tier's direct calls.
		if !scripted {
			return map[string]any{"content": content}
		}
		switch parsed.Action {
		case "speak":
			if offered["speak"] {
				return call("speak", map[string]any{"text": parsed.Text})
			}
			return call("silent", map[string]any{})
		case "tool":
			switch parsed.Tool {
			case "notes_write":
				return call("write_note", parsed.Args)
			case "notes_forget":
				return call("forget_note", parsed.Args)
			default:
				return call(parsed.Tool, parsed.Args)
			}
		}
		return call("silent", map[string]any{})
	}
	// A compose step: plain text back.
	if scripted && parsed.Action == "speak" {
		return map[string]any{"content": parsed.Text}
	}
	if scripted && parsed.Action != "" {
		return map[string]any{"content": ""}
	}
	return map[string]any{"content": content}
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

// The rule the exchange rests on: the low tier goes first, and the ones above
// it read what it said. Run together, the larger models would have nothing to
// pick up on.
func TestTheHigherTiersReadWhatTheLowerOneSaid(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move","gourmet"]}`
		case strings.Contains(system, "あなたは「michi」"):
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
	high := stub.promptFor("tabe")
	if high == "" {
		t.Fatal("the high persona was never asked")
	}
	if !strings.Contains(high, "渋谷まで20分") {
		t.Fatalf("the high one could not see the low one's remark:\n%s", high)
	}
	// And the low one cannot have seen a remark that had not happened yet.
	if strings.Contains(stub.promptFor("michi"), "それなら店も見ておく") {
		t.Fatal("the low one saw the future")
	}
}

// Three tiers in order, and then the rule that stops them talking to each
// other: a persona that already had its turn hears what came after it, keeps
// what it wants of it, and cannot answer.
func TestAnEarlierTierHearsTheLaterOnesButCannotAnswer(t *testing.T) {
	var listened []string
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["diary","book","gourmet"]}`
		case strings.Contains(user, "notes_write の結果"):
			return `{"action":"silent"}`
		}
		// A turn where it may not speak is a turn for the notes only.
		if strings.Contains(user, "このターンは発言できません") {
			for _, name := range []string{"nikki", "shiori", "tabe"} {
				if strings.Contains(system, "あなたは「"+name+"」") {
					listened = append(listened, name)
				}
			}
			// Even asked to speak, nothing may come of it.
			return `{"action":"speak","text":"割り込みたい"}`
		}
		switch {
		case strings.Contains(system, "あなたは「nikki」"):
			return `{"action":"speak","text":"三田まで30分。"}`
		case strings.Contains(system, "あなたは「shiori」"):
			return `{"action":"speak","text":"前に引いた線と重なる。"}`
		default:
			return `{"action":"speak","text":"みちの30分なら間に合う。"}`
		}
	}, "diary", "book", "gourmet")

	// Nothing built in is low any more - a persona with tools is middle or
	// higher - so the low one here is the test's own, holding no tools.
	if _, err := runner.catalogue.Install([]byte(`{"id":"diary","name":"nikki","domain":"日々のこと","instructions":"覚える","model":"low","tools":[]}`), nil); err != nil {
		t.Fatal(err)
	}
	entry := post(t, s, "ここ行きたい")
	runner.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 3 {
		t.Fatalf("remarks = %d, want one per tier: %+v", len(said), said)
	}
	if said[0].Speaker != "diary" || said[1].Speaker != "book" || said[2].Speaker != "gourmet" {
		t.Fatalf("order = %s, %s, %s; want low, middle, high",
			said[0].Speaker, said[1].Speaker, said[2].Speaker)
	}
	// Each tier saw what the ones below it said, and nothing above.
	if !strings.Contains(stub.promptFor("shiori"), "三田まで30分") {
		t.Fatal("middle did not see low")
	}
	if !strings.Contains(stub.promptFor("tabe"), "前に引いた線と重なる") {
		t.Fatal("high did not see middle")
	}

	// The two that went earlier were handed the later remarks, and the one that
	// went last has nothing after it to hear. A listening persona that tries to
	// speak is sent back once, so it may be asked twice.
	once := map[string]bool{}
	for _, name := range listened {
		once[name] = true
	}
	if len(once) != 2 {
		t.Fatalf("listened = %v, want the low and middle ones", listened)
	}
	for _, name := range listened {
		if name == "tabe" {
			t.Fatal("the last tier was given a listening turn it does not need")
		}
	}
	// They asked to speak on that turn and were not allowed to, so the timeline
	// did not grow. This is what keeps them from going back and forth on their
	// own.
	if len(remarks(t, s)) != 3 {
		t.Fatal("a listening turn produced a remark")
	}
}

// Nobody above said anything, so there is nothing to hear and no extra call to
// pay for - which is the common case, since staying quiet is the default.
func TestNoListeningTurnWhenTheHigherTiersStayedQuiet(t *testing.T) {
	listening := 0
	runner, s, _ := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move","gourmet"]}`
		case strings.Contains(user, "このターンは発言できません"):
			listening++
			return `{"action":"silent"}`
		case strings.Contains(system, "あなたは「michi」"):
			return `{"action":"speak","text":"30分。"}`
		default:
			return `{"action":"silent"}`
		}
	}, "move", "gourmet")

	entry := post(t, s, "ここ行きたい")
	runner.react(entry.ID)

	if listening != 0 {
		t.Fatalf("listening turns = %d, want none", listening)
	}
	if len(remarks(t, s)) != 1 {
		t.Fatalf("remarks = %+v", remarks(t, s))
	}
}

// Not speaking is an outcome, not a failure - and it is not the same as not
// remembering.
func TestSilenceStillWritesNotes(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		if strings.Contains(user, "notes_write の結果") {
			return `{"action":"silent"}`
		}
		return `{"action":"tool","tool":"notes_write","args":{"path":"threads/errors.md","text":"Goのエラーハンドリングを追いかけている"}}`
	}, "routine")

	entry := post(t, s, "errors.Join の話")
	runner.react(entry.ID)

	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("something was said when it should have been quiet: %+v", said)
	}
	memory := s.Memory("routine")
	if len(memory.Notes) != 1 || !strings.Contains(memory.Notes[0].Text, "エラーハンドリング") {
		t.Fatalf("notes = %+v", memory.Notes)
	}
	provenance := store.NoteSources(memory.Notes[0].Text)
	if len(provenance) != 1 || provenance[0].EntryID != entry.ID {
		t.Fatalf("silent note must link to its triggering post: %+v", provenance)
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
	}, "gourmet", "routine")

	entry := post(t, s, "@tabe これどう思う")
	runner.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 1 || said[0].Speaker != "gourmet" {
		t.Fatalf("remarks = %+v, want the named one to have answered", said)
	}
	// Routing said nobody, so the persona that was not named must not have run.
	if stub.promptFor("plana") != "" {
		t.Fatal("a persona the routing excluded was woken anyway")
	}
	if !strings.Contains(stub.promptFor("tabe"), "名指し") {
		t.Fatal("the persona was not told it had been named")
	}
}

// A named persona that plans to stay quiet is sent back: being asked is the one
// thing that is not its to decline, and the workflow is where that shows.
func TestBeingNamedStillForcesAnAnswerAfterUsingATool(t *testing.T) {
	step := 0
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		step++
		switch step {
		case 1:
			return `{"action":"tool","tool":"notes_read","args":{"paths":["index.md"]}}`
		case 2:
			return `{"action":"silent"}`
		default:
			return `{"action":"speak","text":"確認して答えます"}`
		}
	}, "book")

	entry := post(t, s, "@shiori この本について教えて")
	runner.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 1 || said[0].Text != "確認して答えます" {
		t.Fatalf("remarks = %+v, want an answer after the tool", said)
	}
	if step != 3 {
		t.Fatalf("calls = %d, want a read, the rejected silence, and its fix", step)
	}
}

// A post only reaches the personas it was routed to. That is what lets the
// number of personas grow with the number of domains.
func TestRoutingKeepsThePostFromEveryoneElse(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"action":"speak","text":"ふむ","notes":"見た"}`
	}, "routine", "gourmet", "book")

	entry := post(t, s, "Go 1.25 のリリースノート")
	runner.react(entry.ID)

	if stub.promptFor("tabe") != "" || stub.promptFor("shiori") != "" {
		t.Fatal("a post reached a persona it was not routed to")
	}
	if stub.promptFor("plana") == "" {
		t.Fatal("the persona it was routed to never saw it")
	}
	// Nothing was even written for the others: no wake, no notes.
	if s.Memory("gourmet").Wakes != 0 {
		t.Fatal("an unrouted persona was woken")
	}
}

// When routing fails, nobody is woken. A named persona is still reached,
// because being asked does not go through routing at all.
func TestRoutingFailureWakesNobodyButTheNamed(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return "モデルが壊れた返事"
		}
		return `{"action":"speak","text":"はい"}`
	}, "routine", "gourmet")

	runner.react(post(t, s, "何か").ID)
	if stub.promptFor("plana") != "" || stub.promptFor("tabe") != "" {
		t.Fatal("a failed routing woke personas nobody named")
	}

	runner.react(post(t, s, "@tabe 何か").ID)
	if stub.promptFor("tabe") == "" {
		t.Fatal("a named persona was lost because the routing failed")
	}
	if stub.promptFor("plana") != "" {
		t.Fatal("a failed routing woke a persona nobody named")
	}
}

// A persona reads and writes its own notes. The app hands over the names and
// nothing else - which is also why the notes of a long-lived persona do not
// have to be paid for on every wake.
func TestAPersonaReadsAndWritesItsOwnNotes(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["routine"]}`
		case strings.Contains(user, "notes_read の結果"):
			return `{"action":"tool","tool":"notes_write","args":{"path":"threads/old.md","text":"前から追いかけていること\n* 続き"}}`
		default:
			return `{"action":"tool","tool":"notes_read","args":{"paths":["threads/old.md"]}}`
		}
	}, "routine")

	if err := s.WriteNote("routine", "threads/old.md", "前から追いかけていること"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("routine", "index.md", "最初の目次"); err != nil {
		t.Fatal(err)
	}
	_ = post(t, s, "関係のない古い投稿")
	entry := post(t, s, "新しい投稿")
	runner.react(entry.ID)

	// The first prompt has the names, not the contents: reading is the
	// persona's own call, and a memory it does not need costs nothing.
	prompt := stub.promptFor("plana")
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
	for _, note := range s.Memory("routine").Notes {
		notes[note.Path] = note.Text
	}
	if !strings.Contains(notes["threads/old.md"], "続き") {
		t.Fatalf("the note it wrote = %q", notes["threads/old.md"])
	}
	if notes["index.md"] != "最初の目次" {
		t.Fatalf("an untouched note changed: %q", notes["index.md"])
	}
}

// Tools are the declared ones and nothing else. A workflow reaching for another
// is sent back before anything runs, and the call is refused again at run time
// whatever the review said.
func TestAToolThatWasNotDeclaredIsRefused(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"経路を見る","steps":[
			{"type":"tool","path":"","text":"","tool":"route","args":"{\"to\":\"渋谷\"}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"着くよ","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")

	entry := post(t, s, "どこか")
	runner.react(entry.ID)

	if !stub.seen("route はあなたが使える道具ではありません") {
		t.Fatal("a tool outside the declaration was allowed through")
	}
	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("a rejected workflow ran: %+v", said)
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

	prompt := stub.promptFor("tabe")
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

func TestFollowUpSharesTheWholeThreadAndIdentifiesTheTarget(t *testing.T) {
	for _, replyToQuestion := range []bool{false, true} {
		name := "add a comment"
		if replyToQuestion {
			name = "reply to a persona"
		}
		t.Run(name, func(t *testing.T) {
			runner, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return `{"deliver":["move","gourmet"]}`
				}
				if strings.Contains(user, "このターンは発言できません") {
					return `{"action":"silent"}`
				}
				return `{"action":"speak","text":"追加条件を踏まえた回答"}`
			}, "move", "gourmet")
			at := time.Now().Add(-time.Hour).Truncate(time.Minute)
			thread := []timeline.Entry{
				{ID: "shibuya", Kind: timeline.KindPost, Speaker: timeline.User, Text: "渋谷にいきたい", At: at},
				{ID: "question", Kind: timeline.KindRemark, Speaker: "move", Root: "shibuya", ReplyTo: "shibuya", Text: "いつ出発したいですか？", At: at.Add(time.Minute)},
				{ID: "earlier-comment", Kind: timeline.KindReply, Speaker: timeline.User, Root: "shibuya", ReplyTo: "shibuya", Text: "出発地は新宿です。", At: at.Add(2 * time.Minute)},
				{ID: "other-persona", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "shibuya", ReplyTo: "earlier-comment", Text: "着いたら食事もできます。", At: at.Add(3 * time.Minute)},
				{ID: "follow-up", Kind: timeline.KindReply, Speaker: timeline.User, Root: "shibuya", ReplyTo: "shibuya", Text: "1時間後。", At: at.Add(4 * time.Minute)},
				// A queued reaction must keep its target even if another remark has arrived.
				{ID: "later-remark", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "shibuya", ReplyTo: "follow-up", Text: "食事の候補も調べます。", At: at.Add(5 * time.Minute)},
			}
			trigger := &thread[4]
			if replyToQuestion {
				trigger.ReplyTo = "question"
			}
			for _, entry := range thread {
				if err := s.Append(entry); err != nil {
					t.Fatal(err)
				}
			}
			post(t, s, "別スレッドの秘密の予定")
			if err := s.Append(timeline.Entry{ID: "deleted", Kind: timeline.KindReply, Root: "shibuya", Text: "削除済みの条件", Deleted: true, At: at}); err != nil {
				t.Fatal(err)
			}

			runner.react(trigger.ID)

			stub.mu.Lock()
			prompts := append([]string(nil), stub.prompts...)
			stub.mu.Unlock()
			if len(prompts) != 4 {
				t.Fatalf("got %d calls, want routing, two personas, and a listening turn", len(prompts))
			}
			for i, prompt := range prompts {
				_, context, ok := strings.Cut(prompt, "スレッド全体（古い順）:\n")
				if !ok {
					t.Fatalf("call %d did not receive the thread", i)
				}
				history, target, ok := strings.Cut(context, "今回の対象発言（この発言をスレッド全体の文脈で解釈）:\n")
				if !ok || !strings.HasPrefix(target, "[follow-up] ") || !strings.Contains(target, "1時間後。") || !strings.Contains(target, trigger.At.UTC().Format("2006-01-02 15:04")) {
					t.Fatalf("call %d lost the target or its timestamp: %s", i, target)
				}
				previous := -1
				for _, entry := range thread {
					index := strings.Index(history, entry.Text)
					if index <= previous {
						t.Fatalf("call %d lost or reordered %q: %s", i, entry.Text, history)
					}
					previous = index
				}
				if !strings.Contains(target, trigger.ReplyTo+" への返事") {
					t.Fatalf("call %d lost which entry the user answered", i)
				}
				for _, excluded := range []string{"別スレッドの秘密の予定", "削除済みの条件"} {
					if strings.Contains(prompt, excluded) {
						t.Fatalf("call %d leaked %q", i, excluded)
					}
				}
			}
			var answers int
			for _, entry := range remarks(t, s) {
				if entry.Text == "追加条件を踏まえた回答" {
					answers++
					if entry.Root != "shibuya" || entry.ReplyTo != trigger.ID {
						t.Fatalf("answer is not attached to the follow-up: %+v", entry)
					}
				}
			}
			if answers != 2 {
				t.Fatalf("got %d answers, want one from each routed persona", answers)
			}
		})
	}
}

func TestNoteConsolidationCarriesReadSources(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "routine")
	if err := s.WriteNote("routine", "old.md", "Old knowledge.", store.NoteSource{EntryID: "old-post", Label: "Old post"}); err != nil {
		t.Fatal(err)
	}
	sources := []store.NoteSource{{EntryID: "current-post", Label: "Current post"}}
	runner.notesTool("routine", "notes_read", map[string]any{"path": "old.md"}, &sources)
	result, _ := runner.notesTool("routine", "notes_write", map[string]any{"path": "combined.md", "text": "Combined knowledge."}, &sources)
	if !strings.Contains(result, "書きました") {
		t.Fatal(result)
	}
	runner.notesTool("routine", "notes_forget", map[string]any{"path": "old.md"}, &sources)
	note, ok := s.Note("routine", "combined.md")
	if !ok || len(store.NoteSources(note.Text)) != 2 {
		t.Fatalf("consolidation lost sources: %+v", note)
	}
}

// A model that escaped its own line breaks leaves a note that cannot be read as
// OKF and renders as one long line. Repairing it is the only chance: the
// persona cannot see the difference from where it sits.
func TestEscapedLineBreaksInANoteAreRepaired(t *testing.T) {
	escaped := `---\ntype: Errand\ntitle: 夜ご飯の買い出し\n---\n後で行く予定。`
	want := "---\ntype: Errand\ntitle: 夜ご飯の買い出し\n---\n後で行く予定。"
	if got := repairNewlines(escaped); got != want {
		t.Fatalf("repairNewlines = %q", got)
	}
	// A note that already has real line breaks keeps every character it wrote,
	// including a literal backslash-n inside a code fence.
	kept := "# 手順\n\n```\nprintf \"a\\nb\"\n```\n"
	if got := repairNewlines(kept); got != kept {
		t.Fatalf("a readable note was rewritten: %q", got)
	}
	if got := repairNewlines("一行だけ"); got != "一行だけ" {
		t.Fatalf("plain text changed: %q", got)
	}
	// The blank line the app leaves before its own sources block is not a line
	// break the persona wrote, and must not make the damage above it look fine.
	trailing := escaped + "\n\n"
	if got := repairNewlines(trailing); got != want {
		t.Fatalf("a note followed by the app's blank line was left broken: %q", got)
	}
}

// The clock a persona is told is the user's. The container runs in UTC, and a
// remark or a note filed from that clock lands on the wrong day.
func TestThePromptCarriesTheWorkspaceZone(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "Asia/Tokyo"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	entry := post(t, s, "後で夜ご飯の買い出しに行く")
	runner.react(entry.ID)

	prompt := stub.promptFor("tabe")
	if !strings.Contains(prompt, "JST") {
		t.Fatalf("the persona was not told the time in the user's zone:\n%s", prompt)
	}
	if strings.Contains(prompt, "UTC") {
		t.Fatalf("the container's own clock reached the persona:\n%s", prompt)
	}
}

// A schedule and the note it rests on come as one step. The failure this is
// shaped against is having one without the other - plana wrote "予定: 22:30"
// into a note, and nothing was set to wake at 22:30.
func TestAPostCanPlaceASchedule(t *testing.T) {
	at := time.Now().Add(90 * time.Minute).Format(time.RFC3339)
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"予定とメモ","steps":[
			{"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\n---\n\n22:30から見る予定","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ドラマを見たか","id":""},
			{"type":"speak","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")

	runner.react(post(t, s, "今日の22時半からドラマを見る").ID)

	placed, err := s.Schedules("routine")
	if err != nil || len(placed) != 1 {
		t.Fatalf("placed = %+v (%v)", placed, err)
	}
	if placed[0].Note != "ドラマを見たか" || placed[0].NotePath != "errands/drama.md" {
		t.Fatalf("schedule = %+v", placed[0])
	}
	if _, ok := s.Note("routine", "errands/drama.md"); !ok {
		t.Fatal("the schedule was placed without its note")
	}
}

// A workflow stops at the step that fails. A wake that could not be placed
// leaves no note behind, and the remark after it never goes out.
func TestAWorkflowStopsAtTheStepThatFails(t *testing.T) {
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"過去に置こうとする","steps":[
			{"type":"write_schedule","path":"errands/late.md","text":"---\ntype: Errand\n---\n\n遅い","tool":"","args":"{}","at":"` + past + `","daily":false,"note":"遅い","id":""},
			{"type":"speak","path":"","text":"あとで聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")

	runner.react(post(t, s, "さっきの用").ID)

	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("a remark went out after a failed step: %+v", said)
	}
	if _, ok := s.Note("routine", "errands/late.md"); ok {
		t.Fatal("a note was written for a schedule that was never placed")
	}
}

// When the note cannot be written, the schedule placed a moment before is
// taken back: neither is left without the other.
func TestAScheduleWhoseNoteFailsIsTakenBack(t *testing.T) {
	at := time.Now().Add(time.Hour).Format(time.RFC3339)
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"書けないメモ","steps":[
			{"type":"write_schedule","path":"../outside.md","text":"中身","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"外","id":""},
			{"type":"speak","path":"","text":"置いたよ","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")

	runner.react(post(t, s, "何か").ID)

	if placed, err := s.Schedules("routine"); err != nil || len(placed) != 0 {
		t.Fatalf("the schedule survived its note: %+v (%v)", placed, err)
	}
	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("a remark went out after a failed step: %+v", said)
	}
}

// The review is what stands between a persona and a promise it did not arrange.
// A workflow it rejects never runs - not the note, not the remark - and the
// persona's second draft is the one that does.
func TestAWorkflowTheReviewRejectsNeverRuns(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	drafts := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		drafts++
		if drafts == 1 {
			return `{"summary":"覚えて約束する","steps":[
				{"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\n---\n\n2026-09-16 22:21 済","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ドラマを見たか","id":""},
				{"type":"speak","path":"","text":"22:30ごろに聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		}
		if !strings.Contains(user, "起きていないことを") {
			t.Errorf("the reason for sending it back was not passed on:\n%s", user)
		}
		return `{"summary":"覚えて、その時刻に起きる","steps":[
			{"type":"write_schedule","path":"errands/drama.md","text":"---\ntype: Errand\n---\n\n22:30からドラマを見る予定","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ドラマを見たか","id":""},
			{"type":"speak","path":"","text":"見終わったころに聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")
	stub.review = func(user string) string {
		// Only the invented line: plana's own instructions talk about 済んだ記録.
		if strings.Contains(user, "22:21 済") {
			return `{"verdict":"fail","summary":"起きていないことを書いている","issues":[{"severity":"high","description":"起きていないことを事実として書いている: 済"}]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}

	entry := post(t, s, "今日の22時半からドラマを見る")
	runner.react(entry.ID)

	if drafts != 2 || len(stub.reviews) != 2 {
		t.Fatalf("drafts = %d, reviews = %d", drafts, len(stub.reviews))
	}
	note, ok := s.Note("routine", "errands/drama.md")
	if !ok || strings.Contains(note.Text, "22:21 済") {
		t.Fatalf("the rejected note was written: %q", note.Text)
	}
	said := remarks(t, s)
	if len(said) != 1 || said[0].Text != "見終わったころに聞くね" {
		t.Fatalf("remarks = %+v", said)
	}
	placed, err := s.Schedules("routine")
	if err != nil || len(placed) != 1 {
		t.Fatalf("placed = %+v (%v)", placed, err)
	}
	if placed[0].NotePath != "errands/drama.md" {
		t.Fatalf("the schedule does not know its note: %+v", placed[0])
	}
}

// Keeping a note and speaking stays in the persona's own corner, so it costs no
// review; a schedule reaches further and is still checked.
func TestOnlyWorkflowsThatReachFurtherThanNotesAndRemarksAreReviewed(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		steps   string
		reviews int
	}{
		{`{"type":"write_note","path":"errands/bath.md","text":"---\ntype: Errand\n---\n\nお風呂","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"いってらっしゃい","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 0},
		{`{"type":"write_schedule","path":"errands/bath.md","text":"---\ntype: Errand\n---\n\nお風呂","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"入ったか","id":""}`, 1},
	} {
		runner, s, stub := setup(t, func(system, user string) string {
			if strings.Contains(system, "振り分け") {
				return `{"deliver":["routine"]}`
			}
			return `{"summary":"覚える","steps":[` + tc.steps + `,{"type":"speak","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		}, "routine")
		runner.react(post(t, s, "今からお風呂入ります").ID)
		if len(stub.reviews) != tc.reviews {
			t.Fatalf("reviews = %d, want %d for %s", len(stub.reviews), tc.reviews, tc.steps)
		}
	}
}

// Staying quiet has nothing to check, so it costs no review.
func TestAnEmptyWorkflowIsNotReviewed(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"何もしない","steps":[{"type":"speak","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
	}, "routine")

	runner.react(post(t, s, "晴れ").ID)

	if len(stub.reviews) != 0 {
		t.Fatalf("an empty workflow was reviewed %d times", len(stub.reviews))
	}
}

// The router names a persona the way a person would. Both spellings are the
// same persona, and neither is a reason to fall back to everyone.
func TestRoutingAcceptsTheNameAndIgnoresOneItMadeUp(t *testing.T) {
	for _, deliver := range []string{`["plana"]`, `["plana","nobody-at-all"]`} {
		t.Run(deliver, func(t *testing.T) {
			runner, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return `{"deliver":` + deliver + `}`
				}
				return `{"action":"silent"}`
			}, "routine", "gourmet")

			entry := post(t, s, "今日の22時半からドラマを見る")
			runner.react(entry.ID)

			if stub.promptFor("plana") == "" {
				t.Fatal("the persona it named never saw the post")
			}
			if stub.promptFor("tabe") != "" {
				t.Fatal("a usable answer still woke everyone")
			}
		})
	}
}

// The spec a persona is given follows what it can do. A persona without
// schedule is not taught write_schedule, and a persona with a search tool is
// shown a call to that tool it can copy.
func TestTheWorkflowSpecFollowsThePersonasTools(t *testing.T) {
	registry := tools.New(tools.Config{}).WithSchedules(nil)
	plana := workflowSpec(registry.For([]string{"schedule"}))
	tabe := workflowSpec(registry.For([]string{"web_search", "place_search"}))

	for _, want := range []string{"### write_schedule", "### cancel_schedule", "ドラマを見たか"} {
		if !strings.Contains(plana, want) {
			t.Errorf("a persona with schedule was not taught %q", want)
		}
	}
	for _, unwanted := range []string{"### write_schedule", "### cancel_schedule"} {
		if strings.Contains(tabe, unwanted) {
			t.Errorf("a persona without schedule was taught %q", unwanted)
		}
	}
	for _, want := range []string{"#### web_search", `\"query\"`, "#### place_search", "### 調べてから言う"} {
		if !strings.Contains(tabe, want) {
			t.Errorf("a persona with search tools was not shown %q", want)
		}
	}
	if strings.Contains(plana, "#### web_search") {
		t.Error("a persona was shown a tool it does not hold")
	}
	// Every step is taught with a sample, and the mistakes that were made.
	for _, want := range []string{"### write_note", "### forget_note", "### speak", "❌", "```json"} {
		if !strings.Contains(tabe, want) {
			t.Errorf("the spec lost %q", want)
		}
	}
}

// The index is handed over whole at the start of every turn.
func TestTheIndexIsHandedOverFirst(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		return `{"action":"silent"}`
	}, "gourmet")
	if err := s.WriteNote("gourmet", "index.md", "# 傾向\n\n* [豚骨に寄る](/tastes/tonkotsu.md)"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "tastes/tonkotsu.md", "豚骨の中身は渡さない"); err != nil {
		t.Fatal(err)
	}

	runner.react(post(t, s, "ラーメン").ID)

	prompt := stub.promptFor("tabe")
	if !strings.Contains(prompt, "[豚骨に寄る](/tastes/tonkotsu.md)") {
		t.Fatalf("the index was not handed over:\n%s", prompt)
	}
	if strings.Contains(prompt, "豚骨の中身は渡さない") {
		t.Fatal("a note other than the index was handed over unasked")
	}
}

// A persona may look at up to ten things before deciding, searches included.
func TestAPersonaMayReadTenTimes(t *testing.T) {
	reads := 0
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		if strings.Contains(user, "これ以上は読めません") {
			return `{"action":"silent"}`
		}
		reads++
		return `{"action":"tool","tool":"notes_list","args":{}}`
	}, "gourmet")

	runner.react(post(t, s, "ラーメン").ID)

	// The eleventh request is not offered read, so the fake answers it with an
	// empty workflow and the turn ends.
	if reads != maxReads+1 || maxReads != 10 {
		t.Fatalf("reads = %d (maxReads %d)", reads, maxReads)
	}
}

// A persona that writes the same note twice in one workflow is sent back: the
// second write silently replaces the first, and in a live run it carried a typo.
func TestTheSameNoteWrittenTwiceIsSentBack(t *testing.T) {
	registry := tools.New(tools.Config{}).WithSchedules(nil)
	problems := checkWorkflow(Workflow{Steps: []Step{
		{Type: "write_note", Path: "errands/drama.md", Text: "a"},
		{Type: "write_schedule", Path: "errands/drama.md", Text: "b", At: "2026-09-18T22:00:00+09:00", Note: "見たか"},
		{Type: "speak", Text: ""},
	}}, turnRules{set: registry.For([]string{"schedule"}).As("routine")})
	if len(problems) != 1 || !strings.Contains(problems[0], "すでに書いています") {
		t.Fatalf("problems = %v", problems)
	}
}

// A search that keeps failing the same way is called out on the second try,
// and a note read written as a tool call is simply done.
func TestReadingForgivesTheWrongFieldAndCallsOutARepeatedFailure(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "index.md", "目次"); err != nil {
		t.Fatal(err)
	}
	set := tools.New(tools.Config{}).For([]string{"place_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find("gourmet")
	failures := map[string]string{}

	text, _, _ := runner.readAll(context.Background(), skill, []ReadStep{{Type: "tool", Tool: "notes_read", Paths: []string{"index.md"}}}, set, timeline.Entry{}, &[]store.NoteSource{}, failures, newLookups(maxReads))
	if !strings.Contains(text, "目次") {
		t.Fatalf("a note read in the wrong field was refused: %s", text)
	}

	broken := []ReadStep{{Type: "tool", Tool: "place_search", Args: `{"query":"神谷町"}`}}
	first, _, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, &[]store.NoteSource{}, failures, newLookups(maxReads))
	if strings.Contains(first, "前にも同じ理由で失敗") {
		t.Fatalf("a first failure was called a repeat: %s", first)
	}
	second, _, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, &[]store.NoteSource{}, failures, newLookups(maxReads))
	if !strings.Contains(second, "前にも同じ理由で失敗") {
		t.Fatalf("a repeated failure was not called out: %s", second)
	}
}

// A note read written as a tool call is the same request in the wrong field,
// and it is read rather than refused.
func TestANoteReadWrittenAsAToolIsStillRead(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "tastes/tonkotsu.md", "豚骨に寄る"); err != nil {
		t.Fatal(err)
	}
	set := runner.registry(s.Settings()).For([]string{"web_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find("gourmet")
	var sources []store.NoteSource
	text, _, _ := runner.readAll(context.Background(), skill,
		[]ReadStep{{Type: "tool", Tool: "notes_read", Paths: []string{"tastes/tonkotsu.md"}, Args: "{}"}},
		set, timeline.Entry{}, &sources, map[string]string{}, newLookups(maxReads))
	if !strings.Contains(text, "豚骨に寄る") {
		t.Fatalf("the note was not read: %s", text)
	}
}

// The same call failing the same way twice is said outright, so a turn is not
// spent on one broken search while another tool sits unused.
func TestARepeatedFailingCallIsCalledOut(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	set := runner.registry(s.Settings()).For([]string{"place_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find("gourmet")
	failures := map[string]string{}
	search := []ReadStep{{Type: "tool", Tool: "place_search", Paths: []string{}, Args: `{"query":"神谷町"}`}}
	var sources []store.NoteSource

	first, _, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, &sources, failures, newLookups(maxReads))
	if !strings.Contains(first, "道具が使えませんでした") || strings.Contains(first, "前にも同じ理由で") {
		t.Fatalf("first failure: %s", first)
	}
	second, _, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, &sources, failures, newLookups(maxReads))
	if !strings.Contains(second, "前にも同じ理由で失敗しています") {
		t.Fatalf("a repeated failure was not called out: %s", second)
	}
}

// A remark written into the tool field is the same remark in the wrong shape,
// and it is put back rather than sent back.
func TestAStepWrittenAsAToolIsPutBack(t *testing.T) {
	workflow := normaliseWorkflow(Workflow{Steps: []Step{
		{Type: "tool", Tool: "speak", Args: `{"text":"また豚骨だ"}`},
	}})
	if step := workflow.Steps[0]; step.Type != "speak" || step.Text != "また豚骨だ" || step.Tool != "" {
		t.Fatalf("step = %+v", step)
	}
	if problems := checkWorkflow(workflow, turnRules{set: tools.New(tools.Config{}).For(nil)}); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

// Every read ends with how many are left and which other tools there are.
func TestAReadSaysWhatIsLeftAndWhatElseThereIs(t *testing.T) {
	set := tools.New(tools.Config{}).For([]string{"place_search", "web_search", "schedule"})
	footer := readFooter(3, set)
	for _, want := range []string{"残り 3 回", "place_search", "web_search"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer lost %q: %s", want, footer)
		}
	}
	if strings.Contains(footer, "schedule") {
		t.Fatalf("schedule is not something to read with: %s", footer)
	}
	if !strings.Contains(readFooter(0, set), "submit_workflow") {
		t.Fatal("the last read did not say to hand in the workflow")
	}
}

// The low tier works without a workflow: its notes and a remark, called
// directly, and nothing to review.
func TestTheLowTierTalksToItsNotesDirectly(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["diary"]}`
		case strings.Contains(user, "write_note の結果"):
			return `{"action":"speak","text":"覚えた"}`
		default:
			return `{"action":"tool","tool":"notes_write","args":{"path":"days/today.md","text":"---\\ntype: Day\\n---\\n\\n* 雨"}}`
		}
	}, "diary")
	if _, err := runner.catalogue.Install([]byte(`{"id":"diary","name":"nikki","domain":"日々のこと","instructions":"覚える","model":"low","tools":["web_search"]}`), func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}

	runner.react(post(t, s, "今日は雨").ID)

	if said := remarks(t, s); len(said) != 1 || said[0].Text != "覚えた" {
		t.Fatalf("remarks = %+v", said)
	}
	if _, ok := s.Note("diary", "days/today.md"); !ok {
		t.Fatal("the note was not written")
	}
	if len(stub.reviews) != 0 {
		t.Fatal("the low tier was reviewed")
	}
	prompt := stub.promptFor("nikki")
	if strings.Contains(prompt, "\nsubmit_workflow — ") || strings.Contains(prompt, "\nread — ") {
		t.Fatalf("the low tier was offered the workflow:\n%s", prompt)
	}
	for _, want := range []string{"write_note — ", "speak — ", "silent — ", "web_search — "} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the low tier was not offered %q", want)
		}
	}
}

// Web search is reading. The low tier may hold it; anything that does more
// than look needs a workflow, and so a tier above low.
func TestTheLowTierHoldsOnlyReadingTools(t *testing.T) {
	catalogue := NewCatalogue(t.TempDir())
	known := func(string) bool { return true }
	if _, err := catalogue.Install([]byte(`{"id":"walker","name":"w","domain":"移動","model":"low","tools":["route"]}`), known); err == nil {
		t.Fatal("a low persona was saved holding route")
	}
	if _, err := catalogue.Install([]byte(`{"id":"reader","name":"r","domain":"調べもの","model":"low","tools":["web_search"]}`), known); err != nil {
		t.Fatalf("a low persona with web search was refused: %v", err)
	}
	// One saved before the rule keeps its tools and runs a tier up.
	dir := filepath.Join(catalogue.root, "old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.json"), []byte(`{"id":"old","name":"o","domain":"移動","model":"low","tools":["route"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if old, ok := catalogue.Find("old"); !ok || old.Model != TierMiddle || len(old.Tools) != 1 {
		t.Fatalf("old = %+v", old)
	}
}

// Web search is used before a workflow, like reading a note, and never in one.
func TestWebSearchIsNotAWorkflowStep(t *testing.T) {
	set := tools.New(tools.Config{}).For([]string{"web_search"})
	problems := checkWorkflow(Workflow{Steps: []Step{{Type: "tool", Tool: "web_search", Args: `{"query":"x"}`}, {Type: "speak"}}}, turnRules{set: set})
	if len(problems) != 1 || !strings.Contains(problems[0], "read で使ってください") {
		t.Fatalf("problems = %v", problems)
	}
}

// A workflow sent back for its shape does not use up the review's second look.
// A live run spent its first draft on a misplaced web_search, and the review
// then rejected the second with no chance left to act on what it found.
func TestAMalformedDraftDoesNotUseUpTheReview(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	drafts := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		drafts++
		switch drafts {
		case 1:
			return `{"summary":"検索を workflow に入れる","steps":[{"type":"tool","path":"","text":"","tool":"schedule","args":"{\"query\":\"x\"}","at":"","daily":false,"note":"","id":""}]}`
		case 2:
			return `{"summary":"作り話","steps":[{"type":"write_schedule","path":"errands/lunch.md","text":"---\\ntype: Errand\\n---\\n\\nランチ","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ランチしたか","id":""},{"type":"speak","path":"","text":"1時間後に着くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		default:
			return `{"summary":"直した","steps":[{"type":"write_schedule","path":"errands/lunch.md","text":"---\\ntype: Errand\\n---\\n\\nランチ","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ランチしたか","id":""},{"type":"speak","path":"","text":"このへんなら蕎麦かな","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		}
	}, "routine")
	stub.review = func(user string) string {
		if strings.Contains(user, "1時間後") {
			return `{"verdict":"fail","summary":"作り話","issues":[{"severity":"high","description":"投稿に無い到着時刻を書いている"}]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}

	runner.react(post(t, s, "このへんでランチするならどこがいいかな").ID)

	if said := remarks(t, s); len(said) != 1 || said[0].Text != "このへんなら蕎麦かな" {
		t.Fatalf("drafts = %d, remarks = %+v", drafts, said)
	}
	// All three were drafts: the first sent back for its shape, the second by
	// the review. Nothing was lost to a reply that called nothing.
	if drafts != 3 || len(stub.reviews) != 2 {
		t.Fatalf("drafts = %d, reviews = %d", drafts, len(stub.reviews))
	}
}

// submit_workflow is offered in the shape of what a persona holds. Lookups are
// never a workflow step, so a persona whose tools only look things up has no
// tool step at all; only a persona that can schedule sees write_schedule.
func TestTheWorkflowSchemaFollowsThePersonasTools(t *testing.T) {
	registry := tools.New(tools.Config{}).WithSchedules(nil)
	shapeOfSchema := func(declared []string) ([]any, map[string]any) {
		tool := workflowToolFor(registry.For(declared))
		steps := tool.Parameters["properties"].(map[string]any)["steps"].(map[string]any)
		items := steps["items"].(map[string]any)
		properties := items["properties"].(map[string]any)
		return properties["type"].(map[string]any)["enum"].([]any), properties
	}
	has := func(values []any, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}

	types, fields := shapeOfSchema([]string{"web_search", "place_search", "route"})
	if has(types, "tool") || has(types, "write_schedule") || fields["at"] != nil || fields["tool"] != nil {
		t.Fatalf("a persona that only looks things up was offered %v / %v", types, fields)
	}
	if !has(types, "write_note") || !has(types, "speak") {
		t.Fatalf("types = %v", types)
	}

	types, fields = shapeOfSchema([]string{"schedule"})
	if !has(types, "write_schedule") || !has(types, "cancel_schedule") || fields["at"] == nil || has(types, "tool") {
		t.Fatalf("a scheduling persona was offered %v / %v", types, fields)
	}

	// The spec's samples carry exactly the fields the schema has.
	tabe := workflowSpec(registry.For([]string{"web_search", "place_search"}))
	if strings.Contains(tabe, `"at":`) || strings.Contains(tabe, "### tool\n") {
		t.Fatalf("the spec showed a lookup-only persona fields or steps it cannot send")
	}
	plana := workflowSpec(registry.For([]string{"schedule"}))
	if !strings.Contains(plana, `"at":"2026-09-18T22:00:00+09:00"`) {
		t.Fatal("the scheduling sample lost its time")
	}
}

// A remark that is the workflow's own summary is a plan said out loud, not
// something said to the user.
func TestSpeakingTheSummaryIsSentBack(t *testing.T) {
	set := tools.New(tools.Config{}).For([]string{"web_search"})
	problems := checkWorkflow(Workflow{Summary: "神谷町駅周辺のランチ情報をWeb検索で探す", Steps: []Step{
		{Type: "speak", Text: "神谷町駅周辺のランチ情報をWeb検索で探す"},
	}}, turnRules{set: set})
	if len(problems) != 1 || !strings.Contains(problems[0], "ユーザーに向けた発言") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEditedPostNotifiesPreviousRecipientsAndRoutesNewOnes(t *testing.T) {
	for _, routed := range []string{`{"deliver":["gourmet"]}`, `{"deliver":["move","gourmet"]}`, `{"deliver":[]}`} {
		t.Run(routed, func(t *testing.T) {
			runner, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return routed
				}
				return `{"action":"silent","notes":""}`
			}, "move", "gourmet")
			entry := post(t, s, "渋谷に行きたい")
			// Silent recipients also remain in the persistent delivery history.
			if err := s.RecordWake(entry.ID, "move"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.UpdateText(entry.ID, "今夜は自宅でワインを飲みたい"); err != nil {
				t.Fatal(err)
			}
			runner.EntryEdited(entry.ID)
			runner.Stop()
			old := stub.promptFor("michi")
			if !strings.Contains(old, editedInstructions) || !strings.Contains(old, "今夜は自宅でワインを飲みたい") {
				t.Fatalf("previous recipient did not receive the edit: %s", old)
			}
			fresh := stub.promptFor("tabe")
			if strings.Contains(routed, "gourmet") {
				if fresh == "" || strings.Contains(fresh, editedInstructions) {
					t.Fatalf("new recipient should receive an ordinary delivery: %s", fresh)
				}
				ids, err := s.Woke(entry.ID)
				if err != nil || !contains(ids, "gourmet") {
					t.Fatalf("new recipient not remembered: %v, %v", ids, err)
				}
			} else if fresh != "" {
				t.Fatal("unrelated new recipient was called")
			}
			count := 0
			for _, prompt := range stub.prompts {
				if strings.Contains(prompt, "あなたは「michi」") {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("previous recipient called %d times, want once", count)
			}
		})
	}
}

// What the router decided is kept for a person to read: who it chose, and why
// it passed over the rest. A persona reached by name was not the router's
// choice, and anyone who got the post is not among the passed-over. A router
// with no answer leaves no record at all.
func TestRoutingDecisionIsKeptForThePost(t *testing.T) {
	for _, tc := range []struct {
		name, routed, text string
		want               *store.Routing
	}{
		{
			name: "named persona is not the router's choice", text: "@tabe 今日の昼どうしよう",
			routed: `{"deliver":["gourmet","move"],"excluded_reasons":{"book":"本の話ではない"}}`,
			want:   &store.Routing{Delivered: []string{"move"}, Excluded: map[string]string{"book": "本の話ではない"}},
		},
		{
			name: "named persona is not passed over either", text: "@tabe 今日の昼どうしよう",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{}, Excluded: map[string]string{"move": "b", "book": "c"}},
		},
		{
			name: "nobody is an answer", text: "今日は眠い",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{}, Excluded: map[string]string{"gourmet": "a", "move": "b", "book": "c"}},
		},
		{name: "a broken router is no answer", text: "今日は眠い", routed: `モデルが壊れた返事`, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, s, _ := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return tc.routed
				}
				return `{"action":"silent","notes":""}`
			}, "gourmet", "move", "book")
			entry := post(t, s, tc.text)
			runner.React(entry.ID, nil)
			runner.Stop()
			got, err := s.Routing(entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != nil {
				tc.want.EntryText = &entry.Text
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("kept %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFailedRoutingDropsPreviousDecision(t *testing.T) {
	runner, workspace, _ := setup(t, func(system, user string) string { return "broken answer" }, "book")
	entry := post(t, workspace, "edited post")
	if err := workspace.SaveRouting(entry.ID, store.Routing{Delivered: []string{"book"}}); err != nil {
		t.Fatal(err)
	}
	runner.React(entry.ID, nil)
	runner.Stop()
	if record, err := workspace.Routing(entry.ID); err != nil || record != nil {
		t.Fatalf("stale decision: %+v (%v)", record, err)
	}
}

// Every workflow ends with a speak, and staying quiet is a speak with nothing
// in it: the persona decides out loud. A live run's summary promised a remark
// that the steps did not carry, and the note was written in silence.
func TestEveryWorkflowDecidesWhetherToSpeak(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	silent := Workflow{Summary: "メモを書いて黙る", Steps: []Step{{Type: "write_note", Path: "a.md", Text: "x"}, {Type: "speak", Text: ""}}}
	if problems := checkWorkflow(silent, turnRules{set: set}); len(problems) != 0 {
		t.Fatalf("explicit silence was refused: %v", problems)
	}
	missing := Workflow{Summary: "メモを書いて発言する", Steps: []Step{{Type: "write_note", Path: "a.md", Text: "x"}}}
	if problems := checkWorkflow(missing, turnRules{set: set}); len(problems) != 1 || !strings.Contains(problems[0], "speak がありません") {
		t.Fatalf("a workflow without a speak was accepted: %v", problems)
	}
	if problems := checkWorkflow(silent, turnRules{set: set, named: true}); len(problems) != 1 || !strings.Contains(problems[0], "空にはできません") {
		t.Fatalf("a named persona was allowed to stay quiet: %v", problems)
	}
	spoken := Workflow{Steps: []Step{{Type: "speak", Text: "また豚骨だ"}}}
	if problems := checkWorkflow(spoken, turnRules{set: set, listening: true}); len(problems) != 1 || !strings.Contains(problems[0], "空にしてください") {
		t.Fatalf("a listening persona was allowed to speak: %v", problems)
	}
	if problems := checkWorkflow(silent, turnRules{set: set, listening: true}); len(problems) != 0 {
		t.Fatalf("a listening persona's silence was refused: %v", problems)
	}
}
