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

func TestPostAnswersNameTheDisplayLanguage(t *testing.T) {
	post := timeline.Entry{ID: "p", Speaker: timeline.User, Text: "Going to Shizuoka tomorrow.", Language: "en"}
	rules := answerRulesFor([]timeline.Entry{post}, nil, briefRule)
	if !strings.Contains(rules, postLanguageRule("en")) || strings.Contains(rules, postLanguageRule("")) {
		t.Fatalf("English post rule missing:\n%s", rules)
	}
	thread := []timeline.Entry{post, {ID: "a", Speaker: "gourmet", Text: "静岡か。", Language: ""}, {ID: "q", Speaker: timeline.User, Text: "何がおすすめ？", Language: "ja"}}
	if rules := answerRulesFor(thread, nil, briefRule); !strings.Contains(rules, postLanguageRule("ja")) {
		t.Fatalf("newest user language not used:\n%s", rules)
	}
	if rules := answerRulesFor([]timeline.Entry{{ID: "legacy", Speaker: timeline.User, Text: "昔の投稿"}}, nil, briefRule); !strings.Contains(rules, postLanguageRule("")) {
		t.Fatalf("legacy post lost the generic rule:\n%s", rules)
	}
}

func TestMemoryTidyWritesFactsInTheRecordedLanguage(t *testing.T) {
	var tidy string
	r, s, f := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), "gourmet")
	if err := s.Append(timeline.Entry{ID: "english", Root: "english", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Wanted to try Sawayaka.", Language: "en", At: day.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	memo := memo{Records: []record{{At: day.Add(-time.Hour), ID: "e_r", Summary: "User wanted to try Sawayaka in Shizuoka."}}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if strings.Contains(system, "メモ係として") {
			tidy = system
		}
		return `{"facts":[],"drop_records":[],"review_records":[]}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if tidy == "" {
		t.Fatal("the tidy did not run")
	}
	if !strings.Contains(tidy, factLanguageRule("en")) {
		t.Fatalf("English fact rule missing:\n%s", tidy)
	}
}

func TestAHandMovedReminderIsToldItsNewTimeHolds(t *testing.T) {
	var seen string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "ユーザーが指定した予定による起床") {
			seen = user
		}
		return `{"action":"silent"}`
	}, "routine")
	now := time.Now()
	// Moved by hand from six o'clock to a minute ago (store.SchedulePatch sets TimeEdited).
	if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: now.Add(-time.Minute), Note: "Remind me at six to pick up the wine", UserRequested: true, TimeEdited: true, TimeZone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	r.schedulesAt(context.Background(), now)
	if seen == "" {
		t.Fatal("the schedule wake did not run")
	}
	if !strings.Contains(seen, "The user moved this reminder to this time themselves") {
		t.Fatalf("moved reminder not explained:\n%s", seen)
	}
}

func TestScheduleNotesFollowTheDisplayLanguage(t *testing.T) {
	prompt := phasePrompt("schedule", Skill{ID: "mei"}, nil, scheduleInstructions+scheduleNoteLanguageRule("en"))
	if !strings.Contains(prompt, scheduleNoteLanguageRule("en")) || !tidiesUp(scheduleInstructions+scheduleNoteLanguageRule("en")) == tidiesUp(scheduleInstructions) {
		t.Fatalf("schedule note language rule missing or wake kind changed:\n%s", prompt)
	}
	if scheduleNoteLanguageRule("") != "" {
		t.Fatal("a thread without a recorded language should add no rule")
	}
}
