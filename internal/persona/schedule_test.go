package persona

import (
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

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
	r.schedulesAt(time.Now())
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	// The wake is claimed before the model runs, so a silent one is not repeated.
	if left, err := s.Schedules(""); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
	r.schedulesAt(time.Now())
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
	r.schedulesAt(time.Now())
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
	r.schedulesAt(time.Now())
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
	r.stopMu.Lock()
	queued := len(r.queue)
	var gone forgotten
	if queued == 1 && r.queue[0].forgotten != nil {
		gone = *r.queue[0].forgotten
	}
	r.stopMu.Unlock()
	if queued != 1 {
		t.Fatalf("queued %d wakes for two deletions", queued)
	}
	if len(gone.paths) != 2 {
		t.Fatalf("paths = %v", gone.paths)
	}

	r.forgottenAt(gone)
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	for _, want := range []string{"メモが消されました", "errands/flowers.md", "routines/medicine.md"} {
		if !strings.Contains(seen, want) {
			t.Fatalf("the persona was not told %q:\n%s", want, seen)
		}
	}
}
