package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/search"
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
	// models are the model each prompt was sent to, by the same index.
	models []string
	reply  func(system, user string) string
	// reviews are the app's own checks, kept apart from what the personas were
	// asked so a test counting persona calls is not counting those. review
	// scripts a verdict; unset, every workflow passes.
	reviews       []string
	reviewSystems []string
	reviewModels  []string
	review        func(user string) string
	// fail makes the endpoint answer 500 to a request whose system prompt it
	// matches, for a test that needs a call to fail.
	fail func(system string) bool
}

func (f *fake) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
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
		if f.fail != nil && f.fail(system) {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if names := toolNames(body.Tools); names["submit_review"] {
			f.mu.Lock()
			f.reviews = append(f.reviews, user)
			f.reviewSystems = append(f.reviewSystems, system)
			f.reviewModels = append(f.reviewModels, body.Model)
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
		f.models = append(f.models, body.Model)
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

	case offered["escalate"]:
		return call("escalate", json.RawMessage(content))
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
			// A remark on a post leaves a note behind, so a scripted remark
			// carries one, as a live persona now must.
			steps = append(steps, step(map[string]any{"type": "write_note", "path": "turns/said.md", "text": "type: Turn\n\n" + parsed.Text}))
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
				case "image":
					read = map[string]any{"type": "image", "paths": []any{}, "tool": "", "args": string(args)}
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

// modelFor returns the model the persona with this name was first asked on.
func (f *fake) modelFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, prompt := range f.prompts {
		if strings.HasPrefix(prompt, "あなたは「"+name+"」") {
			return f.models[i]
		}
	}
	return ""
}

// lastModelFor returns the model the persona with this name was last asked on.
func (f *fake) lastModelFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.prompts) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.prompts[i], "あなたは「"+name+"」") {
			return f.models[i]
		}
	}
	return ""
}

// lastPromptFor returns the last prompt sent to the persona with this name.
func (f *fake) lastPromptFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.prompts) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.prompts[i], "あなたは「"+name+"」") {
			return f.prompts[i]
		}
	}
	return ""
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

// checkInDay is the first day from day on which none of these personas skips
// its check-in. Which days are skipped depends on the workspace, and a test's
// workspace is a new temporary directory each run.
func checkInDay(s *store.Store, day time.Time, personas ...string) time.Time {
	for {
		skipped := false
		for _, persona := range personas {
			if store.CheckInSkipped(s.Root(), persona, day) {
				skipped = true
			}
		}
		if !skipped {
			return day
		}
		day = day.AddDate(0, 0, 1)
	}
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

// Everyone a post reaches answers at once, on the same model, and nobody gets
// a second turn to listen to the others.
func TestEveryoneAnswersTogetherOnSuper(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move","gourmet"]}`
		case strings.Contains(user, "このターンは発言できません"):
			t.Error("a listening turn was given")
			return `{"action":"silent"}`
		case strings.Contains(system, "あなたは「michi」"):
			return `{"action":"speak","text":"渋谷まで20分"}`
		default:
			return `{"action":"speak","text":"それなら店も見ておく"}`
		}
	}, "move", "gourmet")

	entry := post(t, s, "20時に渋谷に着きたい")
	runner.react(entry.ID)

	if said := remarks(t, s); len(said) != 2 {
		t.Fatalf("remarks = %+v", said)
	}
	if stub.modelFor("michi") != store.NemotronSuper || stub.modelFor("tabe") != store.NemotronSuper {
		t.Fatalf("models = %q, %q; want Super", stub.modelFor("michi"), stub.modelFor("tabe"))
	}
}

// A reply to a persona's answer is shown to Lightning, and only one asking for
// a better or deeper answer is answered on Ultra - by the persona it was said
// to, whether or not routing chose it. Anyone else it reaches stays on Super.
func TestAReplyAskingForMoreIsAnsweredOnUltra(t *testing.T) {
	for _, tc := range []struct {
		reply    string
		escalate bool
		want     string
		slot     string
	}{
		{"もっと詳しく", true, store.NemotronUltra, "ultra"},
		{"ありがとう", false, store.NemotronSuper, "super"},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			asked := ""
			replied := false
			runner, s, stub := setup(t, func(system, user string) string {
				switch {
				case strings.Contains(system, "明示的に求めているか"):
					asked = user
					return `{"escalate":` + strconv.FormatBool(tc.escalate) + `}`
				case strings.Contains(system, "振り分け") && !replied:
					return `{"deliver":["gourmet"]}`
				case strings.Contains(system, "振り分け"):
					// The reply is routed to someone else only.
					return `{"deliver":["move"]}`
				case strings.Contains(system, "あなたは「michi」"):
					return `{"action":"speak","text":"駅から5分"}`
				default:
					return `{"action":"speak","text":"豚骨の店がある"}`
				}
			}, "gourmet", "move")

			first := post(t, s, "ラーメン食べたい")
			runner.react(first.ID)
			said := remarks(t, s)
			if len(said) != 1 || said[0].Model != "super" {
				t.Fatalf("remarks = %+v", said)
			}
			replied = true
			reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User,
				Root: first.ID, ReplyTo: said[0].ID, Text: tc.reply, At: time.Now()}
			if err := s.Append(reply); err != nil {
				t.Fatal(err)
			}
			runner.react(reply.ID)

			if !strings.Contains(asked, "豚骨の店がある") || !strings.Contains(asked, tc.reply) {
				t.Fatalf("Lightning was not shown the answer and the reply:\n%s", asked)
			}
			if got := stub.modelFor("michi"); got != store.NemotronSuper {
				t.Fatalf("the other persona answered on %q", got)
			}
			if !tc.escalate {
				if len(remarks(t, s)) != 2 {
					t.Fatalf("the persona replied to answered without being routed: %+v", remarks(t, s))
				}
				return
			}
			if got := stub.lastModelFor("tabe"); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
			// Ultra is not told to keep it short; Super is.
			prompt := stub.lastPromptFor("tabe")
			if !strings.Contains(prompt, "前の回答より良い・深い回答を求めています") || !strings.Contains(prompt, "求められた深さに見合う長さで、Markdown で。") || strings.Contains(prompt, "発言するときは短く") {
				t.Fatalf("tabe was not given the deep prompt:\n%s", prompt)
			}
			if !strings.Contains(stub.promptFor("michi"), "発言するときは短く") {
				t.Fatal("the other persona lost the brief prompt")
			}
			models := map[string]string{}
			for _, remark := range remarks(t, s)[1:] {
				models[remark.Speaker] = remark.Model
			}
			if models["gourmet"] != tc.slot || models["move"] != "super" {
				t.Fatalf("remark models = %v", models)
			}
		})
	}
}

// replyTo appends the user's reply to an entry, in its thread.
func replyTo(t *testing.T, s *store.Store, parent timeline.Entry, text string) timeline.Entry {
	t.Helper()
	root := parent.Root
	if root == "" {
		root = parent.ID
	}
	reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User,
		Root: root, ReplyTo: parent.ID, Text: text, At: time.Now()}
	if err := s.Append(reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

// The persona replied to may already be a recipient - routed, or @mentioned.
// It still takes one turn, and that turn is on Ultra.
func TestAnUltraReplyRunsThePersonaOnce(t *testing.T) {
	for _, tc := range []struct{ name, reply, routed string }{
		{"routed", "もっと詳しく", `{"deliver":["gourmet"]}`},
		{"mentioned", "@tabe もっと詳しく", `{"deliver":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replied := false
			runner, s, stub := setup(t, func(system, user string) string {
				switch {
				case strings.Contains(system, "明示的に求めているか"):
					return `{"escalate":true}`
				case strings.Contains(system, "振り分け") && !replied:
					return `{"deliver":["gourmet"]}`
				case strings.Contains(system, "振り分け"):
					return tc.routed
				default:
					return `{"action":"speak","text":"豚骨の店がある"}`
				}
			}, "gourmet")
			first := post(t, s, "ラーメン食べたい")
			runner.react(first.ID)
			answer := remarks(t, s)[0]

			replied = true
			stub.mu.Lock()
			before := len(stub.prompts)
			stub.mu.Unlock()
			runner.react(replyTo(t, s, answer, tc.reply).ID)

			stub.mu.Lock()
			var models []string
			for i := before; i < len(stub.prompts); i++ {
				if strings.HasPrefix(stub.prompts[i], "あなたは「tabe」") {
					models = append(models, stub.models[i])
				}
			}
			stub.mu.Unlock()
			if len(models) == 0 {
				t.Fatal("tabe was not asked")
			}
			for _, model := range models {
				if model != store.NemotronUltra {
					t.Fatalf("tabe was asked on %v, want Ultra only", models)
				}
			}
			if said := remarks(t, s); len(said) != 2 || said[1].Model != "ultra" {
				t.Fatalf("remarks = %+v, want one more, on Ultra", said)
			}
		})
	}
}

// Lightning is asked only about a reply to an active persona's remark. A
// post, a reply to the user's own post and a reply to a persona no longer in
// the room are never shown to it.
func TestLightningIsAskedOnlyAboutAReplyToAnActivePersona(t *testing.T) {
	for _, tc := range []struct {
		name    string
		speaker string // who the replied-to entry is from; "" means no reply
	}{
		{"a post", ""},
		{"a reply to the user's own post", timeline.User},
		{"a reply to a persona not in the room", "move"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := 0
			runner, s, _ := setup(t, func(system, user string) string {
				switch {
				case strings.Contains(system, "明示的に求めているか"):
					asked++
					return `{"escalate":true}`
				case strings.Contains(system, "振り分け"):
					return `{"deliver":["gourmet"]}`
				default:
					return `{"action":"silent"}`
				}
			}, "gourmet")
			first := post(t, s, "もっと詳しく")
			trigger := first
			switch tc.speaker {
			case "":
			case timeline.User:
				trigger = replyTo(t, s, first, "もっと詳しく")
			default:
				remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: tc.speaker,
					Root: first.ID, ReplyTo: first.ID, Text: "答え", At: time.Now()}
				if err := s.Append(remark); err != nil {
					t.Fatal(err)
				}
				trigger = replyTo(t, s, remark, "もっと詳しく")
			}
			runner.react(trigger.ID)
			if asked != 0 {
				t.Fatalf("Lightning was asked %d times", asked)
			}
		})
	}
}

// A judgement that fails is a no: the reply is answered on Super by whoever
// it reached, and the persona replied to is not added.
func TestAFailedJudgementIsANo(t *testing.T) {
	replied := false
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け") && !replied:
			return `{"deliver":["gourmet"]}`
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move"]}`
		case strings.Contains(system, "あなたは「michi」"):
			return `{"action":"speak","text":"駅から5分"}`
		default:
			return `{"action":"speak","text":"豚骨の店がある"}`
		}
	}, "gourmet", "move")
	tried := 0
	stub.fail = func(system string) bool {
		if strings.Contains(system, "明示的に求めているか") {
			tried++
			return true
		}
		return false
	}
	first := post(t, s, "ラーメン食べたい")
	runner.react(first.ID)
	answer := remarks(t, s)[0]
	replied = true
	runner.react(replyTo(t, s, answer, "もっと詳しく").ID)

	if tried != 1 {
		t.Fatalf("Lightning was tried %d times, want once", tried)
	}
	said := remarks(t, s)
	if len(said) != 2 || said[1].Speaker != "move" || said[1].Model != "super" {
		t.Fatalf("remarks = %+v, want only michi, on Super", said)
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
	// Named, it answers as long as the question needs rather than briefly.
	if prompt := stub.promptFor("tabe"); !strings.Contains(prompt, "聞かれたことに答えるのに必要な長さで") || strings.Contains(prompt, "発言するときは短く") {
		t.Fatalf("the named persona was still told to keep it short:\n%s", prompt)
	}
}

// A direct question carries a stronger contract than an incidental routed
// reaction: the app reviews whether the final answer actually addresses what
// the user asked, and lets the persona replace an answer that does not.
func TestBeingNamedReviewsWhetherTheAnswerMeetsTheRequest(t *testing.T) {
	drafts := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":[]}`
		}
		drafts++
		if drafts == 1 {
			return `{"action":"speak","text":"Goがおすすめです。"}`
		}
		if !strings.Contains(user, "理由を聞かれている") {
			t.Errorf("the review issue was not returned to the persona:\n%s", user)
		}
		return `{"action":"speak","text":"並行処理を簡潔に書けるので、Goがおすすめです。"}`
	}, "routine")
	stub.review = func(user string) string {
		if !strings.Contains(user, "今回の対象発言") || !strings.Contains(user, "@plana どの言語がいい？理由も教えて") {
			t.Errorf("reviewer did not receive the mentioned request:\n%s", user)
		}
		if strings.Contains(user, "Goがおすすめです。") && !strings.Contains(user, "並行処理") {
			return `{"verdict":"fail","summary":"理由に答えていない","issues":[{"severity":"high","description":"理由を聞かれているが、結論しか返していない"}]}`
		}
		return `{"verdict":"pass","summary":"質問と理由に答えている","issues":[]}`
	}

	runner.react(post(t, s, "@plana どの言語がいい？理由も教えて").ID)

	if drafts != 2 || len(stub.reviews) != 2 {
		t.Fatalf("drafts = %d, reviews = %d, want two of each", drafts, len(stub.reviews))
	}
	if !strings.Contains(stub.reviewSystems[0], "@メンションで名指し") || !strings.Contains(stub.reviewSystems[0], "頼まれたことに答えていない") {
		t.Fatalf("named-answer criteria were not given to the reviewer:\n%s", stub.reviewSystems[0])
	}
	said := remarks(t, s)
	if len(said) != 1 || !strings.Contains(said[0].Text, "並行処理") {
		t.Fatalf("remarks = %+v, want the reviewed replacement", said)
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

	// The first prompt has the index and how many notes there are, not the
	// notes: reading is the persona's own call, and a memory it does not need
	// costs nothing - not even a listing of every name.
	prompt := stub.promptFor("plana")
	if !strings.Contains(prompt, "最初の目次") || !strings.Contains(prompt, "あなたのメモ: 2件") {
		t.Fatalf("the persona was not told what it has:\n%s", prompt)
	}
	if strings.Contains(prompt, "threads/old.md") {
		t.Fatalf("every note was listed unasked:\n%s", prompt)
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
				if strings.Contains(system, "明示的に求めているか") {
					return `{"escalate":false}`
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
			var prompts []string
			for _, prompt := range stub.prompts {
				// Whether a reply asks for Ultra is judged on the answer and
				// the reply alone.
				if !strings.Contains(prompt, "明示的に求めているか") {
					prompts = append(prompts, prompt)
				}
			}
			stub.mu.Unlock()
			if len(prompts) != 3 {
				t.Fatalf("got %d calls, want routing and two personas", len(prompts))
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

// A note that consolidates others keeps the trail to their posts, whether the
// old note is thrown away before or after the new one is written. A note read
// merely for context passes nothing on, and the index never carries a post.
func TestNoteConsolidationCarriesReadSources(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "routine")
	if err := s.WriteNote("routine", "old.md", "Old knowledge.", store.NoteSource{EntryID: "old-post", Label: "Old post"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("routine", "context.md", "Read for context.", store.NoteSource{EntryID: "ctx-post", Label: "Context post"}); err != nil {
		t.Fatal(err)
	}
	prov := newProvenance(store.NoteSource{EntryID: "current-post", Label: "Current post"})
	runner.notesTool("routine", "notes_read", map[string]any{"paths": []any{"old.md", "context.md"}}, prov)
	result, _ := runner.notesTool("routine", "notes_write", map[string]any{"path": "combined.md", "text": "Combined knowledge."}, prov)
	if !strings.Contains(result, "書きました") {
		t.Fatal(result)
	}
	runner.notesTool("routine", "notes_write", map[string]any{"path": "index.md", "text": "# Index\n* [combined](/combined.md)"}, prov)
	note, _ := s.Note("routine", "combined.md")
	if ids := sourceIDs(note.Text); !reflect.DeepEqual(ids, []string{"current-post"}) {
		t.Fatalf("before the forget, combined cites %v", ids)
	}
	runner.notesTool("routine", "notes_forget", map[string]any{"path": "old.md"}, prov)
	note, ok := s.Note("routine", "combined.md")
	if !ok {
		t.Fatal("combined.md is gone")
	}
	if ids := sourceIDs(note.Text); !reflect.DeepEqual(ids, []string{"current-post", "old-post"}) {
		t.Fatalf("consolidation lost sources: %v", ids)
	}
	if index, _ := s.Note("routine", "index.md"); len(store.NoteSources(index.Text)) != 0 {
		t.Fatalf("the index carries posts: %s", index.Text)
	}
}

func sourceIDs(text string) []string {
	var ids []string
	for _, source := range store.NoteSources(text) {
		ids = append(ids, source.EntryID)
	}
	sort.Strings(ids)
	return ids
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

// A verdict the app cannot read goes back to the reviewer as its tool result,
// and the reviewer, still on Super, gets another turn to submit it properly.
func TestReviewerRetriesMalformedToolVerdict(t *testing.T) {
	checks := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"action":"speak","text":"覚えました。"}`
	}, "routine")
	stub.review = func(user string) string {
		checks++
		if checks == 1 {
			return `{"verdict":"unknown","summary":"","issues":[]}`
		}
		if !strings.Contains(user, "確認結果を読めませんでした") {
			t.Errorf("reviewer did not receive its tool error: %s", user)
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}
	runner.react(post(t, s, "今日のことを覚えて").ID)
	if checks != 2 || len(remarks(t, s)) != 1 {
		t.Fatalf("review loop did not recover: checks=%d", checks)
	}
	for _, model := range stub.reviewModels {
		if model != store.NemotronSuper {
			t.Fatalf("review model = %q, want Super", model)
		}
	}
}

func TestReviewerRejectsIncompleteVerdicts(t *testing.T) {
	checks := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"action":"speak","text":"覚えました。"}`
	}, "routine")
	stub.review = func(user string) string {
		checks++
		switch checks {
		case 1:
			return `{"verdict":"pass"}`
		case 2:
			if !strings.Contains(user, "summary がありません") {
				t.Errorf("missing summary was not reported: %s", user)
			}
			return `{"verdict":"pass","summary":"ok","issues":[{"severity":"urgent","description":"問題"}]}`
		default:
			if !strings.Contains(user, "severity が不正") {
				t.Errorf("invalid severity was not reported: %s", user)
			}
			return `{"verdict":"pass","summary":"ok","issues":[]}`
		}
	}
	runner.react(post(t, s, "今日のことを覚えて").ID)
	if checks != 3 || len(remarks(t, s)) != 1 {
		t.Fatalf("incomplete verdicts were accepted: checks=%d", checks)
	}
}

func TestReviewFailureLeavesWorkflowUnexecuted(t *testing.T) {
	for _, mode := range []string{"malformed", "endpoint"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
			runner, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return `{"deliver":["routine"]}`
				}
				return `{"summary":"予定を置く","steps":[
					{"type":"write_schedule","path":"errands/plan.md","text":"---\ntype: Errand\n---\n\n予定","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"予定を確認","id":""},
					{"type":"speak","path":"","text":"あとで聞くね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
			}, "routine")
			if mode == "endpoint" {
				stub.fail = func(system string) bool { return strings.Contains(system, "あなたはアプリ本体です") }
			} else {
				stub.review = func(string) string {
					return `{"verdict":"pass"}`
				}
			}
			runner.react(post(t, s, "あとで予定を確認して").ID)
			if _, ok := s.Note("routine", "errands/plan.md"); ok {
				t.Fatal("an unreviewed note was written")
			}
			if scheduled, err := s.Schedules("routine"); err != nil || len(scheduled) != 0 {
				t.Fatalf("an unreviewed schedule was placed: %+v (%v)", scheduled, err)
			}
			if said := remarks(t, s); len(said) != 0 {
				t.Fatalf("an unreviewed remark was published: %+v", said)
			}
			if mode == "malformed" && len(stub.reviews) != 3 {
				t.Fatalf("review attempts = %d, want 3", len(stub.reviews))
			}
		})
	}
}

// Keeping a note and speaking stays in the persona's own corner, so it costs no
// review; a schedule reaches further and is still checked. A persona that can
// schedule is reviewed even for a note and a remark, because the promise it
// tends to make - a time with nothing set to wake - lives in exactly that shape.
func TestOnlyWorkflowsThatReachFurtherThanNotesAndRemarksAreReviewed(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		skill   string
		steps   string
		reviews int
	}{
		{"gourmet", `{"type":"write_note","path":"shops/sento.md","text":"---\ntype: Shop\n---\n\n銭湯の帰りの定食屋","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"帰りに一杯どう","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 0},
		{"routine", `{"type":"write_note","path":"errands/bath.md","text":"---\ntype: Errand\n---\n\nお風呂","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"いってらっしゃい","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 1},
		{"routine", `{"type":"write_schedule","path":"errands/bath.md","text":"---\ntype: Errand\n---\n\nお風呂","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"入ったか","id":""},
			{"type":"speak","path":"","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 1},
	} {
		runner, s, stub := setup(t, func(system, user string) string {
			if strings.Contains(system, "振り分け") {
				return `{"deliver":["` + tc.skill + `"]}`
			}
			return `{"summary":"覚える","steps":[` + tc.steps + `]}`
		}, tc.skill)
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
	plana := workflowSpec(registry.For([]string{"schedule"}), false, false)
	tabe := workflowSpec(registry.For([]string{"web_search", "place_search"}), false, false)

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

func TestAnUltraTurnMayReadTwentyTimes(t *testing.T) {
	reads := 0
	runner, s, stub := setup(t, func(system, user string) string {
		reads++
		return `{"action":"tool","tool":"notes_list","args":{}}`
	}, "gourmet")
	entry := post(t, s, "ラーメン")
	client, settings := runner.clientOf()
	skill := runner.catalogue.Active(settings.Active)[0]
	runner.run(context.Background(), skill, []timeline.Entry{entry}, entry, false, false, "", true,
		client, settings, runner.registry(settings))
	// Twenty reads run. On the next request, read is no longer offered and the
	// fake submits a quiet workflow, ending the turn.
	if reads != ultraReads+1 || ultraReads != 20 {
		t.Fatalf("Ultra requests = %d (ultraReads %d)", reads, ultraReads)
	}
	if got := stub.lastModelFor("tabe"); got != store.NemotronUltra {
		t.Fatalf("model = %q, want Ultra", got)
	}
	if prompt := stub.promptFor("tabe"); !strings.Contains(prompt, "20 回まで") {
		t.Fatalf("Ultra was not told its read limit: %s", prompt)
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

	text, _ := runner.readAll(context.Background(), skill, []ReadStep{{Type: "tool", Tool: "notes_read", Paths: []string{"index.md"}}}, set, timeline.Entry{}, newProvenance(), failures, newLookups(maxReads, maxSearches), map[string]string{})
	if !strings.Contains(text, "目次") {
		t.Fatalf("a note read in the wrong field was refused: %s", text)
	}

	broken := []ReadStep{{Type: "tool", Tool: "place_search", Args: `{"query":"神谷町"}`}}
	first, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, newProvenance(), failures, newLookups(maxReads, maxSearches), map[string]string{})
	if strings.Contains(first, "前にも同じ理由で失敗") {
		t.Fatalf("a first failure was called a repeat: %s", first)
	}
	second, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, newProvenance(), failures, newLookups(maxReads, maxSearches), map[string]string{})
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
	text, _ := runner.readAll(context.Background(), skill,
		[]ReadStep{{Type: "tool", Tool: "notes_read", Paths: []string{"tastes/tonkotsu.md"}, Args: "{}"}},
		set, timeline.Entry{}, newProvenance(), map[string]string{}, newLookups(maxReads, maxSearches), map[string]string{})
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

	first, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, newProvenance(), failures, newLookups(maxReads, maxSearches), map[string]string{})
	if !strings.Contains(first, "道具が使えませんでした") || strings.Contains(first, "前にも同じ理由で") {
		t.Fatalf("first failure: %s", first)
	}
	second, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, newProvenance(), failures, newLookups(maxReads, maxSearches), map[string]string{})
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
	tabe := workflowSpec(registry.For([]string{"web_search", "place_search"}), false, false)
	if strings.Contains(tabe, `"at":`) || strings.Contains(tabe, "### tool\n") {
		t.Fatalf("the spec showed a lookup-only persona fields or steps it cannot send")
	}
	plana := workflowSpec(registry.For([]string{"schedule"}), false, false)
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
			want:   &store.Routing{Delivered: []string{"move"}, Excluded: map[string]string{"book": "本の話ではない"}, Tracked: true, Done: []string{"gourmet", "move"}, Spoke: []string{}},
		},
		{
			name: "named persona is not passed over either", text: "@tabe 今日の昼どうしよう",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{}, Excluded: map[string]string{"move": "b", "book": "c"}, Tracked: true, Done: []string{"gourmet"}, Spoke: []string{}},
		},
		{
			name: "nobody is an answer", text: "今日は眠い",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{}, Excluded: map[string]string{"gourmet": "a", "move": "b", "book": "c"}, Tracked: true, Done: []string{}, Spoke: []string{}},
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

// A turn planned by an external agent finishes on its own goroutine, entirely
// apart from the wave that delivered the post, so its completion can land at
// the same moment the wave itself is writing down what the other persona did. A read-modify-write
// that is not made atomic here would let one of the two writes clobber the
// other, dropping a reply that really happened - which is what a person saw
// as a persona's own routing verdict flipping from quiet to replied only
// after reopening the sheet.
// slowReadWorkspace widens the gap between a read-modify-write's read and its
// write, so two racing updates that are not serialized reliably interleave
// their reads before either writes back - instead of relying on scheduling
// luck to expose the same bug.
type slowReadWorkspace struct {
	store.Workspace
	delay time.Duration
}

func (w *slowReadWorkspace) Routing(id string) (*store.Routing, error) {
	time.Sleep(w.delay)
	return w.Workspace.Routing(id)
}

func TestConcurrentTurnFinishesDoNotLoseEachOthersUpdate(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := post(t, s, "調べて")
	if err := s.SaveRouting(entry.ID, store.Routing{
		Delivered: []string{"gourmet", "book"}, Excluded: map[string]string{}, Done: []string{}, Spoke: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	slow := &slowReadWorkspace{Workspace: s, delay: 20 * time.Millisecond}
	runner := New(slow, NewCatalogue(s.SkillDir()), nil)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runner.turnsDone(entry.ID, []Skill{{ID: "gourmet"}}, map[string]bool{"gourmet": true})
	}()
	go func() { defer wg.Done(); runner.FinishAsyncTurn(entry.ID, "book", true) }()
	wg.Wait()

	record, err := s.Routing(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gourmet", "book"} {
		if !contains(record.Done, id) || !contains(record.Spoke, id) {
			t.Fatalf("a racing write must not drop the other persona's recorded turn: %+v", record)
		}
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
	// A remark on a post leaves a note behind; a note without a remark is fine.
	talkOnly := Workflow{Steps: []Step{{Type: "speak", Text: "また豚骨だ"}}}
	if problems := checkWorkflow(talkOnly, turnRules{set: set, noteRequired: true}); len(problems) != 1 || !strings.Contains(problems[0], "メモがありません") {
		t.Fatalf("a remark without a note on a post was accepted: %v", problems)
	}
	if problems := checkWorkflow(talkOnly, turnRules{set: set}); len(problems) != 0 {
		t.Fatalf("a remark without a note on the app's own wake was refused: %v", problems)
	}
	noteOnly := Workflow{Steps: []Step{{Type: "write_note", Path: "a.md", Text: "x"}, {Type: "speak", Text: ""}}}
	if problems := checkWorkflow(noteOnly, turnRules{set: set, noteRequired: true}); len(problems) != 0 {
		t.Fatalf("a note without a remark was refused: %v", problems)
	}
}

// None of the persona models take a picture, so the original is never handed
// over: a persona that asks for it is told what it already has and goes on.
// The one read on arrival is made rich enough - colour, shape, count, layout -
// that there is nothing left to ask.
func TestThePictureIsNeverHandedOver(t *testing.T) {
	asked := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		if asked == 0 {
			asked++
			return `{"action":"tool","tool":"image"}`
		}
		return `{"action":"speak","text":"白のブレンドですね"}`
	}, "gourmet")
	blob, err := s.PutBlob([]byte("png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	read := time.Now()
	entry := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User, Text: "楽しみ", At: time.Now(),
		Media: &timeline.Media{BlobID: blob, MIME: "image/png", ReadAt: &read, Describe: "ワインのラベル"}}
	entry.Root = entry.ID
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	runner.react(entry.ID)
	if said := remarks(t, s); len(said) != 1 || said[0].Text != "白のブレンドですね" {
		t.Fatalf("remarks = %+v", said)
	}
	if !stub.seen("元画像は渡せません") {
		t.Fatal("the persona was not told the picture is not handed over")
	}
	if strings.Contains(stub.promptFor("tabe"), "### image") {
		t.Fatal("the image read is still described to the persona")
	}
}

// A remark on a post leaves a note behind, and the remark says which: the
// timeline can lead from what a persona said to what it wrote down.
func TestARemarkPointsAtTheNotesWrittenWithIt(t *testing.T) {
	workflow := map[string]any{"summary": "test", "steps": []any{
		map[string]any{"type": "write_note", "path": "wines/altura.md", "text": "type: Wine\n\nNorton Altura", "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""},
		map[string]any{"type": "speak", "path": "", "text": "ノートンの白ですね", "tool": "", "args": "{}", "at": "", "daily": false, "note": "", "id": ""},
	}}
	scripted, _ := json.Marshal(workflow)
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["move"]}`
		}
		return string(scripted)
	}, "move")
	runner.react(post(t, s, "ワインのラベル").ID)
	said := remarks(t, s)
	if len(said) != 1 || len(said[0].Notes) != 1 || said[0].Notes[0] != "wines/altura.md" {
		t.Fatalf("remarks = %+v", said)
	}
}

// A closing fence glued to the last value, or to the first line of the body,
// leaves a note that never closes its front matter. Both are mended on write;
// a block that is already right is left alone.
func TestAGluedFrontmatterFenceIsMended(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"glued to the value", "---\ntype: Wine\ntimestamp: 2026-09-18T21:06:00+09:00---\n\n基本情報", "---\ntype: Wine\ntimestamp: 2026-09-18T21:06:00+09:00\n---\n\n基本情報"},
		{"glued to the body", "---\ntype: Wine\n---基本情報\n生産者", "---\ntype: Wine\n---\n基本情報\n生産者"},
		{"indented", "  ---\n  type: Wine\n  ---\n本文", "---\ntype: Wine\n---\n本文"},
		{"already right", "---\ntype: Wine\n---\n本文 --- 区切り", "---\ntype: Wine\n---\n本文 --- 区切り"},
		{"no front matter", "ただのメモ---", "ただのメモ---"},
	} {
		if got := repairFrontmatter(tc.in); got != tc.want {
			t.Fatalf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// What a search found is not hung on the remark as it is. The persona names
// in its speak the posts it rested on, by id, and those alone become the
// links under it - an id no search of this turn returned sends the workflow
// back. A live routine persona once cited ten posts it never used.
func TestARemarkLinksOnlyThePostsItsSpeakNames(t *testing.T) {
	calls := 0
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		calls++
		switch calls {
		case 1:
			return `{"action":"tool","tool":"timeline_search","args":{"query":"ピノ","limit":5}}`
		case 2:
			return `{"summary":"前のピノを引く","steps":[` +
				`{"type":"write_note","path":"tastes/pinot.md","text":"---\ntype: Preference\n---\n\nピノが好き","sources":[],"tool":"","args":"{}"},` +
				`{"type":"speak","path":"","text":"前に飲んだピノに近いね","sources":["old","made-up"],"tool":"","args":"{}"}]}`
		default:
			return `{"summary":"前のピノを引く","steps":[` +
				`{"type":"write_note","path":"tastes/pinot.md","text":"---\ntype: Preference\n---\n\nピノが好き","sources":[],"tool":"","args":"{}"},` +
				`{"type":"speak","path":"","text":"前に飲んだピノに近いね","sources":["old"],"tool":"","args":"{}"}]}`
		}
	}, "gourmet")
	runner.SetSearch(search.New(s))
	old := timeline.Entry{ID: "old", Kind: timeline.KindPost, Speaker: timeline.User, Text: "ピノ・ノワール、軽くて飲みやすかった", At: time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)}
	old.Root = old.ID
	if err := s.Append(old); err != nil {
		t.Fatal(err)
	}

	runner.react(post(t, s, "今日は赤ワイン").ID)

	said := remarks(t, s)
	if len(said) != 1 || said[0].Text != "前に飲んだピノに近いね" {
		t.Fatalf("calls = %d, remarks = %+v", calls, said)
	}
	if calls != 3 {
		t.Fatalf("a speak citing a post no search returned was accepted: calls = %d", calls)
	}
	if len(said[0].Sources) != 1 || said[0].Sources[0].URL != "/?entry=old" || !strings.Contains(said[0].Sources[0].Label, "2026-09-12") || !strings.Contains(said[0].Sources[0].Label, "ピノ・ノワール") {
		t.Fatalf("sources = %+v", said[0].Sources)
	}
}

// The check itself: a speak may cite what this turn's searches found and
// nothing else, and a persona with nothing found cites nothing.
func TestSpeakSourcesMustComeFromThisTurnsSearches(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	speak := func(ids ...string) Workflow {
		return Workflow{Steps: []Step{{Type: "speak", Text: "ひとこと", Sources: ids}}}
	}
	if problems := checkWorkflow(speak("old"), turnRules{set: set, cited: map[string]string{"old": "2026-09-12 ピノ"}}); len(problems) != 0 {
		t.Fatalf("a found post was refused: %v", problems)
	}
	if problems := checkWorkflow(speak("old", "made-up"), turnRules{set: set, cited: map[string]string{"old": "2026-09-12 ピノ"}}); len(problems) != 1 || !strings.Contains(problems[0], "made-up") {
		t.Fatalf("problems = %v", problems)
	}
	if problems := checkWorkflow(speak("old"), turnRules{set: set}); len(problems) != 1 {
		t.Fatalf("a citation with no search behind it passed: %v", problems)
	}
	if sources := citedSources([]string{" old ", "made-up"}, map[string]string{"old": "2026-09-12 ピノ"}); len(sources) != 1 || sources[0].URL != "/?entry=old" || sources[0].Label != "2026-09-12 ピノ" {
		t.Fatalf("sources = %+v", sources)
	}
	// The spec tells the persona about the field, and every sample carries it.
	spec := workflowSpec(set, false, false)
	if !strings.Contains(spec, "**sources**") || !strings.Contains(spec, `"sources":[]`) {
		t.Fatalf("the spec does not explain sources:\n%s", spec)
	}
}

func TestReplySourcesKeepOnlyTheFirstThreeUniqueLinks(t *testing.T) {
	sources := dedupe([]timeline.Source{
		{Label: "one", URL: "https://example.com/1"},
		{Label: "one again", URL: "https://example.com/1"},
		{Label: "two", URL: "https://example.com/2"},
		{Label: "three", URL: "https://example.com/3"},
		{Label: "four", URL: "https://example.com/4"},
	})
	if len(sources) != maxReplySources || sources[0].Label != "one" || sources[2].Label != "three" {
		t.Fatalf("sources = %+v", sources)
	}
}
