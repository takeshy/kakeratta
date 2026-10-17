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
			ff := memo{Summary: "豚骨派。辛さは平気。", SummaryAt: day.AddDate(0, 0, -2), Facts: []fact{
				{ID: "f_a", Text: "豚骨が好き", Since: day.AddDate(0, 0, -10), From: []string{"e_a"}},
				{ID: "f_b", Text: "辛さは平気", Since: day.AddDate(0, 0, -10), From: []string{"e_b"}},
			}}
			if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "edit":
				err = r.EditFact(context.Background(), "gourmet", "f_a", "塩が好き")
			case "forget":
				err = r.ForgetFact(context.Background(), "gourmet", "f_a")
			case "source-deletion":
				_, err = r.dropFactSources(context.Background(), "gourmet", map[string]bool{"e_a": true}, time.UTC)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := r.loadMemo("gourmet", time.UTC); !got.SummaryDirty || !tidyDue(got) || got.Summary == "" {
				t.Fatalf("correction was not persisted as dirty: %+v", got)
			}
			calls := 0
			f.summarize = func(_, user string) string {
				calls++
				if !strings.Contains(user, "必ず書き直す") {
					t.Error("missing correction instructions")
				}
				if calls == 1 {
					return `{"summary":null}`
				} // An omission must not clear the dirty marker.
				return `{"summary":"辛さは平気。"}`
			}
			client, settings := r.clientOf()
			if _, err := r.rewriteSummaries(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day, time.UTC); err != nil {
				t.Fatal(err)
			}
			ff = r.loadMemo("gourmet", time.UTC)
			if calls != 2 || ff.SummaryDirty || ff.Summary != "辛さは平気。" {
				t.Fatalf("rewritten = %+v; calls = %d", ff, calls)
			}
			if _, err := r.rewriteSummaries(context.Background(), nil, "", Builtins[0].forTurn(), day.AddDate(0, 0, 1), time.UTC); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestForgettingLastFactAlsoRemovesItsSummary(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	ff := memo{Summary: "消したい内容", Facts: []fact{{ID: "f_a", Text: "消したい内容"}}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(context.Background(), "gourmet", "f_a"); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.Note("gourmet", MemoPath); exists {
		t.Fatal("unsupported summary survived the last fact")
	}
}

func TestDirtyOrUnreadMemoryIsDue(t *testing.T) {
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		m    memo
		want bool
	}{
		"new record":     {memo{CheckedAt: day, Records: []record{{At: day.Add(time.Hour), ID: "e_new"}}}, true},
		"dirty":          {memo{Summary: "old", SummaryDirty: true, Facts: []fact{{ID: "f_a", Text: "corrected"}}}, true},
		"expiring":       {memo{CheckedAt: day, Records: []record{{At: day.Add(time.Hour), ID: "e_x", Expires: recordDate{Day: day.AddDate(0, 0, 1), Days: 1}}}}, false},
		"read already":   {memo{CheckedAt: day, Records: []record{{At: day.Add(-time.Hour), ID: "e_q"}}}, false},
		"never read yet": {memo{Records: []record{{At: day, ID: "e_n"}}}, true},
	} {
		if got := tidyDue(tc.m); got != tc.want {
			t.Errorf("%s: tidyDue = %v", name, got)
		}
	}
}
