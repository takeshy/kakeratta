package persona

import (
	"context"
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
			now := store.CheckInAt(s.Root(), skill.ID, checkInDay(s, time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), skill.ID))
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
	// Two days later - one day missed - it reads from that run.
	later := checkInDay(s, day.AddDate(0, 0, 2), "gourmet")
	at := store.CheckInCloses(later).Add(-time.Minute)
	r.dailyAt(context.Background(), at)
	want := `Since the previous check-in: from "` + first.Format("2006-01-02 15:04") + `" to "` + at.Format("2006-01-02 15:04") + `"`
	if !strings.Contains(user, want) {
		t.Fatalf("want %q in:\n%s", want, user)
	}
}
