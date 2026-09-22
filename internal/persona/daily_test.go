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
				if !strings.Contains(system, "Daily check-in") {
					t.Error("daily prompt missing")
				}
				for _, want := range []string{"直近24時間", "自分が返答していない", "最近更新されたメモ", "tags", "notes_read"} {
					if !strings.Contains(system, want) {
						t.Errorf("daily discovery instruction missing %q", want)
					}
				}
				return `{"action":"` + action + `","text":"Something worth sharing"}`
			})
			skill := r.catalogue.All()[0]
			settings := s.Settings()
			settings.Active = []string{skill.ID}
			settings.DailyCheckIns = map[string]store.DailyCheckIn{skill.ID: {Enabled: true, Time: "08:00", TimeZone: "Asia/Tokyo"}}
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC)
			r.dailyAt(now.Add(-time.Minute))
			if calls != 0 {
				t.Fatal("ran before preferred time")
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
			settings.Active = nil
			_ = s.SaveSettings(settings)
			r.dailyAt(now.AddDate(0, 0, 1))
			if calls != 1 {
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
	settings.DailyCheckIns = map[string]store.DailyCheckIn{skill.ID: {Enabled: true, Time: "00:00", TimeZone: "UTC"}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(time.Now())
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

func TestHermesDailyReceivesSharedCheckInInstructions(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "hermes")
	settings := s.Settings()
	settings.DailyCheckIns = map[string]store.DailyCheckIn{
		"hermes": {Enabled: true, Time: "08:00", TimeZone: "Asia/Tokyo"},
	}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC)
	r.dailyAt(now)
	tasks, err := store.AgentTasks(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Kind != "check-in" || tasks[0].Trigger != "" {
		t.Fatalf("check-in task = %+v", tasks)
	}
	if !strings.Contains(tasks[0].Prompt, "Daily check-in:") || !strings.Contains(tasks[0].Prompt, dailyInstructions) {
		t.Fatalf("Hermes did not receive the shared daily prompt: %q", tasks[0].Prompt)
	}
}
