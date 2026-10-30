package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// Manual runs must never read or write the automatic claims.
type manualClaimSpy struct {
	store.Workspace
	calls int
}

func (w *manualClaimSpy) ClaimDailyCheckInAt(id, day string, at time.Time) (bool, time.Time, error) {
	w.calls++
	return w.Workspace.ClaimDailyCheckInAt(id, day, at)
}

func TestManualDailyIgnoresAndPreservesAutomaticClaims(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before automatic", true: "after automatic"}[completed], func(t *testing.T) {
			calls := 0
			r, s, _ := setup(t, func(system, user string) string {
				if strings.Contains(system, "Daily check-in") {
					calls++
				}
				return `{"action":"silent"}`
			}, "routine")
			settings := s.Settings()
			settings.DailyTime = "23:00"
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 15, 1, 0, 0, 0, settings.Location())
			original := now.Add(-time.Minute)
			if completed {
				for _, id := range []string{store.DailyID, "routine"} {
					if _, _, err := s.ClaimDailyCheckInAt(id, now.Format("2006-01-02"), original); err != nil {
						t.Fatal(err)
					}
				}
			}
			spy := &manualClaimSpy{Workspace: s}
			r.store = spy
			r.checkInAt(context.Background(), now, "daily", now)
			// A retry of the same request resumes after the stages done.
			r.checkInAt(context.Background(), now.Add(time.Minute), "daily", now)
			r.checkInAt(context.Background(), now.Add(time.Minute), "daily", now.Add(time.Minute))
			if calls != 2 || spy.calls != 0 {
				t.Fatalf("manual calls=%d, automatic claim accesses=%d", calls, spy.calls)
			}
			r.store = s
			scheduled := now.Add(22 * time.Hour)
			r.dailyAt(context.Background(), scheduled)
			expected := 3
			previous := scheduled
			if completed {
				expected = 2
				previous = original
			}
			if calls != expected {
				t.Fatalf("after automatic: calls=%d, want %d", calls, expected)
			}
			for _, id := range []string{store.DailyID, "routine"} {
				claimed, prior, err := s.ClaimDailyCheckInAt(id, now.AddDate(0, 0, 1).Format("2006-01-02"), now.AddDate(0, 0, 1))
				if err != nil || !claimed || !prior.Equal(previous) {
					t.Fatalf("automatic record changed: %s %v %s %v", id, claimed, prior, err)
				}
			}
		})
	}
}

func TestManualMonthlyIgnoresAndPreservesAutomaticClaims(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before automatic", true: "after automatic"}[completed], func(t *testing.T) {
			summaries, monthly, daily := 0, 0, 0
			r, s, _ := setup(t, func(system, user string) string {
				switch {
				case strings.Contains(system, "今月のまとめ"):
					summaries++
					return "### 人\n- 佐藤さんと釣りに行った"
				case strings.Contains(system, "Monthly check-in"):
					monthly++
					if !strings.Contains(user, "佐藤さんと釣りに行った") {
						t.Error("monthly summary missing")
					}
				case strings.Contains(system, "Daily check-in"):
					daily++
				}
				return `{"action":"silent"}`
			}, "routine")
			raw, _ := json.Marshal(sato())
			if _, err := r.catalogue.Install(raw, nil); err != nil {
				t.Fatal(err)
			}
			settings := s.Settings()
			settings.Active = []string{"routine", "sato"}
			settings.DailyCheckIns = map[string]store.DailyCheckIn{"sato": {Every: store.CheckInMonthly}}
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			day := summaryDay(s)
			at, _, _ := settings.DailyAt(s.ID(), day)
			now := at.Add(-time.Hour)
			appendAt(t, s, "佐藤さんと釣りに行った", now.Add(-time.Hour))
			original := now.Add(-time.Minute)
			if completed {
				// Preserve legacy date-shaped claims too.
				if _, _, err := s.ClaimDailyCheckInAt(store.SummaryID, now.Format("2006-01-02"), original); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.ClaimDailyCheckInAt(monthlyClaimID("sato"), now.Format("2006-01"), original); err != nil {
					t.Fatal(err)
				}
			}
			spy := &manualClaimSpy{Workspace: s}
			r.store = spy
			r.checkInAt(context.Background(), now, "monthly", now)
			r.checkInAt(context.Background(), now.Add(time.Minute), "monthly", now)
			r.checkInAt(context.Background(), now.Add(time.Minute), "monthly", now.Add(time.Minute))
			if summaries != 2 || monthly != 2 || daily != 0 || spy.calls != 0 {
				t.Fatalf("manual: summaries=%d monthly=%d daily=%d claims=%d", summaries, monthly, daily, spy.calls)
			}
			r.store = s
			r.dailyAt(context.Background(), at)
			expected := 3
			previous := at
			if completed {
				expected = 2
				previous = original
			}
			if summaries != expected || monthly != expected {
				t.Fatalf("automatic: summaries=%d monthly=%d want=%d", summaries, monthly, expected)
			}
			next := at.AddDate(0, 1, 0)
			for _, id := range []string{store.SummaryID, monthlyClaimID("sato")} {
				claimed, prior, err := s.ClaimDailyCheckInAt(id, next.Format("2006-01"), next)
				if err != nil || !claimed || !prior.Equal(previous) {
					t.Fatalf("automatic record changed: %s %v %s %v", id, claimed, prior, err)
				}
			}
		})
	}
}

// A person switched from daily to monthly still checks in that month, though
// its daily claim holds a later key than the month.
func TestMonthlyCheckInAfterSwitchFromDaily(t *testing.T) {
	monthly := 0
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "Monthly check-in") {
			monthly++
		}
		return `{"action":"silent"}`
	}, "routine")
	raw, _ := json.Marshal(sato())
	if _, err := r.catalogue.Install(raw, nil); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.Active = []string{"routine", "sato"}
	settings.DailyCheckIns = map[string]store.DailyCheckIn{"sato": {Every: store.CheckInMonthly}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := summaryDay(s)
	at, _, _ := settings.DailyAt(s.ID(), day)
	if _, _, err := s.ClaimDailyCheckInAt("sato", at.AddDate(0, 0, -1).Format("2006-01-02"), at.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(context.Background(), at)
	if monthly != 1 {
		t.Fatalf("monthly check-ins = %d; want 1", monthly)
	}
}

func TestManualCheckInQueueCoalescesOnlyPendingRequests(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` })
	request := func() {
		t.Helper()
		for range 3 {
			if err := r.RequestCheckIn("daily"); err != nil {
				t.Fatal(err)
			}
			if err := r.RequestCheckIn("monthly"); err != nil {
				t.Fatal(err)
			}
		}
	}
	request()
	ctx := context.Background()
	item, _, ok, err := s.ClaimReaction(ctx, "test-manual", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	request()
	items, err := s.Reactions(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("queue=%v %v", items, err)
	}
	for _, kept := range items {
		if kept.Lane != store.DueReaction || kept.CheckIn == "" {
			t.Fatalf("bad reaction: %+v", kept)
		}
		if kept.ID == item.ID && (kept.ClaimedBy != item.ClaimedBy || !kept.At.Equal(item.At)) {
			t.Fatal("running request was overwritten")
		}
	}
	if _, err := s.FinishReaction(ctx, item.ID, "test-manual"); err != nil {
		t.Fatal(err)
	}
	if err := r.RequestCheckIn(item.CheckIn); err != nil {
		t.Fatal(err)
	}
	items, err = s.Reactions(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("cannot rerun completed request: %v %v", items, err)
	}
	if err := r.RequestCheckIn("invalid"); err == nil {
		t.Fatal("accepted invalid period")
	}
	r.Stop()
}
