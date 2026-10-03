package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (f *fakeSchedules) UpdateSchedule(skill, id string, patch store.SchedulePatch) (store.Schedule, error) {
	for i, schedule := range f.stored {
		if schedule.ID == id && schedule.Skill == skill {
			updated, err := patch.Apply(schedule, time.Now())
			if err == nil {
				f.stored[i] = updated
			}
			return updated, err
		}
	}
	return store.Schedule{}, store.ErrScheduleNotFound
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

// An offset in an RFC3339 string is not a zone: Go reads it back as "Local",
// which on a server means UTC. The workspace's own zone is used instead.
func TestScheduleToolFallsBackToTheWorkspaceZone(t *testing.T) {
	schedules := &fakeSchedules{}
	set := New(Config{}).WithSchedules(schedules).WithZone("Asia/Tokyo").For([]string{"schedule"}).As("routine")
	at := time.Now().Add(3 * time.Hour).Format(time.RFC3339)
	if _, err := set.Run(context.Background(), "schedule", Args{"at": at, "note": "ゾーンを書かなかった"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(schedules.stored) != 1 || schedules.stored[0].TimeZone != "Asia/Tokyo" {
		t.Fatalf("stored = %+v", schedules.stored)
	}
	// A daily wake with no zone anywhere is refused rather than stored as UTC.
	bare := New(Config{}).WithSchedules(schedules).For([]string{"schedule"}).As("routine")
	if _, err := bare.Run(context.Background(), "schedule",
		Args{"at": at, "note": "毎日", "daily": true}, nil); !errors.Is(err, store.ErrScheduleZone) {
		t.Fatalf("a daily schedule without any zone was accepted: %v", err)
	}
}

func TestScheduleListIncludesOtherPersonasButCannotCancelThem(t *testing.T) {
	workspace, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	own, err := workspace.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(time.Hour), Note: "朝の薬"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := workspace.SaveSchedule(store.Schedule{Skill: "health", At: time.Now().Add(2 * time.Hour), Note: "診察"})
	if err != nil {
		t.Fatal(err)
	}
	set := New(Config{}).WithSchedules(workspace).WithPersonaNames(map[string]string{"routine": "プラナ", "health": "健康担当"}).For([]string{"schedule"}).As("routine")
	result, err := set.Run(context.Background(), "schedule", Args{"action": "list"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID    string `json:"id"`
		Skill string `json:"skill"`
		Owner string `json:"owner"`
		Note  string `json:"note"`
	}
	if err := json.Unmarshal([]byte(result.Text), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != own.ID || rows[0].Skill != "routine" || rows[1].ID != other.ID || rows[1].Skill != "health" || rows[1].Note != "診察" || rows[0].Owner != "プラナ（routine）" || rows[1].Owner != "健康担当（health）" {
		t.Fatalf("list = %+v", rows)
	}
	fallback, err := scheduleSetFor("routine", workspace).Run(context.Background(), "schedule", Args{"action": "list"}, nil)
	if err != nil || !strings.Contains(fallback.Text, `"owner":"health"`) {
		t.Fatalf("ID fallback = %s (%v)", fallback.Text, err)
	}
	if _, err := set.Run(context.Background(), "schedule", Args{"action": "cancel", "id": other.ID}, nil); !errors.Is(err, store.ErrScheduleNotFound) {
		t.Fatalf("cancel other persona: %v", err)
	}
	if _, err := set.Run(context.Background(), "schedule", Args{"action": "cancel", "id": own.ID}, nil); err != nil {
		t.Fatal(err)
	}
	left, err := workspace.Schedules("")
	if err != nil || len(left) != 1 || left[0].ID != other.ID {
		t.Fatalf("remaining = %+v (%v)", left, err)
	}
}

func TestSchedulesListFiltersPaginatesAndDoesNotModifySchedules(t *testing.T) {
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	schedules := &fakeSchedules{}
	for i := range 35 {
		owner := "routine"
		if i%2 == 0 {
			owner = "health"
		}
		schedules.stored = append(schedules.stored, store.Schedule{ID: fmt.Sprintf("s_%02d", i), Skill: owner, At: at.Add(time.Duration(i) * time.Hour), Note: fmt.Sprintf("薬 %02d", i), NotePath: "private.md", TimeZone: "Asia/Tokyo"})
	}
	registry := New(Config{}).WithSchedules(schedules).WithPersonaNames(map[string]string{"health": "健康担当"}).WithZone("Asia/Tokyo")
	set := registry.For([]string{"schedule"}).As("routine")
	if !set.Allows("schedules_list") || !set.IsReadOnly("schedules_list") || set.IsReadOnly("schedule") {
		t.Fatal("read/write permissions incorrect")
	}
	type row struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
		Note  string `json:"note"`
	}
	type page struct {
		Rows  []row  `json:"schedules"`
		Total int    `json:"total"`
		Next  string `json:"next_after"`
	}
	read := func(args Args) page {
		t.Helper()
		result, err := set.Run(context.Background(), "schedules_list", args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Text, "private.md") {
			t.Fatal("background memory path leaked")
		}
		var got page
		if err := json.Unmarshal([]byte(result.Text), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := read(Args{})
	if first.Total != 35 || len(first.Rows) != 20 || first.Next != "s_19" || first.Rows[0].Owner != "健康担当（health）" {
		t.Fatalf("first page = %+v", first)
	}
	second := read(Args{"after": first.Next})
	if second.Total != 35 || len(second.Rows) != 15 || second.Next != "" || second.Rows[0].ID != "s_20" {
		t.Fatalf("second page = %+v", second)
	}
	capped := read(Args{"limit": float64(100)})
	if len(capped.Rows) != 30 || capped.Next != "s_29" {
		t.Fatalf("capped page = %+v", capped)
	}
	filtered := read(Args{"skill": "health", "query": "薬", "from": "2026-10-02 11:00", "to": "2026-10-02 15:00"})
	if filtered.Total != 3 || len(filtered.Rows) != 3 || filtered.Rows[0].ID != "s_02" || filtered.Rows[2].ID != "s_06" {
		t.Fatalf("filtered = %+v", filtered)
	}
	byName := read(Args{"query": "健康担当"})
	if byName.Total != 18 {
		t.Fatalf("name filter = %+v", byName)
	}
	absent := read(Args{"query": "一致しない"})
	if absent.Total != 0 || len(absent.Rows) != 0 || absent.Next != "" {
		t.Fatalf("empty = %+v", absent)
	}
	for _, args := range []Args{{"from": "bad"}, {"from": "2026-10-03", "to": "2026-10-02"}, {"after": "s_missing"}} {
		if _, err := set.Run(context.Background(), "schedules_list", args, nil); err == nil {
			t.Fatalf("invalid args accepted: %+v", args)
		}
	}
	if len(schedules.stored) != 35 || len(schedules.deleted) != 0 {
		t.Fatal("read tool modified schedules")
	}
	if _, err := registry.For([]string{"schedule"}).Run(context.Background(), "schedules_list", Args{}, nil); err == nil {
		t.Fatal("unowned read accepted")
	}
	if _, err := registry.For(nil).As("routine").Run(context.Background(), "schedules_list", Args{}, nil); err == nil {
		t.Fatal("unpermitted read accepted")
	}
}
