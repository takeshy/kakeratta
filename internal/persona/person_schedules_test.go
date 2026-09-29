package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func installCalendarPerson(t *testing.T, r *Runner, skill Skill) {
	t.Helper()
	raw, _ := json.Marshal(skill)
	if _, err := r.catalogue.Install(raw, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
}

func TestBasicInformationCalendarRegistrationReconciliationAndNoNotes(t *testing.T) {
	r, s, stub := setup(t, func(string, string) string { return `{"action":"silent"}` }, "sato")
	skill := sato()
	skill.BasicInfo = "誕生日は11月10日。12月1日に会う予定。"
	skill.Tools = []string{"schedule"}
	installCalendarPerson(t, r, skill)
	stub.calendar = func(string) string {
		return `{"events":[{"key":"birthday","name":"誕生日","annual":true,"month":11,"day":10},{"key":"meeting","name":"会う予定","at":"2026-12-01T09:00:00+09:00"}]}`
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	plan, err := r.preparePersonSchedules(context.Background(), skill, skill.BasicInfo, "basicInfo", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyPersonSchedules(plan); err != nil {
		t.Fatal(err)
	}
	schedules, err := s.Schedules(skill.ID)
	if err != nil || len(schedules) != 3 {
		t.Fatalf("schedules=%+v,%v", schedules, err)
	}
	for _, schedule := range schedules {
		if schedule.PersonEvent == nil || schedule.NotePath != "" {
			t.Fatalf("calendar needs a memory note: %+v", schedule)
		}
	}
	if err := r.ApplyPersonSchedules(plan); err != nil {
		t.Fatal(err)
	}
	repeated, _ := s.Schedules(skill.ID)
	if len(repeated) != 3 {
		t.Fatalf("duplicate schedules=%+v", repeated)
	}
	// Manual schedules survive clearing basic information.
	if _, err := s.SaveSchedule(store.Schedule{Skill: skill.ID, At: now.Add(time.Hour), Note: "別の用事"}); err != nil {
		t.Fatal(err)
	}
	skill.BasicInfo = ""
	installCalendarPerson(t, r, skill)
	empty, err := r.preparePersonSchedules(context.Background(), skill, "", "basicInfo", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyPersonSchedules(empty); err != nil {
		t.Fatal(err)
	}
	left, _ := s.Schedules(skill.ID)
	if len(left) != 1 || left[0].Note != "別の用事" {
		t.Fatalf("clear left=%+v", left)
	}
	entries, _ := s.Timeline()
	paths, _ := s.NotePaths(skill.ID)
	if len(entries) != 0 || len(paths) != 0 {
		t.Fatalf("automatic posts/notes: %+v,%+v", entries, paths)
	}
}

func TestFragmentDatesAndAppointmentsAreRegisteredAndCorrected(t *testing.T) {
	r, s, stub := setup(t, func(string, string) string { return `{"action":"silent"}` }, "sato")
	skill := sato()
	skill.Tools = []string{"schedule"}
	installCalendarPerson(t, r, skill)
	stub.calendar = func(input string) string {
		day := 10
		if strings.Contains(input, "11月11日") {
			day = 11
		}
		raw, _ := json.Marshal(map[string]any{"events": []extractedPersonEvent{
			{Key: "birthday", Name: "誕生日", Annual: true, Month: 11, Day: day},
			{Key: "meeting", Name: "食事の約束", At: time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)},
		}})
		return string(raw)
	}
	entry := post(t, s, "@佐藤さん 誕生日は11月10日。明後日一緒に食事する約束。")
	r.react(entry.ID)
	schedules, err := s.Schedules(skill.ID)
	if err != nil || len(schedules) != 3 {
		t.Fatalf("fragment schedules=%+v,%v", schedules, err)
	}
	if _, err := s.UpdateText(entry.ID, "@佐藤さん 誕生日は11月11日。明後日一緒に食事する約束。"); err != nil {
		t.Fatal(err)
	}
	r.react(entry.ID)
	updated, _ := s.Schedules(skill.ID)
	if len(updated) != 3 {
		t.Fatalf("correction duplicated=%+v", updated)
	}
	for _, schedule := range updated {
		if schedule.PersonEvent.Annual && schedule.PersonEvent.Day != 11 {
			t.Fatalf("old birthday survived: %+v", schedule)
		}
	}
	// Explicit cancellation targets only the selected event.
	stub.calendar = func(string) string { return `{"events":[{"key":"meeting","cancel":true}]}` }
	cancel := post(t, s, "@佐藤さん 食事の約束は取り消し。")
	r.react(cancel.ID)
	left, _ := s.Schedules(skill.ID)
	if len(left) != 2 {
		t.Fatalf("cancel left=%+v", left)
	}
}

func TestPersonCalendarNotificationPublishesAndRecurs(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string {
		if !strings.Contains(system, "メモがないことを理由に黙らない") {
			t.Error("missing calendar notification instruction")
		}
		return `{"action":"speak","text":"もうすぐ佐藤さんの誕生日です。"}`
	}, "sato")
	skill := sato()
	skill.Tools = []string{"schedule"}
	installCalendarPerson(t, r, skill)
	now := time.Now().UTC()
	event := store.PersonEvent{Key: "birthday", Name: "誕生日", Source: "basicInfo", Annual: true, Month: int(now.Month()), Day: now.Day(), LeadDays: 7}
	if _, err := s.SaveSchedule(store.Schedule{Skill: skill.ID, At: now.Add(-time.Minute), TimeZone: "UTC", Note: "誕生日（1週間前）", PersonEvent: &event}); err != nil {
		t.Fatal(err)
	}
	r.schedulesAt(now)
	entries, _ := s.Timeline()
	if len(entries) != 1 || entries[0].Speaker != skill.ID || entries[0].Kind != timeline.KindPost {
		t.Fatalf("notification=%+v", entries)
	}
	left, _ := s.Schedules(skill.ID)
	if len(left) != 1 || !left[0].At.After(now) {
		t.Fatalf("annual recurrence=%+v", left)
	}
	r.schedulesAt(now)
	entries, _ = s.Timeline()
	if len(entries) != 1 {
		t.Fatalf("duplicate notification=%+v", entries)
	}
}

func TestPersonCalendarRejectsInvalidExtractionWithoutChangingSchedules(t *testing.T) {
	r, s, stub := setup(t, func(string, string) string { return `{"action":"silent"}` }, "sato")
	skill := sato()
	skill.BasicInfo = "誕生日は11月10日。"
	installCalendarPerson(t, r, skill)
	original, err := s.SaveSchedule(store.Schedule{Skill: skill.ID, At: time.Now().Add(time.Hour), Note: "既存の予定"})
	if err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{`{}`, `{"events":null}`, `{"events":[{"key":"birthday","name":"誕生日","annual":true,"month":2,"day":30}]}`} {
		stub.calendar = func(string) string { return response }
		if _, err := r.PreparePersonSchedules(context.Background(), skill); err == nil {
			t.Fatalf("invalid extraction accepted: %s", response)
		}
		left, _ := s.Schedules(skill.ID)
		if len(left) != 1 || left[0].ID != original.ID {
			t.Fatalf("extraction altered schedules=%+v", left)
		}
	}
}

func TestRepeatedCalendarInformationPreservesAlreadyAdvancedAnnualNotification(t *testing.T) {
	r, s, stub := setup(t, func(string, string) string { return `{"action":"silent"}` }, "sato")
	skill := sato()
	skill.BasicInfo = "誕生日は11月10日。"
	installCalendarPerson(t, r, skill)
	stub.calendar = func(string) string {
		return `{"events":[{"key":"birthday","name":"誕生日","annual":true,"month":11,"day":10}]}`
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	plan, err := r.preparePersonSchedules(context.Background(), skill, skill.BasicInfo, "basicInfo", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyPersonSchedules(plan); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Schedules(skill.ID)
	for _, schedule := range old {
		if _, claimed, err := s.ClaimSchedule(schedule.ID, schedule.At, schedule.At); err != nil || !claimed {
			t.Fatalf("claim=%v,%v", claimed, err)
		}
	}
	// Repeating the date in a fragment changes provenance without bringing back this year's reminder.
	entry := post(t, s, "@佐藤さん 誕生日は11月10日。")
	repeated, err := r.preparePersonSchedules(context.Background(), skill, entry.Text, "entry", &entry, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyPersonSchedules(repeated); err != nil {
		t.Fatal(err)
	}
	left, _ := s.Schedules(skill.ID)
	for _, schedule := range left {
		if schedule.At.Year() != 2027 {
			t.Fatalf("notification regressed=%+v", schedule)
		}
	}
}
