package persona

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestDailyCheckInSilenceAndSpeech(t *testing.T) {
	for _, action := range []string{"silent", "speak"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			r, s, _ := setup(t, func(system, user string) string {
				calls++
				if !strings.Contains(system, "Daily check-in") {
					t.Error("daily prompt missing")
				}
				for _, want := range []string{"前回のチェックインから今回まで", "自分が返答していない", "最近の記録によく現れるタグ", "records"} {
					if !strings.Contains(system, want) {
						t.Errorf("daily discovery instruction missing %q", want)
					}
				}
				return `{"action":"` + action + `","text":"Something worth sharing"}`
			})
			skill := r.catalogue.All(context.Background())[0]
			settings := s.Settings()
			settings.Active = []string{skill.ID}
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			now := store.CheckInAt(s.Root(), store.DailyID, checkInDay(s, time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), skill.ID))
			r.dailyAt(context.Background(), now.Add(-time.Minute))
			if calls != 0 {
				t.Fatal("ran before its minute")
			}
			r.dailyAt(context.Background(), now)
			r.dailyAt(context.Background(), now.Add(time.Hour))
			if calls != 1 {
				t.Fatalf("calls = %d", calls)
			}
			entries, _ := s.Timeline()
			if action == "silent" && len(entries) != 0 {
				t.Fatal("silence left a timeline entry")
			}
			if action == "speak" {
				if len(entries) != 1 || entries[0].Speaker != skill.ID || entries[0].Kind != timeline.KindPost || timeline.RootOf(entries[0]) != entries[0].ID {
					t.Fatalf("bad daily thread: %+v", entries)
				}
				page, err := s.TimelinePage(context.Background(), 20, "")
				if err != nil || len(page.Entries) != 1 {
					t.Fatalf("daily post missing from page: %+v %v", page, err)
				}
			}
			settings.Active = nil
			_ = s.SaveSettings(settings)
			r.dailyAt(context.Background(), store.CheckInCloses(checkInDay(s, now.AddDate(0, 0, 1), skill.ID)).Add(-time.Minute))
			if calls != 1 {
				t.Fatal("inactive persona woke")
			}
		})
	}
}

// A check-in is not a kakera: it leaves no record and no field behind.
func TestACheckInKeepsNoMemory(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"action":"silent"}` })
	skill := r.catalogue.All(context.Background())[0]
	settings := s.Settings()
	settings.Active = []string{skill.ID}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(context.Background(), store.CheckInCloses(checkInDay(s, time.Now().UTC(), skill.ID)).Add(-time.Minute))
	if len(f.keeps) != 0 {
		t.Fatalf("the keeper ran for a check-in: %d", len(f.keeps))
	}
	if paths, _ := s.NotePaths(skill.ID); len(paths) != 0 {
		t.Fatalf("a check-in wrote memory: %v", paths)
	}
}

// Legacy speaking settings do not override the persona's character.
func TestCheckInFollowsCharacterWithLegacyTalkSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		saved   store.DailyCheckIn
		want    string
		notWant string
	}{
		{"never set shares", store.DailyCheckIn{}, "前回のチェックインから今回まで", "ユーザーの判断が必要なことだけ"},
		{"legacy minimal", store.DailyCheckIn{Talk: store.CheckInMinimal}, "前回のチェックインから今回まで", "ユーザーの判断が必要なことだけ"},
		{"share", store.DailyCheckIn{Talk: store.CheckInShare}, "前回のチェックインから今回まで", "ユーザーの判断が必要なことだけ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var system string
			r, s, _ := setup(t, func(sys, user string) string {
				if strings.HasPrefix(sys, "あなたは「tabe」") {
					system = sys
				}
				return `{"action":"silent"}`
			}, "gourmet")
			settings := s.Settings()
			settings.DailyCheckIns = map[string]store.DailyCheckIn{"gourmet": tc.saved}
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			r.dailyAt(context.Background(), store.CheckInCloses(checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet")).Add(-time.Minute))
			if !strings.Contains(system, tc.want) || strings.Contains(system, tc.notWant) {
				t.Fatalf("check-in instructions:\n%s", system)
			}
		})
	}
}

// A sharing check-in reads from when the previous one actually ran, so no
// stretch falls between two - whatever the minute did, or a day missed.
func TestASharingCheckInReadsSinceThePreviousRun(t *testing.T) {
	var user string
	r, s, _ := setup(t, func(sys, usr string) string {
		if strings.HasPrefix(sys, "あなたは「tabe」") {
			user = usr
		}
		return `{"action":"silent"}`
	}, "gourmet")
	day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet")
	// The first check-in knows no previous run and reads a day.
	first := store.CheckInCloses(day).Add(-time.Minute)
	r.dailyAt(context.Background(), first)
	if want := `from "` + first.Add(-24*time.Hour).Format("2006-01-02 15:04") + `"`; !strings.Contains(user, want) {
		t.Fatalf("want %q in:\n%s", want, user)
	}
	// Two days later - one day missed - it reads from that run, and is
	// handed what the user posted in between.
	later := checkInDay(s, day.AddDate(0, 0, 2), "gourmet")
	at := store.CheckInCloses(later).Add(-time.Minute)
	between := appendAt(t, s, "新しい店に行った", first.Add(time.Hour))
	r.dailyAt(context.Background(), at)
	if !strings.Contains(user, "Records since the previous check-in") || !strings.Contains(user, between.ID) {
		t.Fatalf("the span was not handed over:\n%s", user)
	}
	want := `Since the previous check-in: from "` + first.Format("2006-01-02 15:04") + `" to "` + at.Format("2006-01-02 15:04") + `"`
	if !strings.Contains(user, want) {
		t.Fatalf("want %q in:\n%s", want, user)
	}
}

// A day with nothing new from the user, no record due and no dated plan
// coming up costs no model call; a plan within the week still wakes it.
func TestAQuietCheckInIsSkippedUnlessAPlanIsNear(t *testing.T) {
	for _, plan := range []bool{false, true} {
		t.Run(fmt.Sprintf("plan=%t", plan), func(t *testing.T) {
			calls := 0
			r, s, _ := setup(t, func(sys, usr string) string {
				if strings.HasPrefix(sys, "あなたは「tabe」") {
					calls++
				}
				return `{"action":"silent"}`
			}, "gourmet")
			day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet")
			first := store.CheckInCloses(day).Add(-time.Minute)
			r.dailyAt(context.Background(), first)
			if calls == 0 {
				t.Fatal("the first check-in, with no previous run, was skipped")
			}
			if plan {
				soon := first.AddDate(0, 0, 3).Format("2006-01-02")
				if err := s.WriteNote("gourmet", "Shop.md", factsText("Shop", fieldFacts{Current: []fact{{ID: "f_a", Text: soon + " にA店を予約", Since: first}}}, time.UTC)); err != nil {
					t.Fatal(err)
				}
			}
			calls = 0
			next := checkInDay(s, day.AddDate(0, 0, 1), "gourmet")
			r.dailyAt(context.Background(), store.CheckInCloses(next).Add(-time.Minute))
			if (calls > 0) != plan {
				t.Fatalf("calls=%d with plan=%t", calls, plan)
			}
		})
	}
}

// The account's day runs at the time the user set, even before the app's
// own window opens, and not a minute earlier.
func TestTheDailyRunFollowsTheSetTime(t *testing.T) {
	calls := 0
	r, s, _ := setup(t, func(sys, usr string) string {
		if strings.HasPrefix(sys, "あなたは「tabe」") {
			calls++
		}
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.DailyTime = "06:30"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 25, 6, 30, 0, 0, time.UTC)
	r.dailyAt(context.Background(), at.Add(-time.Minute))
	if calls != 0 {
		t.Fatal("ran before the set time")
	}
	r.dailyAt(context.Background(), at)
	if calls != 1 {
		t.Fatalf("calls = %d at the set time", calls)
	}
}
