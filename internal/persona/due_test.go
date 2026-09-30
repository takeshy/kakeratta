package persona

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// alarms records the times a runner asked to be woken at.
type alarms struct {
	mu sync.Mutex
	at []time.Time
}

func (a *alarms) set(at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.at = append(a.at, at)
	return nil
}

func (a *alarms) last() (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.at) == 0 {
		return time.Time{}, false
	}
	return a.at[len(a.at)-1], true
}

// The next check-in is the earliest one after now that dailyAt would run:
// the first day's time in the window, a skipped day passed over.
func TestTheNextCheckInIsTheEarliestOneAfterNow(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	settings := s.Settings()
	root := s.Root()
	summary := func(day time.Time) bool { return day.Day() == store.SummaryDay(root) }
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for minutes := 0; minutes < 40*24*60; minutes += 97 {
		now := start.Add(time.Duration(minutes) * time.Minute)
		got := runner.nextCheckIn(context.Background(), now, settings)
		var want time.Time
		for d := 0; d < 40 && want.IsZero(); d++ {
			day := time.Date(now.Year(), now.Month(), now.Day()+d, 0, 0, 0, 0, time.UTC)
			var candidates []time.Time
			if !store.CheckInSkipped(root, "gourmet", day) {
				candidates = append(candidates, store.CheckInAt(root, "gourmet", day))
			}
			if summary(day) {
				candidates = append(candidates, store.CheckInAt(root, store.SummaryID, day))
			}
			for _, at := range candidates {
				if at.After(now) && (want.IsZero() || at.Before(want)) {
					want = at
				}
			}
		}
		if !got.Equal(want) {
			t.Fatalf("at %s the next check-in is %s, want %s", now, got, want)
		}
		if !got.After(now) || got.Sub(now) > 4*24*time.Hour {
			t.Fatalf("at %s the next check-in %s is not soon", now, got)
		}
	}
}

// The workspace is woken at the earliest of what comes due: a schedule, a
// note retry, an ask's wait, an expires date, a check-in - and at most
// maxDue ahead, at least minDue.
func TestTheNextDueIsTheEarliestThingDue(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	ctx := context.Background()
	settings := s.Settings()
	now := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)
	checkIn := runner.nextCheckIn(ctx, now, settings)
	if !checkIn.After(now.Add(3 * time.Hour)) {
		t.Fatalf("the test needs the next check-in to be far off, it is %s", checkIn)
	}

	if got := runner.nextDue(ctx, now, settings, time.Time{}); !got.Equal(checkIn) {
		t.Fatalf("with only check-ins the next due is %s, want %s", got, checkIn)
	}
	saved, err := s.SaveSchedule(store.Schedule{Skill: "gourmet", At: now.Add(2 * time.Hour), Note: "買い物"})
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.nextDue(ctx, now, settings, time.Time{}); !got.Equal(saved.At) {
		t.Fatalf("with a schedule the next due is %s, want %s", got, saved.At)
	}
	if err := s.SaveNoteJob(store.NoteJob{ID: "note-1", Persona: "gourmet", Stage: "notes", NextAttempt: now.Add(45 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got := runner.nextDue(ctx, now, settings, time.Time{}); !got.Equal(now.Add(45 * time.Minute)) {
		t.Fatalf("with a note retry the next due is %s", got)
	}
	if got := runner.nextDue(ctx, now, settings, now.Add(10*time.Minute)); !got.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("with an expires date the next due is %s", got)
	}
	settings.ExternalAgent.ClaimMinutes = 3
	runner.waitsForAsk = func() (time.Duration, time.Duration) { return 5 * time.Minute, time.Hour }
	if err := runner.openAsk(ctx, settings, Ask{ID: "ask-1", Persona: "gourmet"}, AskContext{}, askTurn{Persona: "gourmet"}); err != nil {
		t.Fatal(err)
	}
	if got := runner.nextDue(ctx, time.Now(), settings, time.Time{}); got.After(time.Now().Add(5*time.Minute+time.Second)) || !got.After(time.Now()) {
		t.Fatalf("with an open ask the next due is %s, not its claim wait", got)
	}

	// Nothing to wait for but the month's summary, more than a task can be
	// set ahead: the workspace is woken at the limit and sets the rest then.
	empty, es, _ := setup(t, answering)
	var far time.Time
	for hours := 0; hours < 62*24 && far.IsZero(); hours++ {
		at := now.Add(time.Duration(hours) * time.Hour)
		if empty.nextCheckIn(ctx, at, es.Settings()).Sub(at) > maxDue {
			far = at
		}
	}
	if far.IsZero() {
		t.Skip("the summary is never further away than a task can be set")
	}
	if got := empty.nextDue(ctx, far, es.Settings(), time.Time{}); !got.Equal(far.Add(maxDue)) {
		t.Fatalf("with nothing sooner the next due is %s, want %s", got, far.Add(maxDue))
	}
	// Something already due does not wake the workspace in a loop.
	if _, err := s.SaveSchedule(store.Schedule{ID: "past", Skill: "gourmet", At: now.Add(-time.Hour), Note: "過去"}); err == nil {
		t.Log("a past schedule was accepted by the store")
	}
	if got := runner.nextDue(ctx, now, settings, now.Add(-time.Hour)); got.Before(now.Add(minDue)) {
		t.Fatalf("the next due %s is sooner than %s", got, minDue)
	}
}

// A due reaction ends by asking to be woken at the next time, and a piece of
// work ends by asking to be looked at again soon.
func TestTheWorkspaceAsksToBeWokenAgain(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	var asked alarms
	runner.SetAlarm(asked.set)
	saved, err := s.SaveSchedule(store.Schedule{Skill: "gourmet", At: time.Now().Add(3 * time.Hour), Note: "買い物"})
	if err != nil {
		t.Fatal(err)
	}
	runner.Due()
	runner.Due() // One is waiting; this is the same.
	runner.Stop()
	at, ok := asked.last()
	if !ok || at.After(saved.At.Add(time.Second)) || at.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("woken again at %v (%v), want no later than the schedule at %s", at, ok, saved.At)
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("left = %+v", got)
	}

	runner, s, _ = setup(t, answering, "gourmet")
	asked = alarms{}
	runner.SetAlarm(asked.set)
	entry := post(t, s, "ラーメン食べたい")
	runner.React(entry.ID, nil)
	runner.Stop()
	first := asked.at
	if len(first) == 0 || first[0].Before(time.Now().Add(-time.Minute)) || first[0].After(time.Now().Add(soonDue+time.Minute)) {
		t.Fatalf("after a piece of work the alarms were %v", first)
	}
}

// expireNotes reports the next expires date it did not reach.
func TestExpiringNotesReportTheNextDate(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	settings := s.Settings()
	settings.MiddleModel = "m"
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{
		"shops/gone.md":  "---\ntype: Shop\nexpires: 2026-09-20\n---\n\nもう",
		"shops/soon.md":  "---\ntype: Shop\nexpires: 2026-10-05\n---\n\nもうすぐ",
		"shops/later.md": "---\ntype: Shop\nexpires: 2027-01-01\n---\n\nまだ",
		"shops/never.md": "---\ntype: Shop\nexpires: never\n---\n\nずっと",
	} {
		if err := s.WriteNote("gourmet", path, text); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	next := r.expireNotes(context.Background(), now, s.Settings())
	if want := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next expires = %s, want %s", next, want)
	}
	if _, ok := s.Note("gourmet", "shops/gone.md"); ok {
		t.Fatal("the expired note is still there")
	}
}
