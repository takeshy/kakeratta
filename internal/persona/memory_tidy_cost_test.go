package persona

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRetainedPastEventSkipsRecheckUntilEdited(t *testing.T) {
	r, s, f := setup(t, answering, "gourmet")
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	event := fact{ID: "f_event", Text: "2026-10-01 にA店で食事した", Since: day.AddDate(0, 0, -7), From: []string{"e_a"}}
	ff := memo{Facts: []fact{event}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.summarize = func(_, user string) string {
		calls++
		if strings.Count(user, event.Text) != 1 {
			t.Errorf("event should appear once: %s", user)
		}
		return `{"invalidate":[],"retain":["f_event"]}`
	}
	client, settings := r.clientOf()
	skill := Builtins[0].forTurn()
	rewrite := func(at time.Time) {
		t.Helper()
		if _, err := r.tidyMemory(context.Background(), client, settings.MiddleModel, skill, at, time.UTC); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(day)
	stored := r.loadMemo("gourmet", time.UTC)
	if len(stored.Facts) != 1 || stored.Facts[0].PastReviewed != stored.Facts[0].reviewFingerprint(time.UTC) {
		t.Fatalf("retention not persisted: %+v", stored)
	}
	rewrite(day.AddDate(0, 0, 1))
	if calls != 1 {
		t.Fatalf("unchanged event rechecked: %d calls", calls)
	}
	event.Text = "2026-10-02 にB店で食事した"
	if err := r.EditFact(context.Background(), skill.ID, event.ID, event.Text); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(_, user string) string {
		calls++
		if strings.Count(user, event.Text) != 1 || !strings.Contains(user, "日付の審査対象") {
			t.Errorf("edited event missing: %s", user)
		}
		return `{"invalidate":[],"retain":["f_event"]}`
	}
	rewrite(day.AddDate(0, 0, 2))
	rewrite(day.AddDate(0, 0, 3))
	if calls != 2 {
		t.Fatalf("edited event should be checked once: %d calls", calls)
	}
}

func TestUncertainOrInvalidPastEventDecisionIsNotCached(t *testing.T) {
	for _, response := range []string{
		`{"invalidate":[],"retain":[]}`,
		`{"invalidate":["f_event"],"retain":["f_event"]}`,
		`{"invalidate":[],"retain":["f_unknown"]}`,
	} {
		t.Run(response, func(t *testing.T) {
			r, s, f := setup(t, answering, "gourmet")
			day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
			ff := memo{Facts: []fact{{ID: "f_event", Text: "2026-10-01 の件", Since: day.AddDate(0, 0, -7)}}}
			if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.summarize = func(_, _ string) string { calls++; return response }
			client, settings := r.clientOf()
			for i := 0; i < 2; i++ {
				_, err := r.tidyMemory(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day.AddDate(0, 0, i), time.UTC)
				if (err != nil) != strings.Contains(response, `"retain":["f_`) {
					t.Fatalf("unexpected result: %v", err)
				}
			}
			stored := r.loadMemo("gourmet", time.UTC)
			if calls < 2 || len(stored.Facts) != 1 || stored.Facts[0].PastReviewed != "" {
				t.Fatalf("uncertain/invalid retention saved: %+v, calls=%d", stored, calls)
			}
		})
	}
}

func TestTidyFactsAreSentOnceWithChangeAttributes(t *testing.T) {
	r, s, f := setup(t, answering, "gourmet")
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	ff := memo{TidyDirty: true,
		Facts: []fact{{ID: "f_new", Text: "2026-10-07 にA店で食事", Since: day.AddDate(0, 0, -1)}},
	}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(_, user string) string {
		if strings.Count(user, ff.Facts[0].Text) != 1 {
			t.Errorf("duplicated fact: %s", user)
		}
		for _, attr := range []string{"事実や記録が変更されています", "日付の審査対象"} {
			if !strings.Contains(user, attr) {
				t.Errorf("missing attribute %s", attr)
			}
		}
		return `{"facts":[],"invalidate":[],"retain":["f_new"]}`
	}
	client, settings := r.clientOf()
	if _, err := r.tidyMemory(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day, time.UTC); err != nil {
		t.Fatal(err)
	}
}

func TestPastEventRetentionSurvivesSourcesAndZoneButNotWords(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)
	f := fact{ID: "f_a", Text: "2026-10-01 に食事した", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, tokyo), From: []string{"e_a"}}
	f.PastReviewed = f.reviewFingerprint(tokyo)
	text := memoText(memo{Facts: []fact{f}}, tokyo)
	if !strings.Contains(text, "- [f_a] "+f.Text+" from:e_a since:2026-10-01 reviewed:"+f.PastReviewed+"\n") {
		t.Fatalf("review not kept on the fact's line:\n%s", text)
	}
	if parsed := parseMemo(text, time.UTC); parsed.Facts[0].Text != f.Text || parsed.Facts[0].PastReviewed != parsed.Facts[0].reviewFingerprint(time.UTC) {
		t.Fatalf("review lost when read in another zone: %+v", parsed.Facts[0])
	}
	f.From = append(f.From, "e_b")
	if parsed := parseMemo(memoText(memo{Facts: []fact{f}}, tokyo), tokyo); parsed.Facts[0].PastReviewed == "" {
		t.Fatal("review lost when a source was added")
	}
	f.Text = "2026-10-02 に食事した"
	if parsed := parseMemo(memoText(memo{Facts: []fact{f}}, tokyo), tokyo); parsed.Facts[0].PastReviewed != "" {
		t.Fatal("stale review survived new words")
	}
}

func TestReconfirmedPastEventIsNotReviewedAgain(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	event := fact{ID: "f_event", Text: "2026-10-01 にA店で食事した", Since: day.AddDate(0, 0, -7), From: []string{"e_a"}}
	event.PastReviewed = event.reviewFingerprint(time.UTC)
	m := memo{Facts: []fact{event}}
	next, _, problems := planFacts(m, []factOp{{Op: "update", ID: event.ID, Text: event.Text}}, "e_new", day, time.UTC)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got := next.Facts[0]
	if strings.Join(got.From, ",") != "e_a,e_new" {
		t.Fatalf("source not added: %+v", got)
	}
	if len(pastDated(next, day)) != 0 {
		t.Fatal("a reconfirmed retained event is up for date review again")
	}
}
