package persona

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

func TestScheduleTimelineUsesTheUsersLocalDayAndIncludesRemarks(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	zone, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 0, 30, 0, 0, zone)
	yesterday := timeline.Entry{ID: "e_yesterday", Kind: timeline.KindPost, Speaker: timeline.User, Text: "前日の記録", At: now.Add(-time.Hour)}
	yesterday.Root = yesterday.ID
	today := timeline.Entry{ID: "e_today", Kind: timeline.KindPost, Speaker: timeline.User, Text: "朝の薬飲んだ", At: now.Add(-20 * time.Minute)}
	today.Root = today.ID
	plana := timeline.Entry{ID: "e_plana", Kind: timeline.KindRemark, Speaker: "routine", Text: "花、買った？", At: now.Add(-10 * time.Minute), Root: today.ID}
	for _, entry := range []timeline.Entry{yesterday, today, plana} {
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	got := r.scheduleTimeline(context.Background(), now, zone)
	for _, want := range []string{"00:10 user:\n朝の薬飲んだ", "00:20 plana:\n花、買った？"} {
		if !strings.Contains(got, want) {
			t.Fatalf("schedule timeline lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "前日の記録") {
		t.Fatalf("schedule timeline crossed the user's midnight:\n%s", got)
	}
}

func TestScheduleTimelineMarksAClippedDay(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for i := range 31 {
		entry := timeline.Entry{
			ID: fmt.Sprintf("e_%02d", i), Kind: timeline.KindPost, Speaker: timeline.User,
			Text: fmt.Sprintf("記録%d", i), At: now.Add(time.Duration(i-31) * time.Minute),
		}
		entry.Root = entry.ID
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.scheduleTimeline(context.Background(), now, time.UTC); !strings.Contains(got, "今日のかけらは一部省略されています") {
		t.Fatalf("clipped timeline was not marked:\n%s", got)
	}
}

func TestOnlyScheduleWakesReceiveTodaysTimeline(t *testing.T) {
	var schedulePrompt string
	calls := 0
	r, s, _ := setup(t, func(system, user string) string {
		calls++
		schedulePrompt = system + "\n" + user
		return `{"action":"speak","text":"そういえば朝の薬"}`
	}, "routine")
	now := time.Now()
	entry := timeline.Entry{ID: "e_today", Kind: timeline.KindPost, Speaker: timeline.User, Text: "朝の記録", At: now.Add(-time.Minute)}
	entry.Root = entry.ID
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: now.Add(-time.Second).UTC(), Note: "朝の薬"}); err != nil {
		t.Fatal(err)
	}
	r.schedulesAt(context.Background(), now)
	if !strings.Contains(schedulePrompt, "## 今日のかけら") || !strings.Contains(schedulePrompt, "朝の記録") {
		t.Fatalf("schedule wake did not receive today's timeline:\n%s", schedulePrompt)
	}
	if calls != 1 {
		t.Fatalf("direct workflow took %d model rounds; want one without timeline_search", calls)
	}
	// A scheduled wake answers no post, so what it says is a post of its own.
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	var said []timeline.Entry
	for _, entry := range entries {
		if entry.Speaker == "routine" {
			said = append(said, entry)
		}
	}
	if len(said) != 1 || said[0].Text != "そういえば朝の薬" {
		t.Fatalf("direct workflow was not executed: %+v", said)
	}

	var ordinaryPrompt string
	ordinaryRunner, ordinaryStore, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		ordinaryPrompt = system + "\n" + user
		return `{"action":"silent"}`
	}, "routine")
	ordinaryRunner.react(post(t, ordinaryStore, "通常投稿").ID)
	if ordinaryPrompt == "" {
		t.Fatal("ordinary persona did not run")
	}
	if strings.Contains(ordinaryPrompt, "## 今日のかけら") {
		t.Fatalf("ordinary post received schedule timeline:\n%s", ordinaryPrompt)
	}
}

func TestAScheduleWakesItsPersonaOnce(t *testing.T) {
	calls := 0
	r, s, stub := setup(t, func(system, user string) string {
		calls++
		if !strings.Contains(system, "Schedule（自分が預けた時刻") {
			t.Error("the schedule's own instructions were missing")
		}
		if !strings.Contains(user, "花を買う") {
			t.Errorf("the note did not reach the persona:\n%s", user)
		}
		return `{"action":"silent","notes":"記録を確認した"}`
	}, "routine")

	if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(-time.Minute).UTC(), Note: "花を買う"}); err != nil {
		t.Fatal(err)
	}
	r.schedulesAt(context.Background(), time.Now())
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	// The wake is claimed before the model runs, so a silent one is not repeated.
	if left, err := s.Schedules(""); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
	r.schedulesAt(context.Background(), time.Now())
	if calls != 1 {
		t.Fatalf("the same schedule fired twice: calls = %d", calls)
	}
	if entries, _ := s.Timeline(); len(entries) != 0 {
		t.Fatalf("a silent wake left a post: %+v", entries)
	}
	if !stub.seen("notes_read") {
		t.Fatal("the persona was not given its notes")
	}
}

// Coming back from downtime should not spend the morning saying yesterday.
func TestAScheduleNobodyCouldRunInTimeIsDropped(t *testing.T) {
	calls := 0
	r, s, _ := setup(t, func(string, string) string {
		calls++
		return `{"action":"silent"}`
	}, "routine")

	if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(-2 * time.Hour).UTC(), Note: "昨日の分"}); err != nil {
		t.Fatal(err)
	}
	r.schedulesAt(context.Background(), time.Now())
	if calls != 0 {
		t.Fatalf("a stale schedule woke its persona: calls = %d", calls)
	}
	if left, err := s.Schedules(""); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
}

// A persona that was paused keeps its notes, not its wakes.
func TestSchedulesOfAPausedPersonaAreForgotten(t *testing.T) {
	calls := 0
	r, s, _ := setup(t, func(string, string) string {
		calls++
		return `{"action":"silent"}`
	})

	for _, note := range []string{"止めた担当", "もうひとつ"} {
		if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(-time.Minute).UTC(), Note: note}); err != nil {
			t.Fatal(err)
		}
	}
	r.schedulesAt(context.Background(), time.Now())
	if calls != 0 {
		t.Fatalf("a paused persona woke: calls = %d", calls)
	}
	if left, err := s.Schedules(""); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
}

// Deleting several notes in a row is one wake, and the persona is told every
// path so it can fix its own memory and schedules.
func TestForgottenNotesWakeThePersonaOnce(t *testing.T) {
	calls := 0
	var seen string
	r, _, _ := setup(t, func(system, user string) string {
		calls++
		seen = system + "\n" + user
		return `{"action":"silent"}`
	}, "routine")

	r.NoteForgotten("routine", "errands/flowers.md")
	r.NoteForgotten("routine", "routines/medicine.md")
	pending := wakes(t, r)
	queued := len(pending)
	var gone forgotten
	if queued == 1 && pending[0].forgotten != nil {
		gone = *pending[0].forgotten
	}
	if queued != 1 {
		t.Fatalf("queued %d wakes for two deletions", queued)
	}
	if len(gone.paths) != 2 {
		t.Fatalf("paths = %v", gone.paths)
	}

	r.forgottenAt(context.Background(), gone)
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	for _, want := range []string{"メモが消されました", "errands/flowers.md", "routines/medicine.md"} {
		if !strings.Contains(seen, want) {
			t.Fatalf("the persona was not told %q:\n%s", want, seen)
		}
	}
}

func TestScheduleContextOnlyPassesOwnSchedulesAndReadsOthersWithTool(t *testing.T) {
	r, workspace, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	own, err := workspace.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(time.Hour), Note: "朝の薬", NotePath: "own.md"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := workspace.SaveSchedule(store.Schedule{Skill: "health", At: time.Now().Add(2 * time.Hour), Note: "診察", NotePath: "private.md"})
	if err != nil {
		t.Fatal(err)
	}
	set := tools.New(tools.Config{}).WithSchedules(workspace).WithPersonaNames(map[string]string{"routine": "プラナ", "health": "健康担当"}).For([]string{"schedule"}).As("routine")
	listed, ids := r.scheduleListFor(Skill{ID: "routine"}, set, map[string]string{"routine": "プラナ", "health": "健康担当"})
	for _, want := range []string{own.ID, "担当: プラナ（routine）", "朝の薬", "own.md"} {
		if !strings.Contains(listed, want) {
			t.Fatalf("missing %q in context: %s", want, listed)
		}
	}
	fallback, _ := r.scheduleListFor(Skill{ID: "routine"}, set, nil)
	if !strings.Contains(fallback, "担当: routine") {
		t.Fatalf("missing ID fallback: %s", fallback)
	}
	if strings.Contains(listed, "private.md") || strings.Contains(listed, other.ID) || strings.Contains(listed, "診察") {
		t.Fatalf("other persona's memory path leaked: %s", listed)
	}
	if _, exists := ids[other.ID]; !exists || ids[other.ID] || !ids[own.ID] {
		t.Fatalf("ownership = %+v", ids)
	}
	result, _ := r.readAll(context.Background(), Skill{ID: "routine"}, []ReadStep{{Type: "tool", Tool: "schedules_list", Args: `{"skill":"health"}`}}, set, timeline.Entry{}, newProvenance(), map[string]string{}, newLookups(10, 2), map[string]string{})
	if !strings.Contains(result, "健康担当（health）") || !strings.Contains(result, "診察") || strings.Contains(result, "private.md") {
		t.Fatalf("tool read = %s", result)
	}
	offered := false
	for _, tool := range askTools(set) {
		if tool.Name == "schedules_list" && tool.Use == "read" {
			offered = true
		}
	}
	if !offered {
		t.Fatal("external agent was not offered schedules_list")
	}
	empty, _ := r.scheduleListFor(Skill{ID: "absent"}, set, nil)
	if empty != "（ありません）" {
		t.Fatalf("other schedules entered empty own list: %s", empty)
	}
	rules := turnRules{set: set, schedules: ids}
	if problems := checkBatch(operationBatch{Steps: []operation{{Type: "cancel_schedule", ID: own.ID}}}, rules); len(problems) != 0 {
		t.Fatalf("own cancellation: %v", problems)
	}
	if problems := checkBatch(operationBatch{Steps: []operation{{Type: "cancel_schedule", ID: other.ID}}}, rules); len(problems) == 0 {
		t.Fatal("other persona's cancellation accepted")
	}
	if problems := checkBatch(operationBatch{Steps: []operation{{Type: "write_note", Path: "reminder.md", Text: "既存の予定 " + other.ID}}}, rules); len(problems) != 0 {
		t.Fatalf("visible schedule reference rejected: %v", problems)
	}
}
