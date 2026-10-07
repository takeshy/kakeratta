package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
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
	calendar   func(user string) string
	prepared   map[string]testTurn
	phases     []string
	phaseReply func(system, user string, offered map[string]bool) string
	mu         sync.Mutex
	prompts    []string
	// models are the model each prompt was sent to, by the same index.
	models       []string
	answerModels map[string][]string
	reply        func(system, user string) string
	// reviews are the app's own checks, kept apart from what the personas were
	// asked so a test counting persona calls is not counting those. review
	// scripts a verdict; unset, every workflow passes.
	reviews       []string
	reviewSystems []string
	reviewModels  []string
	review        func(user string) string
	// records numbers the notes the fake keeps for scripted remarks.
	records int
	// fail makes the endpoint answer 500 to a request whose system prompt it
	// matches, for a test that needs a call to fail.
	fail func(system string) bool
	// keep scripts what the note keeper hands in, as keep_memory's arguments;
	// unset, it records the kakera in one sentence and changes no field.
	keep func(system, user string) string
	// keeps are the prompts the note keeper was sent.
	keeps []string
	// summarize scripts write_summaries; summaries are the prompts it was
	// sent. Unset, it rewrites nothing.
	summarize func(system, user string) string
	summaries []string
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
					Name        string         `json:"name"`
					Description string         `json:"description"`
					Parameters  map[string]any `json:"parameters"`
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
		if strings.HasPrefix(system, "# Person calendar events") {
			content := `{"events":[]}`
			if f.calendar != nil {
				content = f.calendar(user)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
			return
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
		names := toolNames(body.Tools)
		if names["submit_answer"] {
			f.mu.Lock()
			if f.answerModels == nil {
				f.answerModels = map[string][]string{}
			}
			answerKey := strings.Split(system, "\n")[0]
			f.answerModels[answerKey] = append(f.answerModels[answerKey], body.Model)
			f.mu.Unlock()
		}
		key := strings.Split(system, "\n")[0]
		phase := ""
		for _, name := range []string{"schedule", "action"} {
			if names["submit_"+name] {
				phase = name
			}
		}
		f.mu.Lock()
		f.phases = append(f.phases, system+"\n"+user+"\n"+offered)
		if f.prepared == nil {
			f.prepared = map[string]testTurn{}
		}
		prepared, have := f.prepared[key]
		script := f.phaseReply
		f.mu.Unlock()
		// ask calls the scripted persona and remembers the turn it describes.
		ask := func() testTurn {
			f.mu.Lock()
			f.prompts = append(f.prompts, system+"\n----\n"+user+"\n----\n"+offered)
			f.models = append(f.models, body.Model)
			f.mu.Unlock()
			next := splitTestTurn(f.reply(system, user))
			f.mu.Lock()
			f.prepared[key] = next
			f.mu.Unlock()
			return next
		}
		var result map[string]any
		switch {
		case names["write_summaries"]:
			f.mu.Lock()
			f.summaries = append(f.summaries, system+"\n----\n"+user)
			summarize := f.summarize
			f.mu.Unlock()
			args := `{"summaries":[]}`
			if summarize != nil {
				args = summarize(system, user)
			}
			result = testCall("write_summaries", json.RawMessage(args))
		case names["keep_memory"]:
			f.mu.Lock()
			f.keeps = append(f.keeps, system+"\n----\n"+user)
			keep := f.keep
			f.mu.Unlock()
			args := `{"summary":"テストのかけら","tags":["テスト"],"facts":[]}`
			if keep != nil {
				args = keep(system, user)
			}
			result = testCall("keep_memory", json.RawMessage(args))
		case script != nil && (names["finish_read"] || names["submit_answer"] || phase != ""):
			result = asCall(script(system, user, names), names)
		case names["finish_read"]:
			// A deep turn reading on Super: read when the scripted turn reads,
			// otherwise finish so the answer model submits it.
			f.mu.Lock()
			f.prompts = append(f.prompts, system+"\n----\n"+user+"\n----\n"+offered)
			f.models = append(f.models, body.Model)
			f.mu.Unlock()
			result = asCall(f.reply(system, user), map[string]bool{"read": true, "finish_read": true})
		case names["submit_answer"]:
			f.mu.Lock()
			f.prompts = append(f.prompts, system+"\n----\n"+user+"\n----\n"+offered)
			f.models = append(f.models, body.Model)
			f.mu.Unlock()
			content := f.reply(system, user)
			candidate := asCall(content, map[string]bool{"read": true, "legacy_turn": true})
			if calledName(candidate) == "read" && names["read"] {
				result = candidate
			} else {
				prepared = splitTestTurn(content)
				prepared.answered = true
				f.mu.Lock()
				f.prepared[key] = prepared
				f.mu.Unlock()
				result = testCall("submit_answer", prepared.answer)
			}
		case phase != "":
			// A phase asked a second time was sent back or blamed by a review:
			// the script says what the persona hands in now.
			if !have || prepared.done[phase] {
				prepared = ask()
			}
			f.mu.Lock()
			if prepared.done == nil {
				prepared.done = map[string]bool{}
			}
			prepared.done[phase] = true
			f.prepared[key] = prepared
			f.mu.Unlock()
			result = testCall("submit_"+phase, prepared.submission(phase))
		default:
			f.mu.Lock()
			f.prompts = append(f.prompts, system+"\n----\n"+user+"\n----\n"+offered)
			f.models = append(f.models, body.Model)
			f.mu.Unlock()
			if f.reply == nil {
				result = map[string]any{"content": ""}
			} else {
				result = asCall(f.reply(system, user), names)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": result}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// testTurn is a scripted turn taken apart: the answer, and the operations
// each later phase hands in.
type testTurn struct {
	answered bool
	recorded bool
	done     map[string]bool
	answer   Answer
	steps    []testStep
}

// testStep is one change a scripted turn makes, in the shape the fixtures
// write it.
type testStep struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	At     string `json:"at"`
	Daily  bool   `json:"daily"`
	Notify *bool  `json:"notify"`
	Note   string `json:"note"`
	ID     string `json:"id"`
}

// submission is what one phase of a scripted turn hands in.
func (turn testTurn) submission(phase string) map[string]any {
	operations := []any{}
	for _, step := range turn.steps {
		switch {
		case phase == "schedule" && (step.Type == "write_schedule" || step.Type == "cancel_schedule" || step.Type == "update_schedule"):
			kind := "create"
			if step.Type == "update_schedule" {
				kind = "update"
			}
			if step.Type == "cancel_schedule" {
				kind = "cancel"
			}
			operations = append(operations, map[string]any{"type": kind, "at": step.At, "daily": step.Daily, "notify": step.Notify, "note": step.Note, "id": step.ID})
		case phase == "action" && step.Type == "tool":
			operations = append(operations, map[string]any{"tool": step.Tool, "args": step.Args})
		}
	}
	return map[string]any{"operations": operations, "consistent": true, "reason": "test"}
}

// calledName is the tool a reply calls, or "" for plain text.
func calledName(message map[string]any) string {
	calls, _ := message["tool_calls"].([]any)
	if len(calls) == 0 {
		return ""
	}
	call, _ := calls[0].(map[string]any)
	function, _ := call["function"].(map[string]any)
	name, _ := function["name"].(string)
	return name
}

// Legacy scenario fixtures describe the answer and the changes together as
// one workflow; they are taken apart at the fake endpoint only. Production
// has no speak step and no legacy_turn tool.
func splitTestTurn(content string) testTurn {
	result := asCall(content, map[string]bool{"legacy_turn": true})
	next := testTurn{answer: Answer{Sources: []string{}}}
	if calledName(result) != "legacy_turn" {
		return next
	}
	raw, _ := json.Marshal(result)
	var msg struct {
		Calls []struct {
			Function struct {
				Args string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	json.Unmarshal(raw, &msg)
	var wf struct {
		Steps []struct {
			testStep
			Sources []string `json:"sources"`
		} `json:"steps"`
	}
	json.Unmarshal([]byte(msg.Calls[0].Function.Args), &wf)
	for _, step := range wf.Steps {
		if step.Type == "speak" {
			next.answer = Answer{Text: step.Text, Sources: append([]string{}, step.Sources...)}
		} else {
			next.steps = append(next.steps, step.testStep)
		}
	}
	return next
}

func testCall(name string, args any) map[string]any {
	raw, _ := json.Marshal(args)
	return map[string]any{"content": "", "tool_calls": []any{map[string]any{"id": "call_" + name, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}
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
	// "read:" followed by steps is a phase looking something up first.
	if rest, ok := strings.CutPrefix(content, "read:"); ok && offered["read"] {
		return call("read", json.RawMessage(rest))
	}
	// "force-read:" calls read even when it was not offered, as a live model
	// told to submit has been seen to.
	if rest, ok := strings.CutPrefix(content, "force-read:"); ok {
		return call("read", json.RawMessage(rest))
	}
	switch {
	case offered["read"] && scripted && parsed.Steps != nil && !offered["legacy_turn"]:
		return call("read", json.RawMessage(content))
	case offered["submit_answer"]:
		return call("submit_answer", json.RawMessage(content))
	case offered["finish_read"]:
		// Anything but read steps ends the reading; "read_done" says so.
		return call("finish_read", map[string]any{})
	case offered["submit_schedule"]:
		return call("submit_schedule", json.RawMessage(content))
	case offered["submit_action"]:
		return call("submit_action", json.RawMessage(content))
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

	case offered["legacy_turn"]:
		if !scripted {
			return map[string]any{"content": content}
		}
		if parsed.Steps != nil {
			return call("legacy_turn", json.RawMessage(content))
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
				read := map[string]any{"type": "tool", "tool": parsed.Tool, "args": string(args)}
				switch parsed.Tool {
				case "records":
					read = map[string]any{"type": "records", "tool": "", "args": string(args)}
				case "image":
					read = map[string]any{"type": "image", "tool": "", "args": string(args)}
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
		return call("legacy_turn", map[string]any{"summary": "test", "steps": steps})

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

// lastModelFor returns the final-answer model for the persona with this name.
func (f *fake) lastModelFor(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for prompt, models := range f.answerModels {
		if strings.HasPrefix(prompt, "あなたは「"+name+"」") {
			return models[len(models)-1]
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
	catalogue := NewCatalogue(s)
	for _, id := range active {
		if id == testTravelSkill.ID {
			raw, err := json.Marshal(testTravelSkill)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := catalogue.Install(raw, nil); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
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

// Explicit detail strings select Ultra before answering with the parent context - by the persona it was said
// to. Other personas do not receive the reply through automatic routing.
func TestAReplyAskingForMoreIsAnsweredOnUltra(t *testing.T) {
	for _, tc := range []struct {
		reply    string
		escalate bool
		want     string
		slot     string
	}{
		{"詳しく", true, store.NemotronUltra, "ultra"},
		{"ありがとう", false, store.NemotronSuper, "super"},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			asked := ""
			replied := false
			runner, s, stub := setup(t, func(system, user string) string {
				switch {
				case strings.Contains(system, "振り分け") && !replied:
					return `{"deliver":["gourmet"]}`
				case strings.Contains(system, "振り分け"):
					// The reply is routed to someone else only.
					return `{"deliver":["move"]}`
				case strings.Contains(system, "あなたは「michi」"):
					return `{"action":"speak","text":"駅から5分"}`
				default:
					if replied {
						asked = user
					}
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
				t.Fatalf("Super was not shown the answer and the reply:\n%s", asked)
			}
			if got := stub.modelFor("michi"); got != "" {
				t.Fatalf("the other persona unexpectedly answered on %q", got)
			}
			// The persona replied to answers whether or not routing chose it.
			if got := stub.lastModelFor("tabe"); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
			models := map[string]string{}
			for _, remark := range remarks(t, s)[1:] {
				models[remark.Speaker] = remark.Model
			}
			if models["gourmet"] != tc.slot || models["move"] != "" {
				t.Fatalf("remark models = %v", models)
			}
			if !tc.escalate {
				return
			}
			// Ultra is not told to keep it short; Super is.
			prompt := ""
			for _, phase := range stub.phases {
				if strings.Contains(phase, "submit_answer") && strings.Contains(phase, "今回の依頼には深い回答が必要です") {
					prompt = phase
				}
			}
			if !strings.Contains(prompt, "今回の依頼には深い回答が必要です") || !strings.Contains(prompt, "必要な根拠・比較・具体例") || strings.Contains(prompt, "発言するときは短く") {
				t.Fatalf("tabe was not given the deep prompt:\n%s", prompt)
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
// It still takes one turn, with one Ultra answer when no read is needed.
func TestAnUltraReplyRunsThePersonaOnce(t *testing.T) {
	for _, tc := range []struct{ name, reply, routed string }{
		{"routed", "もっと詳しく", `{"deliver":["gourmet"]}`},
		{"mentioned", "@tabe もっと詳しく", `{"deliver":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replied := false
			runner, s, stub := setup(t, func(system, user string) string {
				switch {
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
					t.Fatalf("tabe answered on %v, want Ultra only", models)
				}
			}
			if said := remarks(t, s); len(said) != 2 || said[1].Model != "ultra" {
				t.Fatalf("remarks = %+v, want one more, on Ultra", said)
			}
		})
	}
}

// Standalone Lightning classification is absent for every entry kind.
func TestNoStandaloneLightningClassification(t *testing.T) {
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

// Not speaking is an outcome, not a failure - and it is not the same as not
// remembering: the kakera is recorded under itself.
func TestSilenceStillWritesNotes(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"action":"silent"}`
	}, "routine")

	entry := post(t, s, "errors.Join の話")
	runner.react(entry.ID)

	if said := remarks(t, s); len(said) != 0 {
		t.Fatalf("something was said when it should have been quiet: %+v", said)
	}
	found, _ := runner.findRecords("routine", "", "", "", 10, time.UTC)
	if len(found) != 1 || found[0].ID != entry.ID {
		t.Fatalf("a silent kakera must be recorded under itself: %+v", found)
	}
	if memory := s.Memory("routine"); memory.Wakes != 1 {
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
	if stub.promptFor("sona") != "" {
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

// A direct question is answered as the persona wrote it. Whether the answer
// meets the request is the persona's to judge: a reviewer on the app's model
// cannot see what an external agent found, and sent good answers back for it.
// Only schedules and actions are reviewed.
func TestANamedAnswerWithoutChangesIsNotReviewed(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["submit_answer"]:
			return `{"text":"Goがおすすめです。理由は配布が楽だからです。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		return `{"operations":[{"type":"write","path":"questions/lang.md","text":"---\ntitle: 言語\n---\n- どの言語がよいか聞かれた。配布の楽さでGoを挙げた。"}]}`
	}
	f.review = func(user string) string {
		return `{"verdict":"fail","summary":"不要","issues":[{"phase":"both","severity":"high","description":"呼ばれるべきでない"}]}`
	}
	r.react(post(t, s, "@sona どの言語がいい？理由も教えて").ID)
	if len(f.reviews) != 0 {
		t.Fatalf("a named answer was reviewed %d times", len(f.reviews))
	}
	said := remarks(t, s)
	if len(said) != 1 || said[0].Text != "Goがおすすめです。理由は配布が楽だからです。" {
		t.Fatalf("remarks = %+v", said)
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
	if stub.promptFor("sona") == "" {
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
	if stub.promptFor("sona") != "" || stub.promptFor("tabe") != "" {
		t.Fatal("a failed routing woke personas nobody named")
	}

	runner.react(post(t, s, "@tabe 何か").ID)
	if stub.promptFor("tabe") == "" {
		t.Fatal("a named persona was lost because the routing failed")
	}
	if stub.promptFor("sona") != "" {
		t.Fatal("a failed routing woke a persona nobody named")
	}
}

// The first prompt hands over the persona's memory - its fields whole and its
// latest records - and nothing else: another post is not handed over, and an
// older record is read only when asked for.
func TestAPersonaReadsAndWritesItsOwnNotes(t *testing.T) {
	runner, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["routine"]}`
		case strings.Contains(user, "records の結果"):
			return `{"action":"silent"}`
		default:
			return `{"action":"tool","tool":"records","args":{"query":"薬"}}`
		}
	}, "routine")
	if err := s.WriteNote("routine", "Routine.md", factsFile("Routine", [2]string{"朝の薬は毎日飲む", ""})); err != nil {
		t.Fatal(err)
	}
	old := timeline.Entry{ID: "e_old", Text: "薬を飲み忘れた", At: time.Date(2025, 1, 5, 8, 0, 0, 0, time.UTC)}
	writeRecord(t, s, "routine", old, old.ID)
	for i := 0; i < recentRecordsLimit; i++ {
		at := time.Date(2026, 9, 1, 8, i, 0, 0, time.UTC)
		writeRecord(t, s, "routine", timeline.Entry{ID: fmt.Sprintf("e_%02d", i), Text: "散歩した", At: at}, fmt.Sprintf("e_%02d", i))
	}
	_ = post(t, s, "関係のない古い投稿")
	entry := post(t, s, "新しい投稿")
	runner.react(entry.ID)

	prompt := stub.promptFor("sona")
	if !strings.Contains(prompt, "[f_t0] 朝の薬は毎日飲む") || !strings.Contains(prompt, fmt.Sprintf("[e_%02d] 散歩した", recentRecordsLimit-1)) {
		t.Fatalf("the persona was not handed its memory:\n%s", prompt)
	}
	if strings.Contains(prompt, "薬を飲み忘れた") {
		t.Fatalf("an old record was handed over unasked:\n%s", prompt)
	}
	if strings.Contains(prompt, "関係のない古い投稿") {
		t.Fatalf("another post was handed over:\n%s", prompt)
	}
	if !stub.seen("[e_old] 薬を飲み忘れた") {
		t.Fatal("the records read never handed the old record back")
	}
}

// Tools are the declared ones and nothing else. A plan reaching for another is
// sent back before anything runs, and the call is refused again at run time
// whatever the review said.
func TestAToolThatWasNotDeclaredIsRefused(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	set := runner.registry(s.Settings()).For([]string{"schedule"}).As("routine")
	plan := Workflow{Actions: []ActionOperation{{Tool: "place_search", Args: `{"query":"渋谷"}`}}}.batch()
	if problems := checkBatch(plan, turnRules{set: set}); len(problems) != 1 || !strings.Contains(problems[0], "place_search はあなたが使える道具ではありません") {
		t.Fatalf("problems = %v", problems)
	}
	skill, _ := runner.catalogue.Find(context.Background(), "routine")
	_, steps := runner.execute(context.Background(), skill, plan, set, turnRules{set: set}, timeline.Entry{})
	if len(steps) != 1 || steps[0].Error == "" {
		t.Fatalf("an undeclared tool ran: %+v", steps)
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
			wantCalls, wantAnswers := 3, 2
			if replyToQuestion {
				wantCalls, wantAnswers = 1, 1
			}
			if len(prompts) != wantCalls {
				t.Fatalf("got %d calls, want %d", len(prompts), wantCalls)
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
			if answers != wantAnswers {
				t.Fatalf("got %d answers, want %d", answers, wantAnswers)
			}
		})
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
// shaped against is having one without the other - sona wrote "予定: 22:30"
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
	if placed[0].Note != "ドラマを見たか" {
		t.Fatalf("schedule = %+v", placed[0])
	}
}

// A workflow stops at the step that fails. A wake that could not be placed
// leaves no note behind, and the remark after it never goes out.
func TestAPlanStopsAtTheStepThatFails(t *testing.T) {
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
	if placed, _ := s.Schedules("routine"); len(placed) != 0 {
		t.Fatalf("a schedule in the past was placed: %+v", placed)
	}
}

// The review is what stands between a persona and a promise it did not arrange.
// A schedule it rejects never runs, and the persona's second schedule is the
// one that does. The answer was finished before the review and stays as it was.
func TestAPlanTheReviewRejectsNeverRuns(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	drafts := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		drafts++
		if drafts == 1 {
			return `{"summary":"覚えて約束する","steps":[
				{"type":"write_schedule","path":"","text":"","tool":"","args":"{}","at":"` + at + `","daily":false,"note":"ドラマを見たか（2026-09-16 22:21 済）","id":""},
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
		// Only the invented line: sona's own instructions talk about 済んだ記録.
		if strings.Contains(user, "22:21 済") {
			return `{"verdict":"fail","summary":"起きていないことを書いている","issues":[{"phase":"schedule","severity":"high","description":"起きていないことを事実として書いている: 済"}]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}

	entry := post(t, s, "今日の22時半からドラマを見る")
	runner.react(entry.ID)

	if drafts != 2 || len(stub.reviews) != 2 {
		t.Fatalf("drafts = %d, reviews = %d", drafts, len(stub.reviews))
	}
	said := remarks(t, s)
	if len(said) != 1 || said[0].Text != "22:30ごろに聞くね" {
		t.Fatalf("remarks = %+v", said)
	}
	placed, err := s.Schedules("routine")
	if err != nil || len(placed) != 1 {
		t.Fatalf("placed = %+v (%v)", placed, err)
	}
	if strings.Contains(placed[0].Note, "22:21 済") {
		t.Fatalf("the rejected schedule was placed: %+v", placed[0])
	}
}

// reviewedTurn places a schedule, so the plan is one the app reviews.
var reviewedTurn = `{"summary":"予定を置く","steps":[
	{"type":"write_schedule","path":"errands/today.md","text":"---\ntype: Errand\n---\n\n今日のこと","tool":"","args":"{}","at":"` + time.Now().Add(2*time.Hour).Format(time.RFC3339) + `","daily":false,"note":"今日のこと","id":""},
	{"type":"speak","path":"","text":"覚えました。","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`

// A verdict the app cannot read goes back to the reviewer as its tool result,
// and the reviewer, still on Super, gets another turn to submit it properly.
func TestReviewerRetriesMalformedToolVerdict(t *testing.T) {
	checks := 0
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return reviewedTurn
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
		return reviewedTurn
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
			return `{"verdict":"pass","summary":"ok","issues":[{"phase":"schedule","severity":"urgent","description":"問題"}]}`
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

func TestReviewFailureLeavesThePlanUnexecuted(t *testing.T) {
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
// review, even for a persona that can schedule: a promised time with nothing
// set to wake is the schedule phase's to catch, as an inconsistency. A
// schedule reaches further and is still checked.
func TestOnlyPlansWithSchedulesOrActionsAreReviewed(t *testing.T) {
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		skill   string
		steps   string
		reviews int
	}{
		{"gourmet", `{"type":"write_note","path":"shops/sento.md","text":"---\ntype: Shop\n---\n\n銭湯の帰りの定食屋","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"帰りに一杯どう","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 0},
		{"routine", `{"type":"write_note","path":"errands/bath.md","text":"---\ntype: Errand\n---\n\nお風呂","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},
			{"type":"speak","path":"","text":"いってらっしゃい","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}`, 0},
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
func TestAnEmptyPlanIsNotReviewed(t *testing.T) {
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
	for _, deliver := range []string{`["sona"]`, `["sona","nobody-at-all"]`} {
		t.Run(deliver, func(t *testing.T) {
			runner, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return `{"deliver":` + deliver + `}`
				}
				return `{"action":"silent"}`
			}, "routine", "gourmet")

			entry := post(t, s, "今日の22時半からドラマを見る")
			runner.react(entry.ID)

			if stub.promptFor("sona") == "" {
				t.Fatal("the persona it named never saw the post")
			}
			if stub.promptFor("tabe") != "" {
				t.Fatal("a usable answer still woke everyone")
			}
		})
	}
}

// Each phase is told what the persona can do there. A persona without
// schedule is told it may place none, and reading is taught apart from any
// change.
func TestThePhasePromptsFollowThePersonasTools(t *testing.T) {
	registry := tools.New(tools.Config{}).WithSchedules(nil)
	skill := Skill{ID: "sona", Name: "ぷらな", Tone: "やさしく"}
	sona := phasePrompt("schedule", skill, registry.For([]string{"schedule"}), "")
	tabe := phasePrompt("schedule", skill, registry.For([]string{"web_search"}), "")
	if strings.Contains(sona, "予定操作を許可されていない") || !strings.Contains(tabe, "予定操作を許可されていない") {
		t.Fatal("schedule instructions do not follow permissions")
	}
	if !strings.HasPrefix(sona, "あなたは「ぷらな」。やさしく") {
		t.Fatalf("a phase does not open as the persona: %q", sona)
	}
	read := currentReadCapabilities(skill, registry.WithTavilyKey("test-key").For([]string{"web_search"}), newLookups(maxReads, maxSearches), nil, maxReads).instruction()
	if !strings.Contains(read, "web_search") || strings.Contains(read, "write_note") {
		t.Fatal("research spec contains memory actions or omits search")
	}
	if !strings.Contains(capabilityLine(registry.For([]string{"web_search"})), "約束しないでください") ||
		!strings.Contains(capabilityLine(registry.For([]string{"schedule"})), "schedule") {
		t.Fatal("the answer is not told what the later phases can do")
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
	entry := post(t, s, "ラーメンを詳しく")
	client, settings := runner.clientOf()
	skill := runner.catalogue.Active(context.Background(), settings.Active)[0]
	runner.run(context.Background(), skill, []timeline.Entry{entry}, entry, false, false, "", false,
		client, settings, runner.registry(settings))
	// Twenty reads run. On the next request, read is no longer offered and the
	// fake submits a quiet workflow, ending the turn.
	if reads != ultraReads+1 || ultraReads != 20 {
		t.Fatalf("Ultra requests = %d (ultraReads %d)", reads, ultraReads)
	}
	if len(stub.prompts) < 2 || !strings.Contains(stub.prompts[1], "read は残り 19 回") {
		t.Fatal("deep budget did not retain the spent first read")
	}
	if got := stub.lastModelFor("tabe"); got != store.NemotronUltra {
		t.Fatalf("model = %q, want Ultra", got)
	}
	if prompt := stub.promptFor("tabe"); !strings.Contains(prompt, "read は残り 20 回") {
		t.Fatalf("deep answer did not start with expanded budget: %s", prompt)
	}
}

// A search that keeps failing the same way is called out on the second try,
// and a note read written as a tool call is simply done.
func TestReadingForgivesTheWrongFieldAndCallsOutARepeatedFailure(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "records/2026-09.md", "- 2026-09-30 12:00 [e_1] 目次の話 #本\n"); err != nil {
		t.Fatal(err)
	}
	set := tools.New(tools.Config{}).For([]string{"place_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find(context.Background(), "gourmet")
	failures := map[string]string{}

	text, _ := runner.readAll(context.Background(), skill, []ReadStep{{Type: "tool", Tool: "records", Args: `{"tag":"本"}`}}, set, timeline.Entry{}, time.UTC, failures, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(text, "目次") {
		t.Fatalf("a note read in the wrong field was refused: %s", text)
	}

	broken := []ReadStep{{Type: "tool", Tool: "place_search", Args: `{"query":"神谷町"}`}}
	first, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, time.UTC, failures, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if strings.Contains(first, "前にも同じ理由で失敗") {
		t.Fatalf("a first failure was called a repeat: %s", first)
	}
	second, _ := runner.readAll(context.Background(), skill, broken, set, timeline.Entry{}, time.UTC, failures, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(second, "前にも同じ理由で失敗") {
		t.Fatalf("a repeated failure was not called out: %s", second)
	}
}

// A records read written as a tool call is the same request in the wrong
// field, and it is read rather than refused.
func TestARecordsReadWrittenAsAToolIsStillRead(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "records/2026-09.md", "- 2026-09-30 12:00 [e_1](/?entry=e_1) 豚骨に寄る #ラーメン\n"); err != nil {
		t.Fatal(err)
	}
	set := runner.registry(s.Settings()).For([]string{"web_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find(context.Background(), "gourmet")
	text, _ := runner.readAll(context.Background(), skill,
		[]ReadStep{{Type: "tool", Tool: "records", Args: `{"query":"豚骨"}`}},
		set, timeline.Entry{}, time.UTC, map[string]string{}, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(text, "豚骨に寄る") {
		t.Fatalf("the note was not read: %s", text)
	}
}

// The same call failing the same way twice is said outright, so a turn is not
// spent on one broken search while another tool sits unused.
func TestARepeatedFailingCallIsCalledOut(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	set := runner.registry(s.Settings()).For([]string{"place_search"}).As("gourmet")
	skill, _ := runner.catalogue.Find(context.Background(), "gourmet")
	failures := map[string]string{}
	search := []ReadStep{{Type: "tool", Tool: "place_search", Paths: []string{}, Args: `{"query":"神谷町"}`}}

	first, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, time.UTC, failures, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(first, "道具が使えませんでした") || strings.Contains(first, "前にも同じ理由で") {
		t.Fatalf("first failure: %s", first)
	}
	second, _ := runner.readAll(context.Background(), skill, search, set, timeline.Entry{}, time.UTC, failures, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(second, "前にも同じ理由で失敗しています") {
		t.Fatalf("a repeated failure was not called out: %s", second)
	}
}

// Only a tool the persona holds, and one that changes something, is an action.
func TestOnlyTheToolsAPersonaHoldsAreActions(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	for _, tool := range []string{"speak", "notes_forget", "schedule"} {
		if problems := checkBatch(Workflow{Actions: []ActionOperation{{Tool: tool, Args: "{}"}}}.batch(), turnRules{set: set}); len(problems) == 0 {
			t.Fatalf("%s was smuggled in as an action", tool)
		}
	}
}

// Every read ends with how many are left and which other tools there are.
func TestAReadSaysWhatIsLeftAndWhatElseThereIs(t *testing.T) {
	set := tools.New(tools.Config{}).For([]string{"place_search", "web_search", "schedule"})
	footer := readFooter(3, set)
	for _, want := range []string{"残り 3 回", "place_search", "web_search", "schedules_list"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer lost %q: %s", want, footer)
		}
	}
	if strings.Contains(strings.ReplaceAll(footer, "schedules_list", ""), "schedule") {
		t.Fatalf("schedule is not something to read with: %s", footer)
	}
	if !strings.Contains(readFooter(0, set), "現在のフェーズ") {
		t.Fatal("the last read did not say to hand in the workflow")
	}
}

// Web search is used in the read, like reading a note, and never as an action.
func TestWebSearchIsNotAnAction(t *testing.T) {
	set := tools.New(tools.Config{}).For([]string{"web_search"})
	problems := checkBatch(operationBatch{Steps: []operation{{Type: "tool", Tool: "web_search", Args: `{"query":"x"}`}}}, turnRules{set: set})
	if len(problems) != 1 || !strings.Contains(problems[0], "read で使ってください") {
		t.Fatalf("problems = %v", problems)
	}
}

// A proposal sent back for its shape does not use up the review's second look.
// A live run spent its first draft on a malformed step, and the review then
// rejected the second with no chance left to act on what it found.
func TestAMalformedDraftDoesNotUseUpTheReview(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"deliver":["routine"]}` }, "routine")
	wrong := time.Now().Add(5 * time.Hour).Format(time.RFC3339)
	right := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	script, calls := scheduleTurn(func(call int) string {
		switch call {
		case 1:
			return createSchedule("22時")
		case 2:
			return createSchedule(wrong)
		}
		return createSchedule(right)
	})
	f.phaseReply = script
	f.review = func(user string) string {
		if strings.Contains(user, wrong) {
			return `{"verdict":"fail","summary":"不正","issues":[{"phase":"schedule","severity":"high","description":"時刻が違う"}]}`
		}
		return `{"verdict":"pass","summary":"ok","issues":[]}`
	}
	r.react(post(t, s, "2時間後に知らせて").ID)
	if calls["schedule"] != 3 || len(f.reviews) != 2 {
		t.Fatalf("calls=%v reviews=%d", calls, len(f.reviews))
	}
	if said := remarks(t, s); len(said) != 1 || said[0].Text != "指定の時刻に知らせるね。" {
		t.Fatal(said)
	}
}

// Each phase's submit tool is offered in the shape of what the persona holds.
// Lookups are never an action, so a persona whose tools only look things up
// has no action phase at all.
func TestThePhaseSchemasFollowThePersonasTools(t *testing.T) {
	registry := tools.New(tools.Config{}).WithSchedules(nil)
	lookups := registry.For([]string{"web_search", "place_search"})
	if actions := shapeOf(lookups).actions; len(actions) != 0 {
		t.Fatalf("a persona that only looks things up has actions: %v", actions)
	}
	fields := func(phase string) map[string]any {
		properties := phaseTool(phase, lookups).Parameters["properties"].(map[string]any)
		items := properties["operations"].(map[string]any)["items"].(map[string]any)
		return items["properties"].(map[string]any)
	}
	if fields("schedule")["at"] == nil || fields("schedule")["path"] != nil || fields("schedule")["text"] != nil {
		t.Fatal("the schedule phase was offered a note to write")
	}
	schedule := phaseTool("schedule", lookups).Parameters["required"].([]string)
	if strings.Join(schedule, ",") != "operations,consistent,reason" {
		t.Fatalf("schedule phase required = %v", schedule)
	}
}

// The review looks at schedules and actions only: the answer's research and
// its length are not the app's to judge.
func TestTheReviewLeavesTheAnswerAlone(t *testing.T) {
	if !strings.Contains(reviewPrompt, "Answerの品質") || !strings.Contains(reviewPrompt, "外部調査の出典") {
		t.Fatal("review lost the line that keeps it off the answer")
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

// What happened to a post is kept for a person to read: who the router chose,
// who the user called by name, and why the router passed over the rest. A
// persona reached by name is listed as called, not as the router's choice, and
// anyone who got the post is not among the passed-over. A router with no
// answer and nobody called leaves no record at all.
func TestRoutingDecisionIsKeptForThePost(t *testing.T) {
	for _, tc := range []struct {
		name, routed, text string
		want               *store.Routing
	}{
		{
			name: "named persona is called, not the router's choice", text: "@tabe 今日の昼どうしよう",
			routed: `{"deliver":["gourmet","move"],"excluded_reasons":{"book":"本の話ではない"}}`,
			want:   &store.Routing{Delivered: []string{"gourmet"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet"}, Spoke: []string{"gourmet"}, Called: map[string]string{"gourmet": "mention"}},
		},
		{
			name: "named persona is not passed over either", text: "@tabe 今日の昼どうしよう",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{"gourmet"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet"}, Spoke: []string{"gourmet"}, Called: map[string]string{"gourmet": "mention"}},
		},
		{
			name: "nobody is an answer", text: "今日は眠い",
			routed: `{"deliver":[],"excluded_reasons":{"gourmet":"a","move":"b","book":"c"}}`,
			want:   &store.Routing{Delivered: []string{}, Excluded: map[string]string{"gourmet": "a", "move": "b", "book": "c"}, Tracked: true, Done: []string{}, Spoke: []string{}},
		},
		{name: "a broken router is no answer", text: "今日は眠い", routed: `モデルが壊れた返事`, want: nil},
		{
			name: "a broken router still records who was called", text: "@tabe 今日の昼どうしよう", routed: `モデルが壊れた返事`,
			want: &store.Routing{Delivered: []string{"gourmet"}, Excluded: map[string]string{}, Tracked: true, Done: []string{"gourmet"}, Spoke: []string{"gourmet"}, Called: map[string]string{"gourmet": "mention"}},
		},
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

// Directed entries bypass both subject routing and person keyword matching.
func TestDirectedEntriesSkipAutomaticRouting(t *testing.T) {
	for _, tc := range []struct {
		name, text, speaker string
		want                []string
		called              map[string]string
		routes              int
	}{
		{"mention", "@tabe 佐藤さんと釣り", "", []string{"gourmet"}, map[string]string{"gourmet": "mention"}, 0},
		{"person mention", "@けんじ 昼食", "", []string{"sato"}, map[string]string{"sato": "mention"}, 0},
		{"reply", "佐藤さんと釣り", "gourmet", []string{"gourmet"}, map[string]string{"gourmet": "reply"}, 0},
		{"mention overrides reply", "@tabe 佐藤さんと釣り", "move", []string{"gourmet"}, map[string]string{"gourmet": "mention"}, 0},
		{"same recipient", "@tabe 佐藤さんと釣り", "gourmet", []string{"gourmet"}, map[string]string{"gourmet": "mention"}, 0},
		{"ordinary post", "佐藤さんと釣り", "", []string{"move", "sato"}, nil, 1},
		{"user reply", "佐藤さんと釣り", timeline.User, []string{"move", "sato"}, nil, 1},
		{"inactive reply", "佐藤さんと釣り", "book", []string{"move", "sato"}, nil, 1},
		{"unknown mention", "@unknown 佐藤さんと釣り", "", []string{"move", "sato"}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes := 0
			runner, s, _ := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					routes++
					return `{"deliver":["move"]}`
				}
				return `{"action":"silent","notes":""}`
			}, "gourmet", "move", "sato")
			raw, err := json.Marshal(sato())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runner.catalogue.Install(raw, nil); err != nil {
				t.Fatal(err)
			}
			entry := post(t, s, tc.text)
			if tc.speaker != "" {
				parent := entry
				if tc.speaker != timeline.User {
					parent = timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: tc.speaker, Root: entry.ID, ReplyTo: entry.ID, Text: "質問", At: time.Now()}
					if err := s.Append(parent); err != nil {
						t.Fatal(err)
					}
				}
				entry = replyTo(t, s, parent, tc.text)
			}
			plan, record, err := runner.prepareEntry(runner.withSkills(context.Background()), entry.ID, nil, false)
			if err != nil || plan == nil || record == nil {
				t.Fatalf("prepare: %+v, %+v, %v", plan, record, err)
			}
			sort.Strings(plan.Targets)
			if !reflect.DeepEqual(plan.Targets, tc.want) || !reflect.DeepEqual(record.Delivered, tc.want) || !reflect.DeepEqual(record.Called, tc.called) || routes != tc.routes {
				t.Fatalf("targets=%v record=%+v routing calls=%d", plan.Targets, record, routes)
			}
			if tc.routes == 0 && len(plan.Heard) != 0 {
				t.Fatalf("directed entry matched person keywords: %v", plan.Heard)
			}
			if tc.name == "mention overrides reply" && (plan.ReplySpeaker != "" || plan.Asked != "") {
				t.Fatalf("overridden reply target retained: %+v", plan)
			}
		})
	}
}

// A reply under a persona's remark reaches only that persona, and the record
// says it was called by the reply without model exclusion reasons.
func TestAReplyIsRecordedAsHowThePersonaWasCalled(t *testing.T) {
	replied := false
	runner, s, _ := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "明示的に求めているか"):
			return `{"escalate":false}`
		case strings.Contains(system, "振り分け") && !replied:
			return `{"deliver":["gourmet"]}`
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["move"],"excluded_reasons":{"gourmet":"a","book":"c"}}`
		}
		return `{"action":"speak","text":"うん"}`
	}, "gourmet", "move", "book")
	first := post(t, s, "ラーメン食べたい")
	runner.react(first.ID)
	answer := remarks(t, s)[0]
	replied = true
	reply := replyTo(t, s, answer, "ありがとう")
	runner.react(reply.ID)

	got, err := s.Routing(reply.ID)
	if err != nil || got == nil {
		t.Fatalf("routing = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(got.Called, map[string]string{"gourmet": "reply"}) || !reflect.DeepEqual(got.Delivered, []string{"gourmet"}) {
		t.Fatalf("kept %+v", got)
	}
	if len(got.Excluded) != 0 {
		t.Fatalf("excluded = %v", got.Excluded)
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
	runner := New(slow, NewCatalogue(s), nil)

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

// The answer and the operations are handed in apart. No operation speaks, and
// the answer is held to the turn: named, it must say something; listening, it
// must not.
func TestAnswerAndOperationsAreIndependent(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	for _, wf := range []operationBatch{{Steps: []operation{}}, {Steps: []operation{{Type: "cancel_schedule", ID: "s_1"}}}} {
		if problems := checkBatch(wf, turnRules{set: tools.New(tools.Config{}).WithSchedules(nil).For([]string{"schedule"}), schedules: map[string]bool{"s_1": true}}); len(problems) > 0 {
			t.Fatal(problems)
		}
	}
	if problems := checkBatch(operationBatch{Steps: []operation{{Type: "speak", Note: "hello"}}}, turnRules{set: set}); len(problems) == 0 {
		t.Fatal("an operation spoke")
	}
	if problems := checkAnswer(Answer{Text: "hello"}, turnRules{named: true}); len(problems) > 0 {
		t.Fatal(problems)
	}
	if problems := checkAnswer(Answer{}, turnRules{named: true}); len(problems) == 0 {
		t.Fatal("named answer was empty")
	}
	if problems := checkAnswer(Answer{Text: "hello"}, turnRules{listening: true}); len(problems) == 0 {
		t.Fatal("listening turn spoke")
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
	runner, s, f := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["move"]}`
		}
		return `{"action":"speak","text":"ノートンの白ですね"}`
	}, "move")
	f.keep = func(string, string) string {
		return `{"summary":"ワインのラベルを見せた","tags":["ワイン"],"facts":[{"op":"add","field":"Place","id":"","ids":[],"text":"ワイン屋。"}]}`
	}
	runner.react(post(t, s, "ワインのラベル").ID)
	said := remarks(t, s)
	if len(said) != 1 || len(said[0].Notes) != 1 || said[0].Notes[0] != "Place.md" {
		t.Fatalf("remarks = %+v", said)
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

// A speak may cite what this turn's searches found and nothing else. A stray
// id, such as the post being answered, is dropped rather than refused, so a
// long answer is not regenerated for metadata.
func TestSpeakSourcesMustComeFromThisTurnsSearches(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	speak := func(ids ...string) Answer {
		return Answer{Text: "ひとこと", Sources: ids}
	}
	found := turnRules{set: set, cited: map[string]string{"old": "2026-09-12 ピノ", "a": "a", "b": "b", "c": "c"}}
	for _, answer := range []Answer{speak("old"), speak("old", "made-up"), speak("old", "old", "a", "b", "c")} {
		if problems := checkAnswer(answer, found); len(problems) != 0 {
			t.Fatalf("sources refused the answer: %v", problems)
		}
	}
	if kept := citedOnly(speak(" old ", "made-up", "old"), found).Sources; !slices.Equal(kept, []string{"old"}) {
		t.Fatalf("kept = %v", kept)
	}
	if kept := citedOnly(speak("old", "a", "b", "c"), found).Sources; len(kept) != maxReplySources {
		t.Fatalf("kept beyond the limit: %v", kept)
	}
	if kept := citedOnly(speak("old"), turnRules{set: set}).Sources; kept == nil || len(kept) != 0 {
		t.Fatalf("a citation with no search behind it was kept: %v", kept)
	}
	if sources := citedSources([]string{" old ", "made-up"}, map[string]string{"old": "2026-09-12 ピノ"}); len(sources) != 1 || sources[0].URL != "/?entry=old" || sources[0].Label != "2026-09-12 ピノ" {
		t.Fatalf("sources = %+v", sources)
	}
	schema, _ := json.Marshal(answerTool.Parameters)
	if !strings.Contains(string(schema), "sources") {
		t.Fatal("answer schema omitted sources")
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
