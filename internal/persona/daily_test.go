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
			r, s, stub := setup(t, func(system, user string) string {
				calls++
				if !strings.Contains(system, "Check-in（朝・昼・晩") {
					t.Error("check-in prompt missing")
				}
				if calls == 1 && !strings.Contains(user, "Check-in: 朝 (morning)") {
					t.Errorf("the part of the day was not given:\n%s", user)
				}
				return `{"action":"` + action + `","text":"Something worth sharing"}`
			})
			skill := r.catalogue.All()[0]
			settings := s.Settings()
			settings.Active = []string{skill.ID}
			settings.DailyCheckIns = map[string]store.DailyCheckIn{skill.ID: {Slots: []string{"morning", "noon", "evening"}, TimeZone: "Asia/Tokyo"}}
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			// 06:00 in Tokyo: the start of the morning.
			now := time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC)
			r.dailyAt(now.Add(-time.Minute))
			if calls != 0 {
				t.Fatal("ran during the night")
			}
			r.dailyAt(now)
			r.dailyAt(now.Add(time.Hour))
			if calls != 1 {
				t.Fatalf("calls = %d", calls)
			}
			if !stub.seen("timeline_search") || !stub.seen("notes_read") {
				t.Fatal("missing tools")
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
			// 11:00 is noon, a separate wake on the same day.
			r.dailyAt(now.Add(5 * time.Hour))
			if calls != 2 {
				t.Fatalf("noon did not wake: calls = %d", calls)
			}
			settings.Active = nil
			_ = s.SaveSettings(settings)
			r.dailyAt(now.AddDate(0, 0, 1))
			if calls != 2 {
				t.Fatal("inactive persona woke")
			}
		})
	}
}

func TestDailyCanRememberWithoutPosting(t *testing.T) {
	calls := 0
	r, s, _ := setup(t, func(system, user string) string {
		calls++
		if calls == 1 {
			return `{"action":"tool","tool":"notes_write","args":{"path":"daily.md","text":"Remembered quietly"}}`
		}
		return `{"action":"silent"}`
	})
	skill := r.catalogue.All()[0]
	settings := s.Settings()
	settings.Active = []string{skill.ID}
	settings.DailyCheckIns = map[string]store.DailyCheckIn{skill.ID: {Slots: []string{"noon"}, TimeZone: "UTC"}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	note, ok := s.Note(skill.ID, "daily.md")
	if !ok || !strings.Contains(note.Text, "Remembered quietly") {
		t.Fatal("silent wake did not retain memory")
	}
	if len(store.NoteSources(note.Text)) != 0 {
		t.Fatal("synthetic event leaked as a note source")
	}
	entries, _ := s.Timeline()
	if len(entries) != 0 {
		t.Fatal("silent memory update posted")
	}
}
