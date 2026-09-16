package store

import (
	"errors"
	"testing"
	"time"
)

func TestScheduleValidationAndOwnership(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for _, bad := range []Schedule{
		{Skill: "routine", At: now.Add(time.Hour)},                // no note
		{Skill: "routine", At: now.Add(-time.Minute), Note: "過去"}, // already gone
		{Skill: "routine", At: now.Add(40 * 24 * time.Hour), Note: "遠すぎる"},
		{Skill: "routine", At: now.Add(time.Hour), Note: "毎日", Daily: true}, // repeating without a zone
		{Skill: "routine", At: now.Add(time.Hour), Note: "毎日", Daily: true, TimeZone: "bad/zone"},
	} {
		if bad.Validate(now) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	good := Schedule{Skill: "routine", At: now.Add(time.Hour), Note: "朝の薬", Daily: true, TimeZone: "Asia/Tokyo"}
	if err := good.Validate(now); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(good)
	if err != nil || saved.ID == "" {
		t.Fatalf("saved = %+v (%v)", saved, err)
	}
	// A schedule belongs to the persona that asked for it.
	if mine, err := s.Schedules("routine"); err != nil || len(mine) != 1 {
		t.Fatalf("own schedules = %+v (%v)", mine, err)
	}
	if others, err := s.Schedules("gourmet"); err != nil || len(others) != 0 {
		t.Fatalf("another persona saw it: %+v (%v)", others, err)
	}
	if err := s.DeleteSchedule("gourmet", saved.ID); !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("another persona could cancel it: %v", err)
	}
	if err := s.DeleteSchedule("routine", saved.ID); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleCapIsPerSkill(t *testing.T) {
	s := open(t)
	at := time.Now().Add(time.Hour).UTC()
	for i := 0; i < MaxSchedulesPerSkill; i++ {
		if _, err := s.SaveSchedule(Schedule{Skill: "routine", At: at, Note: "用事"}); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := s.SaveSchedule(Schedule{Skill: "routine", At: at, Note: "溢れる"}); !errors.Is(err, ErrScheduleTooMany) {
		t.Fatalf("the cap did not hold: %v", err)
	}
	// The cap is one persona's, not the workspace's.
	if _, err := s.SaveSchedule(Schedule{Skill: "gourmet", At: at, Note: "別の担当"}); err != nil {
		t.Fatal(err)
	}
	// Forgetting a persona's memory takes its schedules with it.
	if err := s.DeleteSchedules("routine"); err != nil {
		t.Fatal(err)
	}
	if left, err := s.Schedules("routine"); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
	if kept, err := s.Schedules("gourmet"); err != nil || len(kept) != 1 {
		t.Fatalf("another persona's schedule was cleared: %+v (%v)", kept, err)
	}
}

func TestScheduleClaimFiresOnceAndRepeatsDaily(t *testing.T) {
	s := open(t)
	// 07:30 in Tokyo.
	at := time.Date(2026, 9, 16, 22, 30, 0, 0, time.UTC)
	daily, err := s.SaveSchedule(Schedule{Skill: "routine", At: at, Note: "朝の薬", Daily: true, TimeZone: "Asia/Tokyo"})
	if err != nil {
		t.Fatal(err)
	}
	once, err := s.SaveSchedule(Schedule{Skill: "routine", At: at, Note: "花を買う"})
	if err != nil {
		t.Fatal(err)
	}
	now := at.Add(time.Minute)
	if fired, claimed, err := s.ClaimSchedule(once.ID, at, now); err != nil || !claimed || fired.Note != "花を買う" {
		t.Fatalf("one-off claim = %+v %v %v", fired, claimed, err)
	}
	if _, claimed, _ := s.ClaimSchedule(once.ID, at, now); claimed {
		t.Fatal("a one-off schedule fired twice")
	}
	if _, claimed, err := s.ClaimSchedule(daily.ID, at, now); err != nil || !claimed {
		t.Fatalf("daily claim = %v %v", claimed, err)
	}
	left, err := s.Schedules("routine")
	if err != nil || len(left) != 1 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	next := left[0].At.In(tokyo)
	if next.Format("15:04") != "07:30" || next.Day() != at.In(tokyo).Day()+1 {
		t.Fatalf("next fire = %s", next)
	}
	// The advanced schedule is a different fire, so the old instant cannot run again.
	if _, claimed, _ := s.ClaimSchedule(daily.ID, at, now); claimed {
		t.Fatal("the same daily fire ran twice")
	}
}
