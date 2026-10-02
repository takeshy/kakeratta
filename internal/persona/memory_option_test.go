package persona

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestMemoryDisabledTurnsAnswerWithoutNotesOrCheckIns(t *testing.T) {
	for _, id := range []string{"support", "gourmet", "infographic"} {
		t.Run(id, func(t *testing.T) {
			ctx := context.Background()
			r, s, f := setup(t, func(string, string) string { return `{"deliver":["` + id + `"]}` }, id)
			skill, _ := r.catalogue.Find(ctx, id)
			if skill.Remembers() {
				skill.MemoryEnabled = memoryDisabled()
				raw, _ := json.Marshal(skill)
				if _, err := r.catalogue.Update(ctx, id, raw, nil); err != nil {
					t.Fatal(err)
				}
			}
			f.phaseReply = func(_, _ string, offered map[string]bool) string {
				switch {
				case offered["finish_read"]:
					return "read_done"
				case offered["submit_answer"]:
					return `{"text":"回答です。","sources":[]}`
				}
				t.Fatalf("unexpected phase: %v", offered)
				return ""
			}
			if r.memoryView(skill, time.UTC) != "" {
				t.Fatal("memory was handed to a memoryless persona")
			}
			r.react(post(t, s, "質問です").ID)
			if len(remarks(t, s)) != 1 {
				t.Fatal("the persona did not answer")
			}
			if len(f.keeps) != 0 || len(s.Memory(id).Notes) != 0 {
				t.Fatal("the answer wrote memory")
			}
			settings := s.Settings()
			now := time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC)
			next := r.nextCheckIn(ctx, now, settings)
			settings.Active = nil
			if !next.Equal(r.nextCheckIn(ctx, now, settings)) {
				t.Fatal("a memoryless persona added a check-in alarm")
			}
			day := now
			if day.Day() == store.SummaryDay(s.Root()) {
				day = day.AddDate(0, 0, 1)
			}
			wakes := s.Memory(id).Wakes
			r.dailyAt(ctx, day)
			if s.Memory(id).Wakes != wakes {
				t.Fatal("a memoryless persona checked in")
			}
		})
	}
}

func TestMemoryOptionDefaultsAndSurvivesSerialization(t *testing.T) {
	if !(Skill{}).Remembers() {
		t.Fatal("legacy definitions must still remember")
	}
	original := Skill{ID: "plain", MemoryEnabled: memoryDisabled()}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Skill
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Remembers() {
		t.Fatal("false was lost in JSON")
	}
}
