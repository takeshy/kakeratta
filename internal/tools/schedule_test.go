package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

type fakeSchedules struct {
	stored  []store.Schedule
	deleted []string
}

func (f *fakeSchedules) Schedules(skill string) ([]store.Schedule, error) {
	var out []store.Schedule
	for _, schedule := range f.stored {
		if skill == "" || schedule.Skill == skill {
			out = append(out, schedule)
		}
	}
	return out, nil
}

func (f *fakeSchedules) SaveSchedule(schedule store.Schedule) (store.Schedule, error) {
	schedule.ID = "s_test"
	f.stored = append(f.stored, schedule)
	return schedule, nil
}

func (f *fakeSchedules) DeleteSchedule(skill, id string) error {
	f.deleted = append(f.deleted, skill+"/"+id)
	return nil
}

func scheduleSetFor(owner string, schedules Schedules) *Set {
	set := New(Config{}).WithSchedules(schedules).For([]string{"schedule"})
	if owner != "" {
		set = set.As(owner)
	}
	return set
}

func TestScheduleToolSetsListsAndCancels(t *testing.T) {
	schedules := &fakeSchedules{}
	set := scheduleSetFor("routine", schedules)
	ctx := context.Background()

	result, err := set.Run(ctx, "schedule", Args{"in_minutes": float64(90), "note": "花を買う予定だった"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules.stored) != 1 || schedules.stored[0].Skill != "routine" || schedules.stored[0].Note != "花を買う予定だった" {
		t.Fatalf("stored = %+v", schedules.stored)
	}
	if ahead := time.Until(schedules.stored[0].At); ahead < 85*time.Minute || ahead > 95*time.Minute {
		t.Fatalf("in_minutes landed at %s", schedules.stored[0].At)
	}
	if !strings.Contains(result.Text, "s_test") {
		t.Fatalf("the persona was not told the id: %s", result.Text)
	}

	// A repeating wake has to keep its local clock time, so it needs a real zone.
	if _, err := set.Run(ctx, "schedule", Args{"in_minutes": float64(90), "note": "毎日", "daily": true}, nil); !errors.Is(err, store.ErrScheduleZone) {
		t.Fatalf("a daily schedule without a zone was accepted: %v", err)
	}
	at := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := set.Run(ctx, "schedule", Args{"at": at, "note": "毎日の薬", "daily": true, "time_zone": "Asia/Tokyo"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := set.Run(ctx, "schedule", Args{"at": "tomorrow evening", "note": "曖昧"}, nil); err == nil {
		t.Fatal("a non-RFC3339 time was accepted")
	}
	if _, err := set.Run(ctx, "schedule", Args{"note": "時刻がない"}, nil); err == nil {
		t.Fatal("a schedule without a time was accepted")
	}

	listed, err := set.Run(ctx, "schedule", Args{"action": "list"}, nil)
	if err != nil || !strings.Contains(listed.Text, "花を買う予定だった") {
		t.Fatalf("list = %q (%v)", listed.Text, err)
	}
	if _, err := set.Run(ctx, "schedule", Args{"action": "cancel", "id": "s_test"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(schedules.deleted) != 1 || schedules.deleted[0] != "routine/s_test" {
		t.Fatalf("deleted = %v", schedules.deleted)
	}
	if _, err := set.Run(ctx, "schedule", Args{"action": "sing"}, nil); err == nil {
		t.Fatal("an unknown action was accepted")
	}
}

// A schedule belongs to the persona that asked for it, so a call that cannot
// say who is asking is refused rather than writing someone's schedule.
func TestScheduleToolNeedsAnOwner(t *testing.T) {
	schedules := &fakeSchedules{}
	if _, err := scheduleSetFor("", schedules).Run(context.Background(), "schedule",
		Args{"in_minutes": float64(5), "note": "誰の"}, nil); err == nil {
		t.Fatal("an unowned schedule was accepted")
	}
	if len(schedules.stored) != 0 {
		t.Fatalf("stored = %+v", schedules.stored)
	}
}

func TestScheduleToolSaysWhenItIsNotConfigured(t *testing.T) {
	set := New(Config{}).For([]string{"schedule"}).As("routine")
	if _, err := set.Run(context.Background(), "schedule", Args{"in_minutes": float64(5), "note": "x"}, nil); err == nil {
		t.Fatal("a schedule was accepted without a store")
	}
}
