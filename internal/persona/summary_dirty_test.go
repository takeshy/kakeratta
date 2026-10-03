package persona

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFactCorrectionsAndRemovalsRefreshOldSummaries(t *testing.T) {
	for _, mode := range []string{"edit", "forget", "source-deletion"} {
		t.Run(mode, func(t *testing.T) {
			r, s, f := setup(t, answering, "gourmet")
			day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
			ff := fieldFacts{Summary: "豚骨派。辛さは平気。", SummaryAt: day.AddDate(0, 0, -2), Current: []fact{
				{ID: "f_a", Text: "豚骨が好き", Since: day.AddDate(0, 0, -10), From: []string{"e_a"}},
				{ID: "f_b", Text: "辛さは平気", Since: day.AddDate(0, 0, -10), From: []string{"e_b"}},
			}}
			if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "edit":
				err = r.EditFact(context.Background(), "gourmet", "Preference", "f_a", "塩が好き")
			case "forget":
				err = r.ForgetFact(context.Background(), "gourmet", "Preference", "f_a")
			case "source-deletion":
				_, err = r.dropFactSources(context.Background(), Builtins[0].forTurn(), map[string]bool{"e_a": true}, time.UTC)
			}
			if err != nil {
				t.Fatal(err)
			}
			got := r.loadFacts(Builtins[0].forTurn(), time.UTC)
			if !got.ByField["Preference"].SummaryDirty {
				t.Fatal("correction was not persisted as dirty")
			}
			due, _ := staleFields(got, day)
			if len(due) != 1 {
				t.Fatalf("due = %v", due)
			}
			calls := 0
			f.summarize = func(_, user string) string {
				calls++
				if !strings.Contains(user, "事実が訂正・統合・削除されています") {
					t.Error("missing correction instructions")
				}
				if calls == 1 {
					return `{"summaries":[]}`
				} // An omission must not clear the dirty marker.
				return `{"summaries":[{"field":"Preference","text":"辛さは平気。"}]}`
			}
			client, settings := r.clientOf()
			if err := r.rewriteSummaries(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day, time.UTC); err != nil {
				t.Fatal(err)
			}
			ff = r.loadFacts(Builtins[0].forTurn(), time.UTC).ByField["Preference"]
			if calls != 2 || ff.SummaryDirty || ff.Summary != "辛さは平気。" {
				t.Fatalf("rewritten = %+v; calls = %d", ff, calls)
			}
			if err := r.rewriteSummaries(context.Background(), nil, "", Builtins[0].forTurn(), day.AddDate(0, 0, 1), time.UTC); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestForgettingLastFactAlsoRemovesItsSummary(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	ff := fieldFacts{Summary: "消したい内容", Current: []fact{{ID: "f_a", Text: "消したい内容"}}}
	if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(context.Background(), "gourmet", "Preference", "f_a"); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.Note("gourmet", "Preference.md"); exists {
		t.Fatal("unsupported summary survived the last fact")
	}
}

func TestDirtyOrphanSummaryIsDue(t *testing.T) {
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	m := memoryFacts{Fields: []string{"Preference"}, Orphans: []string{"Taste"}, ByField: map[string]fieldFacts{
		"Taste": {Summary: "old", SummaryAt: day.AddDate(0, 0, -1), SummaryDirty: true, Current: []fact{{ID: "f_a", Text: "corrected", Since: day.AddDate(0, 0, -10)}}},
	}}
	due, _ := staleFields(m, day)
	if len(due) != 1 || due[0] != "Taste" {
		t.Fatalf("orphan correction not due: %v", due)
	}
}
