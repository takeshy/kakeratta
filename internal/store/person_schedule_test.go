package store

import (
	"testing"
	"time"
)

func TestPersonAnnualSchedulesKeepCalendarDatesAndAdvanceOnce(t *testing.T) {
	zone, _ := time.LoadLocation("Asia/Tokyo")
	event := PersonEvent{Key: "birthday", Name: "誕生日", Source: "basicInfo", Annual: true, Month: 2, Day: 29, LeadDays: 7}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 30, 12, 0, 0, 0, zone)
	at := event.NextAt(from, zone, true)
	if want := time.Date(2027, 2, 21, 9, 0, 0, 0, zone); !at.Equal(want) {
		t.Fatalf("next=%v,want=%v", at, want)
	}
	schedule := Schedule{Skill: "sato", At: at, TimeZone: zone.String(), Note: "誕生日", PersonEvent: &event}
	if err := schedule.Validate(from); err != nil {
		t.Fatal(err)
	} // More than 30 days away.
	s := open(t)
	saved, err := s.SaveSchedule(schedule)
	if err != nil {
		t.Fatal(err)
	}
	fired, claimed, err := s.ClaimSchedule(saved.ID, at, at)
	if err != nil || !claimed || fired.PersonEvent == nil {
		t.Fatalf("claim=%+v,%v,%v", fired, claimed, err)
	}
	left, err := s.Schedules("sato")
	if err != nil || len(left) != 1 {
		t.Fatalf("left=%+v,%v", left, err)
	}
	want := time.Date(2028, 2, 22, 9, 0, 0, 0, zone)
	if !left[0].At.Equal(want) {
		t.Fatalf("leap-year advance=%v,want=%v", left[0].At, want)
	}
	if _, claimed, err := s.ClaimSchedule(saved.ID, at, at); err != nil || claimed {
		t.Fatalf("duplicate claim=%v,%v", claimed, err)
	}
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Schedules("sato")
	if err != nil || len(restored) != 1 || restored[0].PersonEvent.Day != 29 {
		t.Fatalf("restored=%+v,%v", restored, err)
	}
}

func TestNewlyLearnedAnniversaryInsideOneWeekNotifiesSoon(t *testing.T) {
	zone := time.UTC
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, zone)
	event := PersonEvent{Key: "birthday", Name: "誕生日", Source: "entry", Annual: true, Month: 11, Day: 10, LeadDays: 7}
	if got := event.NextAt(now, zone, true); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("imminent=%v", got)
	}
	if got := event.NextAt(now, zone, false); got.Year() != 2027 {
		t.Fatalf("recurrence=%v", got)
	}
	event.Month, event.Day = 13, 1
	if err := event.Validate(); err == nil {
		t.Fatal("invalid calendar date accepted")
	}
}
