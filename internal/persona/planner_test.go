package persona

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// externalSetup is tabe with its planning handed to an external agent, and
// the in-process planner ready to answer "内蔵" should the turn fall through.
func externalSetup(t *testing.T, claim, plan time.Duration) (*Runner, *store.Store, *fake) {
	t.Helper()
	runner, s, stub := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet"]}`
		}
		return `{"action":"speak","text":"内蔵"}`
	}, "gourmet")
	settings := s.ConfiguredSettings()
	settings.Planners = map[string]string{"gourmet": store.PlannerExternal}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	runner.waitsForAsk = func() (time.Duration, time.Duration) { return claim, plan }
	// The agent has just called in, as its Wake Gate does every minute.
	runner.AgentSeen(time.Now())
	t.Cleanup(runner.Stop)
	return runner, s, stub
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func openAskOf(t *testing.T, runner *Runner) Ask {
	t.Helper()
	var asks []Ask
	eventually(t, "an ask", func() bool { asks = runner.Asks(); return len(asks) > 0 })
	return asks[0]
}

func remarkBy(s *store.Store, speaker string) *timeline.Entry {
	entries, _ := s.Timeline()
	for _, entry := range entries {
		if entry.Kind == timeline.KindRemark && entry.Speaker == speaker {
			return &entry
		}
	}
	return nil
}

// The agent reads the context the in-process planner would have read, hands
// in a workflow, and the app checks it and runs it as the persona. The queue
// is not held while the agent thinks, and the routing sheet keeps the turn
// open until it has really ended.
func TestAnExternalAnswerRunsThroughTheBuiltinPhases(t *testing.T) {
	runner, s, stub := externalSetup(t, time.Minute, time.Minute)
	if err := s.WriteNote("gourmet", "index.md", "# tabe の索引"); err != nil {
		t.Fatal(err)
	}
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)

	ask := openAskOf(t, runner)
	if ask.Persona != "gourmet" || ask.Trigger != entry.ID || ask.State != "pending" || ask.Wake != "post" {
		t.Fatalf("ask = %+v", ask)
	}
	if record, _ := s.Routing(entry.ID); record == nil || contains(record.Done, "gourmet") {
		t.Fatalf("the turn must stay open while the agent plans: %+v", record)
	}
	claimed, brief, err := runner.ClaimAsk(ask.ID, "exec-1")
	if err != nil || claimed.State != "claimed" || claimed.ClaimedAt == nil || claimed.PlanBy == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, _, err := runner.ClaimAsk(ask.ID, "exec-2"); !errors.Is(err, ErrAskClaimed) {
		t.Fatalf("another claimId took the ask: %v", err)
	}
	if _, _, err := runner.ClaimAsk(ask.ID, "exec-1"); err != nil {
		t.Fatalf("the same claimId could not claim again: %v", err)
	}
	switch {
	case brief.Persona.ID != "gourmet" || brief.Persona.Name != "tabe" || brief.Instructions == "":
		t.Fatalf("identity = %+v", brief.Persona)
	case brief.Trigger.ID != entry.ID || len(brief.Thread) != 1:
		t.Fatalf("trigger/thread = %+v / %+v", brief.Trigger, brief.Thread)
	case brief.MemoryIndex != "# tabe の索引":
		t.Fatalf("index = %q", brief.MemoryIndex)
	case brief.Answer == nil:
		t.Fatal("the answer schema is missing")
	case brief.Capabilities == "":
		t.Fatal("the agent was not told what the persona can change")
	case !strings.HasPrefix(brief.Rules, "規則:"):
		t.Fatalf("rules = %q", brief.Rules)
	// The agent is not held to a short remark, as the built-in model is.
	case !strings.Contains(brief.Rules, agentRule) || strings.Contains(brief.Rules, briefRule):
		t.Fatal("the agent was told to be brief")

	}
	tools := map[string]string{}
	for _, tool := range brief.Tools {
		tools[tool.Name] = tool.Use
	}
	if tools["web_search"] != "read" || tools["notes_read"] != "read" || tools["timeline_search"] != "read" {
		t.Fatalf("tools = %+v", brief.Tools)
	}

	ctx := context.Background()
	if text, err := runner.ReadForAsk(ctx, ask.ID, "exec-1", []ReadStep{{Type: "notes_read", Paths: []string{"index.md"}}}); err != nil || !strings.Contains(text, "tabe の索引") {
		t.Fatalf("read = %q, %v", text, err)
	}
	if _, err := runner.ReadForAsk(ctx, ask.ID, "exec-2", nil); !errors.Is(err, ErrAskNotYours) {
		t.Fatalf("another claimId read: %v", err)
	}
	// The agent submits only an answer; no note or workflow is required.
	verdict, err := runner.SubmitAskAnswer(ctx, ask.ID, "exec-1", &Answer{Text: "うまそう", Sources: []string{}}, nil)
	if err != nil || !verdict.Accepted || !verdict.Closed {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}

	var remark *timeline.Entry
	eventually(t, "the remark", func() bool { remark = remarkBy(s, "gourmet"); return remark != nil })
	if remark.Text != "うまそう" || remark.Model != "agent" {
		t.Fatalf("remark = %+v", remark)
	}
	if len(s.Memory("gourmet").Notes) > 1 {
		t.Fatal("answer-only turn fabricated notes")
	}
	eventually(t, "the turn to finish", func() bool {
		record, _ := s.Routing(entry.ID)
		return record != nil && contains(record.Done, "gourmet") && contains(record.Spoke, "gourmet")
	})
	if !strings.Contains(strings.Join(stub.phases, "\n"), "submit_note") {
		t.Fatal("the builtin note phase did not run")
	}
	if len(runner.Asks()) != 0 {
		t.Fatal("the ask stayed open")
	}
}

// Nobody claims it: the in-process planner takes the turn.
func TestAnUnclaimedAskFallsThroughToTheBuiltinPlanner(t *testing.T) {
	runner, s, _ := externalSetup(t, 20*time.Millisecond, time.Minute)
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)
	var remark *timeline.Entry
	eventually(t, "the in-process remark", func() bool { remark = remarkBy(s, "gourmet"); return remark != nil })
	if remark.Text != "内蔵" || remark.Model == "agent" {
		t.Fatalf("remark = %+v", remark)
	}
	if len(runner.Asks()) != 0 {
		t.Fatal("the ask stayed open")
	}
}

// Claimed, but no workflow in time: the in-process planner takes the turn,
// and the late workflow has nowhere to go.
func TestAClaimedAskWithoutAnAnswerFallsThrough(t *testing.T) {
	runner, s, _ := externalSetup(t, time.Minute, 30*time.Millisecond)
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)
	ask := openAskOf(t, runner)
	if _, _, err := runner.ClaimAsk(ask.ID, "exec"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the in-process remark", func() bool { return remarkBy(s, "gourmet") != nil })
	_, err := runner.SubmitAskAnswer(context.Background(), ask.ID, "exec", &Answer{}, nil)
	if !errors.Is(err, ErrAskNotFound) {
		t.Fatalf("late workflow = %v", err)
	}
}

// An agent that cannot plan gives the ask back and does not make the persona
// wait out the deadline.
func TestAReleasedAskFallsThroughAtOnce(t *testing.T) {
	runner, s, _ := externalSetup(t, time.Minute, time.Hour)
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)
	ask := openAskOf(t, runner)
	if _, _, err := runner.ClaimAsk(ask.ID, "exec"); err != nil {
		t.Fatal(err)
	}
	if err := runner.ReleaseAsk(context.Background(), ask.ID, "exec", "no tools"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the in-process remark", func() bool { return remarkBy(s, "gourmet") != nil })
}

// The daily check-in waits on the agent apart from the queue as well.
func TestACheckInIsPlannedExternallyWithoutHoldingTheQueue(t *testing.T) {
	runner, s, _ := externalSetup(t, time.Minute, time.Minute)
	settings := s.ConfiguredSettings()
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		runner.dailyAt(store.CheckInCloses(checkInDay(s, time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), "gourmet")).Add(-time.Minute))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the check-in held the queue while waiting on the agent")
	}
	ask := openAskOf(t, runner)
	if ask.Wake != "daily" || ask.Trigger != "" {
		t.Fatalf("ask = %+v", ask)
	}
	_, brief, err := runner.ClaimAsk(ask.ID, "exec")
	if err != nil || !strings.Contains(brief.WakeNote, dailyHeader) {
		t.Fatalf("check-in brief = %q, %v", brief.WakeNote, err)
	}
}

// A preview a person is waiting on is planned in-process.
func TestAPreviewIsPlannedInProcess(t *testing.T) {
	settings := store.Settings{Planners: map[string]string{"gourmet": store.PlannerExternal}}
	runner := New(nil, nil, nil)
	runner.AgentSeen(time.Now())
	skill := Skill{ID: "gourmet"}
	if !runner.detaches(context.Background(), skill, settings) {
		t.Fatal("an external planner's turn must run apart")
	}
	if runner.detaches(withBuiltinPlanner(context.Background()), skill, settings) {
		t.Fatal("a preview was handed to the agent")
	}
}

// Paused, the agent is set aside without forgetting who it planned for.
func TestAPausedExternalAgentPlansNothing(t *testing.T) {
	settings := store.Settings{Planners: map[string]string{"gourmet": store.PlannerExternal}}
	settings.ExternalAgent.Paused = true
	runner := New(nil, nil, nil)
	runner.AgentSeen(time.Now())
	if runner.detaches(context.Background(), Skill{ID: "gourmet"}, settings) {
		t.Fatal("a paused agent was handed the turn")
	}
	if settings.Planners["gourmet"] != store.PlannerExternal {
		t.Fatal("the choice was lost")
	}
}

// An agent that has not called in for the claim wait is set aside without
// anyone switching it off, and comes back the moment it calls in again.
func TestAQuietAgentIsSetAsideUntilItCallsIn(t *testing.T) {
	settings := store.Settings{Planners: map[string]string{"gourmet": store.PlannerExternal}}
	settings.ExternalAgent.ClaimMinutes = 3
	runner := New(nil, nil, nil)
	skill := Skill{ID: "gourmet"}
	if !runner.AgentAway(settings) || runner.detaches(context.Background(), skill, settings) {
		t.Fatal("an agent that never called in was handed the turn")
	}
	runner.AgentSeen(time.Now().Add(-4 * time.Minute))
	if !runner.AgentAway(settings) || runner.detaches(context.Background(), skill, settings) {
		t.Fatal("an agent silent past the claim wait was handed the turn")
	}
	runner.AgentSeen(time.Now().Add(-2 * time.Minute))
	if runner.AgentAway(settings) || !runner.detaches(context.Background(), skill, settings) {
		t.Fatal("an agent that called in within the claim wait was set aside")
	}

	// While it works on an ask it claimed, its Wake Gate is quiet: it is not
	// away, and the next turn goes to it too.
	runner.AgentSeen(time.Now().Add(-10 * time.Minute))
	ask := &openAsk{Ask: Ask{ID: "ask_busy", State: "pending"}, planWait: time.Minute, claimed: make(chan struct{}), requests: make(chan askRequest), done: make(chan struct{})}
	runner.desk.open(ask)
	if !runner.AgentAway(settings) {
		t.Fatal("an unclaimed ask kept a silent agent present")
	}
	if _, _, err := runner.ClaimAsk("ask_busy", "claim-1"); err != nil {
		t.Fatal(err)
	}
	if runner.AgentAway(settings) || !runner.detaches(context.Background(), skill, settings) {
		t.Fatal("an agent working on a claimed ask was set aside")
	}
	runner.desk.close(ask)
	if !runner.AgentAway(settings) {
		t.Fatal("an agent silent past the claim wait stayed present after its ask ended")
	}
}

// Nobody waits out the claim for an agent that is away: the in-process
// planner answers at once and no ask is opened.
func TestAnAwayAgentOpensNoAsk(t *testing.T) {
	runner, s, _ := externalSetup(t, time.Minute, time.Minute)
	runner.agentSeen.Store(0)
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)
	if remark := remarkBy(s, "gourmet"); remark == nil || remark.Text != "内蔵" {
		t.Fatalf("remark = %+v", remark)
	}
	if len(runner.Asks()) != 0 {
		t.Fatal("an ask was opened for an agent that is away")
	}
}

// Compact claims must retain the context formerly found only in briefing,
// and preserve the whole index rather than truncate memory to save tokens.
func TestCompactAskContextPreservesTurnContext(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		named, listening, deep bool
	}{{"ordinary", false, false, false}, {"named", true, false, false}, {"listening", false, true, false}, {"deep", false, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			runner, s, _ := externalSetup(t, time.Minute, time.Minute)
			index := strings.Repeat("memory-index-unique-content\n", 500)
			if err := s.WriteNote("gourmet", "index.md", index); err != nil {
				t.Fatal(err)
			}
			entry := post(t, s, "投稿の全文")
			entry.Place = &timeline.Place{Label: "渋谷", Lat: 35.658, Lon: 139.7016}
			entry.Link = "https://example.com/article"
			entry.Media = &timeline.Media{Describe: "ページの題名", Text: "リンク先の全文"}
			client, settings := runner.clientOf()
			skill := runner.catalogue.Active(settings.Active)[0]
			wakeNote := "このターン固有の追加指示"
			runner.run(context.Background(), skill, []timeline.Entry{entry}, entry, tc.named, tc.listening, wakeNote, tc.deep,
				client, settings, runner.registry(settings))
			ask := openAskOf(t, runner)
			_, brief, err := runner.ClaimAsk(ask.ID, "compact-test")
			if err != nil {
				t.Fatal(err)
			}
			length := agentRule
			if tc.deep {
				length = deepRule
			}
			if !strings.Contains(brief.Rules, length) || strings.Contains(brief.Rules, briefRule) {
				t.Fatal("wrong length rule")
			}
			if brief.Turn != turnLine(tc.named, tc.listening) || brief.WakeNote != wakeNote {
				t.Fatal("turn instructions lost")
			}
			if brief.Place != placeLine(entry.Place) || brief.NoteCount != runner.noteCount(skill.ID) || brief.Now == "" {
				t.Fatal("location, inventory or clock lost")
			}
			if brief.MemoryIndex != index || brief.Instructions != skill.Instructions {
				t.Fatal("persona or memory context lost")
			}
			if len(brief.Thread) != 1 || !strings.Contains(brief.Thread[0].Rendered, entry.Link) || !strings.Contains(brief.Thread[0].Rendered, entry.Media.Text) {
				t.Fatal("rich post context lost")
			}
			raw, err := json.Marshal(brief)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["briefing"] != nil || fields["threadText"] != nil {
				t.Fatal("duplicate prompts returned")
			}
			// Large note indexes used to occur in both memoryIndex and briefing.user.
			if strings.Count(string(raw), "memory-index-unique-content") != 500 {
				t.Fatal("index was duplicated or truncated")
			}
			if fields["workflow"] != nil || fields["skill"] != nil {
				t.Fatal("answer agent received memory-writing instructions")
			}

		})
	}
}
