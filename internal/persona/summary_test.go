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

func appendAt(t *testing.T, s *store.Store, text string, at time.Time) timeline.Entry {
	t.Helper()
	entry := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User, Text: text, At: at}
	entry.Root = entry.ID
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

// summaryDay is a day in October 2026 on which the workspace's summary falls.
func summaryDay(s *store.Store) time.Time {
	return time.Date(2026, 10, store.SummaryDay(s.Root()), 0, 0, 0, 0, time.UTC)
}

// The month's summary is written once, on its day at its minute, from the
// month's timeline, and posted by the app.
func TestMonthlySummaryIsWrittenOnceOnItsDay(t *testing.T) {
	calls := 0
	var read string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "今月のまとめ") {
			calls++
			read = user
			return "### 人\n- 佐藤さんと釣りに行った [→](/?entry=x)"
		}
		return `{"action":"silent"}`
	})
	day := summaryDay(s)
	at := store.CheckInAt(s.Root(), store.SummaryID, day)
	appendAt(t, s, "佐藤さんと釣りに行った", at.Add(-10*24*time.Hour))
	appendAt(t, s, "去年の話", at.Add(-60*24*time.Hour))

	r.dailyAt(context.Background(), at.Add(-time.Minute))
	if calls != 0 {
		t.Fatal("written before its minute")
	}
	r.dailyAt(context.Background(), at)
	r.dailyAt(context.Background(), at.Add(time.Hour))
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if !strings.Contains(read, "佐藤さんと釣りに行った") || strings.Contains(read, "去年の話") {
		t.Fatalf("the month read was %q", read)
	}
	entries, _ := s.Timeline()
	summary := entries[len(entries)-1]
	if summary.Speaker != timeline.App || summary.Summary != timeline.SummaryMonth || summary.Kind != timeline.KindPost || timeline.RootOf(summary) != summary.ID {
		t.Fatalf("summary = %+v", summary)
	}

	// Another day of the month writes none.
	r.dailyAt(context.Background(), at.AddDate(0, 0, 1))
	if calls != 1 {
		t.Fatal("written on another day")
	}
}

// A person heard from monthly checks in only on the summary's day, after it,
// and is handed the summary and the posts that name it.
func TestMonthlyPersonReadsTheSummary(t *testing.T) {
	var checkIn string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "今月のまとめ") {
			return "### 人\n- 佐藤さんと釣りに行った"
		}
		if strings.Contains(system, "Monthly check-in") {
			checkIn = user
		}
		return `{"action":"silent"}`
	})
	raw, _ := json.Marshal(sato())
	if _, err := r.catalogue.Install(raw, nil); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.Active = []string{"sato"}
	settings.DailyCheckIns = map[string]store.DailyCheckIn{"sato": {Enabled: true, Talk: store.CheckInShare, Every: store.CheckInMonthly}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := summaryDay(s)
	at := store.CheckInAt(s.Root(), store.SummaryID, day)
	if own := store.CheckInAt(s.Root(), "sato", day); own.After(at) {
		at = own
	}
	appendAt(t, s, "サトウと釣行の相談", at.Add(-5*24*time.Hour))
	appendAt(t, s, "ラーメンを食べた", at.Add(-4*24*time.Hour))

	// Not on another day of the month, whatever the minute.
	other := day.AddDate(0, 0, 1)
	r.dailyAt(context.Background(), store.CheckInCloses(other).Add(-time.Minute))
	if checkIn != "" {
		t.Fatal("a monthly person checked in on another day")
	}
	r.dailyAt(context.Background(), at)
	if !strings.Contains(checkIn, "Monthly check-in") || !strings.Contains(checkIn, "佐藤さんと釣りに行った") {
		t.Fatalf("the summary was not handed over: %q", checkIn)
	}
	if !strings.Contains(checkIn, "サトウと釣行の相談") || strings.Contains(checkIn, "ラーメンを食べた") {
		t.Fatalf("the posts naming the person were not picked: %q", checkIn)
	}
}
