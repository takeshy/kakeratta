package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestBackgroundLanguageIgnoresAIAndLegacyRecords(t *testing.T) {
	at := time.Now()
	entries := []timeline.Entry{
		{ID: "en", Speaker: timeline.User, Language: "en", At: at},
		{ID: "legacy", Speaker: timeline.User, At: at.Add(time.Minute)},
		{ID: "ai", Speaker: "dad", Language: "ja", At: at.Add(time.Hour)},
		{ID: "deleted", Speaker: timeline.User, Language: "ja", Deleted: true, At: at.Add(time.Hour)},
	}
	if got := recordedLanguage(entries); got != "en" {
		t.Fatalf("language=%q", got)
	}
	entries = append(entries, timeline.Entry{ID: "ja", Speaker: timeline.User, Language: "ja", At: at.Add(2 * time.Hour)})
	if got := recordedLanguage(entries); got != "ja" {
		t.Fatalf("language=%q", got)
	}
	if got := recordedLanguage(nil); got != "" {
		t.Fatalf("legacy fallback=%q", got)
	}
}

func TestBackgroundTurnsExplicitlyUseRecordedEnglish(t *testing.T) {
	for _, wake := range []string{"daily", "schedule"} {
		t.Run(wake, func(t *testing.T) {
			seen := false
			r, s, _ := setup(t, func(system, user string) string {
				if strings.Contains(system, dailyHeader) || strings.Contains(system, scheduleInstructions) {
					seen = true
					if !strings.Contains(system, backgroundLanguageRule("en")) {
						t.Error("English background instruction missing")
					}
				}
				return `{"action":"silent"}`
			}, "routine")
			now := store.CheckInAt(s.Root(), store.DailyID, checkInDay(s, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), "routine"))
			if err := s.Append(timeline.Entry{ID: "english", Root: "english", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Going out later.", Language: "en", At: now.Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if wake == "daily" {
				r.dailyAt(context.Background(), now)
			} else {
				if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: now.Add(-time.Minute), Note: "Check belongings"}); err != nil {
					t.Fatal(err)
				}
				r.schedulesAt(context.Background(), now)
			}
			if !seen {
				t.Fatal("background turn did not run")
			}
		})
	}
}
