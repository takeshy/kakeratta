package store

import (
	"errors"
	"testing"
	"time"
)

func scheduleEditContract(t *testing.T, s Workspace) {
	now := time.Now().UTC().Truncate(time.Second)
	initial := Schedule{Skill: "routine", At: now.Add(time.Hour), Daily: true, UserRequested: true, Note: "digest", TimeZone: "Asia/Tokyo"}
	saved, err := s.SaveSchedule(initial)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.NotificationsEnabled() {
		t.Fatal("legacy schedule must notify")
	}
	off := false
	next := now.Add(2 * time.Hour)
	if _, err := s.UpdateSchedule("other", saved.ID, SchedulePatch{Notify: &off}); !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	past := now.Add(-time.Hour)
	if _, err := s.UpdateSchedule(saved.Skill, saved.ID, SchedulePatch{At: &past, Notify: &off}); !errors.Is(err, ErrScheduleTime) {
		t.Fatalf("past time: %v", err)
	}
	unchanged, _ := s.Schedules(saved.Skill)
	if !unchanged[0].NotificationsEnabled() {
		t.Fatal("failed update changed notification")
	}
	updated, err := s.UpdateSchedule(saved.Skill, saved.ID, SchedulePatch{At: &next, Notify: &off})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != saved.ID || !updated.At.Equal(next) || updated.NotificationsEnabled() || !updated.Daily || !updated.UserRequested || updated.Note != saved.Note || updated.TimeZone != saved.TimeZone {
		t.Fatalf("lost attributes: %+v", updated)
	}
	if _, ok, err := s.ClaimSchedule(saved.ID, saved.At, next); err != nil || ok {
		t.Fatalf("stale claim succeeded: %v %v", ok, err)
	}
	fired, ok, err := s.ClaimSchedule(saved.ID, next, next)
	if err != nil || !ok || fired.NotificationsEnabled() {
		t.Fatalf("claim: %+v %v %v", fired, ok, err)
	}
	recurring, _ := s.Schedules(saved.Skill)
	if len(recurring) != 1 || recurring[0].NotificationsEnabled() || !recurring[0].At.Equal(next.AddDate(0, 0, 1)) {
		t.Fatalf("recurrence: %+v", recurring)
	}
	once, err := s.SaveSchedule(Schedule{Skill: "routine", At: next, Note: "once"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimSchedule(once.ID, next, next); err != nil || !ok {
		t.Fatal("cannot claim one-off")
	}
	if _, err := s.UpdateSchedule(once.Skill, once.ID, SchedulePatch{Notify: &off}); !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("recreated fired schedule: %v", err)
	}
}
func TestScheduleEdits(t *testing.T) { scheduleEditContract(t, open(t)) }
func TestFirestoreScheduleEdits(t *testing.T) {
	_, s := firestoreWorkspace(t)
	scheduleEditContract(t, s)
}

func TestAnnualEditedClockSurvivesRecurrence(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Tokyo")
	at := time.Date(2030, 11, 10, 18, 30, 0, 0, zone)
	s := Schedule{At: at, TimeZone: zone.String(), TimeEdited: true, PersonEvent: &PersonEvent{Key: "birthday", Name: "birthday", Source: "basicInfo", Annual: true, Month: 11, Day: 10}}
	next, ok := s.Next(at)
	want := time.Date(2031, 11, 10, 18, 30, 0, 0, zone)
	if !ok || !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	// A notification later today is not advanced a whole year because 09:00 passed.
	next, _ = s.Next(at.Add(-time.Hour))
	if !next.Equal(at) {
		t.Fatalf("skipped today: %v", next)
	}
}

func TestCalendarSyncPreservesConcurrentEdits(t *testing.T) {
	s := open(t)
	original := Schedule{ID: "s_birthday", Skill: "person", At: time.Now().Add(24 * time.Hour).UTC(), Note: "birthday", TimeZone: "UTC", PersonEvent: &PersonEvent{Key: "birthday", Name: "birthday", Source: "basicInfo", Annual: true, Month: 11, Day: 10}}
	if _, err := s.SaveSchedule(original); err != nil {
		t.Fatal(err)
	}
	off := false
	later := original.At.Add(time.Hour)
	if _, err := s.UpdateSchedule(original.Skill, original.ID, SchedulePatch{At: &later, Notify: &off}); err != nil {
		t.Fatal(err)
	}
	// A previously prepared extraction must merge with the latest persisted edit.
	saved, err := s.SaveSchedule(original)
	if err != nil || saved.NotificationsEnabled() || !saved.At.Equal(later) || !saved.TimeEdited {
		t.Fatalf("sync reset edit: %+v %v", saved, err)
	}
}
