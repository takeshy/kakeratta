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
	sona := timeline.Entry{ID: "e_plana", Kind: timeline.KindRemark, Speaker: "routine", Text: "花、買った？", At: now.Add(-10 * time.Minute), Root: today.ID}
	for _, entry := range []timeline.Entry{yesterday, today, sona} {
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	got := r.scheduleTimeline(context.Background(), now, zone)
	for _, want := range []string{"00:10 user:\n朝の薬飲んだ", "00:20 sona:\n花、買った？"} {
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
	reminders := 0
	r.OnEvent(func(name string, data any) {
		if name == "reminder" {
			reminders++
		}
	})
	r.schedulesAt(context.Background(), now)
	if reminders != 1 {
		t.Fatalf("reminder events: %d", reminders)
	}

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
	// It is a reminder, which the user can mark done.
	if !said[0].Scheduled {
		t.Fatal("scheduled post was not marked as scheduled")
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
		if !strings.Contains(system, "一度だけ軽く一言") || strings.Contains(system, userScheduleInstructions) {
			t.Error("an autonomous schedule lost its reminder restrictions")
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
	if !stub.seen("あなたの記憶") {
		t.Fatal("the persona was not given its memory")
	}
}

func TestUserRequestedDailyScheduleExecutesTheTaskAndRetainsItsOrigin(t *testing.T) {
	calls := 0
	r, s, _ := setup(t, func(system, user string) string {
		calls++
		if !strings.Contains(system, userScheduleInstructions) || strings.Contains(system, "一度だけ軽く一言") {
			t.Errorf("user task received reminder restrictions:\n%s", system)
		}
		if !strings.Contains(user, "最新のAIニュースを3件まとめる") || strings.Contains(user, "Check whether it is already done") {
			t.Errorf("user task received the wrong trigger:\n%s", user)
		}
		return `{"action":"speak","text":"今日のAIニュースを3件お届けします。"}`
	}, "routine")
	skill, _ := r.catalogue.Find(context.Background(), "routine")
	set := r.registry(s.Settings()).WithZone("UTC").For([]string{"schedule"}).As("routine")
	at := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	plan := Workflow{Schedules: []ScheduleOperation{{
		Type: "create", At: at.Format(time.RFC3339), Daily: true,
		UserRequested: true, Note: "最新のAIニュースを3件まとめる",
	}}}
	steps := r.execute(context.Background(), plan.batch(), set)
	if len(steps) != 1 || steps[0].Error != "" {
		t.Fatalf("schedule creation failed: %+v", steps)
	}
	reminders := 0
	r.OnEvent(func(name string, data any) {
		if name == "reminder" {
			reminders++
		}
	})
	for day := range 2 {
		saved, err := s.Schedules("routine")
		if err != nil || len(saved) != 1 || !saved[0].UserRequested || !saved[0].Daily {
			t.Fatalf("user task origin was not persisted on day %d: %+v (%v)", day, saved, err)
		}
		listed, _ := r.scheduleListFor(skill, set, nil)
		if !strings.Contains(listed, "user_requested:true") {
			t.Fatalf("schedule context omitted its origin: %s", listed)
		}
		r.schedulesAt(context.Background(), saved[0].At.Add(time.Second))
	}
	if calls != 2 || reminders != 2 {
		t.Fatalf("daily user tasks: calls=%d reminders=%d", calls, reminders)
	}
	entries, err := s.Timeline()
	if err != nil || len(entries) != 2 {
		t.Fatalf("daily outputs were not published: %+v (%v)", entries, err)
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
	for _, want := range []string{own.ID, "担当: プラナ（routine）", "朝の薬"} {
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
	result, _ := r.readAll(context.Background(), Skill{ID: "routine"}, []ReadStep{{Type: "tool", Tool: "schedules_list", Args: `{"skill":"health"}`}}, set, timeline.Entry{}, time.UTC, map[string]string{}, newLookups(10, 2), map[string]string{}, nil)
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
	if problems := checkBatch(operationBatch{Steps: []operation{{Type: "write_schedule", At: time.Now().Add(time.Hour).Format(time.RFC3339), Note: "既存の予定 " + other.ID + " の前に"}}}, rules); len(problems) != 0 {
		t.Fatalf("visible schedule reference rejected: %v", problems)
	}
}

func TestPersonReminderInstructionsRetainScheduleWakeKind(t *testing.T) {
	if got := wakeKind(timeline.Entry{Speaker: "system"}, false, scheduleInstructions+"\nBirthday instructions"); got != "schedule" {
		t.Fatalf("wake: %s", got)
	}
	if got := wakeKind(timeline.Entry{Speaker: "system"}, false, userScheduleInstructions); got != "schedule" {
		t.Fatalf("user-requested task lost its schedule wake kind: %s", got)
	}
}

func TestSilentPushScheduleStillPublishesAndCanBeUpdatedByPersona(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"speak","text":"今日のニュースです"}` }, "routine")
	off := false
	set := r.registry(s.Settings()).WithZone("UTC").For([]string{"schedule"}).As("routine")
	at := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	plan := Workflow{Schedules: []ScheduleOperation{{Type: "create", At: at.Format(time.RFC3339), Daily: true, Notify: &off, UserRequested: true, Note: "朝のニュース"}}}
	steps := r.execute(context.Background(), plan.batch(), set)
	if len(steps) != 1 || steps[0].Error != "" {
		t.Fatalf("create: %+v", steps)
	}
	reminders := 0
	r.OnEvent(func(name string, _ any) {
		if name == "reminder" {
			reminders++
		}
	})
	saved, _ := s.Schedules("routine")
	r.schedulesAt(context.Background(), saved[0].At.Add(time.Second))
	entries, _ := s.Timeline()
	if len(entries) != 1 || reminders != 0 {
		t.Fatalf("muted schedule: entries=%d reminders=%d", len(entries), reminders)
	}
	saved, _ = s.Schedules("routine")
	if saved[0].NotificationsEnabled() {
		t.Fatal("repeat lost notify:false")
	}
	on := true
	plan = Workflow{Schedules: []ScheduleOperation{{Type: "update", ID: saved[0].ID, Notify: &on}}}
	rules := turnRules{set: set, schedules: map[string]bool{saved[0].ID: true}}
	if problems := checkBatch(plan.batch(), rules); len(problems) != 0 {
		t.Fatal(problems)
	}
	steps = r.execute(context.Background(), plan.batch(), set)
	if steps[0].Error != "" {
		t.Fatal(steps)
	}
	updated, _ := s.Schedules("routine")
	if len(updated) != 1 || updated[0].ID != saved[0].ID || !updated[0].At.Equal(saved[0].At) || !updated[0].UserRequested {
		t.Fatal("update replaced recurrence or identity")
	}
	r.schedulesAt(context.Background(), updated[0].At.Add(time.Second))
	if reminders != 1 {
		t.Fatalf("enabled schedule reminders=%d", reminders)
	}
	rules.schedules[saved[0].ID] = false
	if len(checkBatch(plan.batch(), rules)) == 0 {
		t.Fatal("another owner's update accepted")
	}
}

func TestScheduleUpdatePhaseAndPreviewKeepTheSameSchedule(t *testing.T) {
	at := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["routine"]}`
		}
		return `{"summary":"通知を止める","steps":[{"type":"update_schedule","id":"s_edit","at":"` + at.Format(time.RFC3339) + `","notify":false},{"type":"speak","text":"予定の時刻を変更し、Push通知をOFFにしました。"}]}`
	}, "routine")
	original := store.Schedule{ID: "s_edit", Skill: "routine", At: at.Add(-time.Hour), Note: "予定", Daily: true, UserRequested: true, TimeZone: "UTC"}
	if _, err := s.SaveSchedule(original); err != nil {
		t.Fatal(err)
	}
	entry := post(t, s, "@sona この予定を3時間後に変更して、通知しないで")
	preview, err := r.PreviewReply(context.Background(), entry.ID, "routine")
	if err != nil || preview.Error != "" || preview.Spoke == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	unchanged, _ := s.Schedules("routine")
	if len(unchanged) != 1 || !unchanged[0].At.Equal(original.At) || !unchanged[0].NotificationsEnabled() {
		t.Fatal("preview mutated schedule")
	}
	r.react(entry.ID)
	saved, _ := s.Schedules("routine")
	if len(saved) != 1 || saved[0].ID != "s_edit" || !saved[0].At.Equal(at) || saved[0].NotificationsEnabled() || !saved[0].Daily || !saved[0].UserRequested {
		t.Fatalf("phase update failed: %+v", saved)
	}
}
