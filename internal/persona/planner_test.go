package persona

import (
	"context"
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
func TestAnExternalPlannerWorkflowRunsAsThePersona(t *testing.T) {
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
	case !strings.Contains(brief.Skill, "メモの書き方"):
		t.Fatal("the note-writing skill is missing")
	case brief.Trigger.ID != entry.ID || len(brief.Thread) != 1:
		t.Fatalf("trigger/thread = %+v / %+v", brief.Trigger, brief.Thread)
	case brief.MemoryIndex != "# tabe の索引":
		t.Fatalf("index = %q", brief.MemoryIndex)
	case brief.Workflow.Schema == nil || !strings.Contains(brief.Workflow.Spec, "speak"):
		t.Fatal("the workflow schema is missing")
	case !strings.HasPrefix(brief.Briefing.System, "あなたは「tabe」"):
		t.Fatalf("briefing = %q", brief.Briefing.System)
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
	// A remark on a post leaves a note behind: this one is sent back.
	verdict, err := runner.SubmitAskWorkflow(ctx, ask.ID, "exec-1", &Workflow{Summary: "言う", Steps: []Step{{Type: "speak", Text: "うまそう"}}}, nil)
	if err != nil || verdict.Accepted || verdict.Closed || len(verdict.Problems) == 0 {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}
	verdict, err = runner.SubmitAskWorkflow(ctx, ask.ID, "exec-1", &Workflow{Summary: "覚えて言う", Steps: []Step{
		{Type: "write_note", Path: "ramen.md", Text: "---\ntype: Preference\ntitle: ラーメン\n---\n食べた。"},
		{Type: "speak", Text: "うまそう"},
	}}, nil)
	if err != nil || !verdict.Accepted || !verdict.Closed {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}

	var remark *timeline.Entry
	eventually(t, "the remark", func() bool { remark = remarkBy(s, "gourmet"); return remark != nil })
	if remark.Text != "うまそう" || remark.Model != "agent" {
		t.Fatalf("remark = %+v", remark)
	}
	if _, ok := s.Note("gourmet", "ramen.md"); !ok {
		t.Fatal("the workflow's note was not written")
	}
	eventually(t, "the turn to finish", func() bool {
		record, _ := s.Routing(entry.ID)
		return record != nil && contains(record.Done, "gourmet") && contains(record.Spoke, "gourmet")
	})
	if stub.promptFor("tabe") != "" {
		t.Fatal("the in-process planner was asked although the agent planned")
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
func TestAClaimedAskWithoutAWorkflowFallsThrough(t *testing.T) {
	runner, s, _ := externalSetup(t, time.Minute, 30*time.Millisecond)
	entry := post(t, s, "ラーメン食べた")
	runner.react(entry.ID)
	ask := openAskOf(t, runner)
	if _, _, err := runner.ClaimAsk(ask.ID, "exec"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the in-process remark", func() bool { return remarkBy(s, "gourmet") != nil })
	_, err := runner.SubmitAskWorkflow(context.Background(), ask.ID, "exec", &Workflow{Summary: "黙る", Steps: []Step{{Type: "speak"}}}, nil)
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
	settings.DailyCheckIns = map[string]store.DailyCheckIn{"gourmet": {Enabled: true, Time: "08:00", TimeZone: "Asia/Tokyo"}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { runner.dailyAt(time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC)); close(done) }()
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
	if runner.detaches(context.Background(), Skill{ID: "gourmet"}, settings) {
		t.Fatal("a paused agent was handed the turn")
	}
	if settings.Planners["gourmet"] != store.PlannerExternal {
		t.Fatal("the choice was lost")
	}
}
