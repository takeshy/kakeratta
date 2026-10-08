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
	ff := fieldFacts{Summary: "以前のまとめ", SummaryAt: day.AddDate(0, 0, -1), Current: []fact{event}}
	if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.summarize = func(_, user string) string {
		calls++
		if strings.Count(user, event.Text) != 1 {
			t.Errorf("event should appear once: %s", user)
		}
		return `{"summaries":[],"invalidate":[],"retain":["f_event"]}`
	}
	client, settings := r.clientOf()
	skill := Builtins[0].forTurn()
	rewrite := func(at time.Time) {
		t.Helper()
		if err := r.rewriteSummaries(context.Background(), client, settings.MiddleModel, skill, at, time.UTC); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(day)
	stored := r.loadFacts(skill, time.UTC).ByField["Preference"]
	if len(stored.Current) != 1 || stored.Current[0].PastReviewed != stored.Current[0].reviewFingerprint(time.UTC) {
		t.Fatalf("retention not persisted: %+v", stored)
	}
	rewrite(day.AddDate(0, 0, 1))
	if calls != 1 {
		t.Fatalf("unchanged event rechecked: %d calls", calls)
	}
	event.Text = "2026-10-02 にB店で食事した"
	if err := r.EditFact(context.Background(), skill.ID, "Preference", event.ID, event.Text); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(_, user string) string {
		calls++
		if strings.Count(user, event.Text) != 1 || !strings.Contains(user, "日付の過ぎた事実") {
			t.Errorf("edited event missing: %s", user)
		}
		return `{"summaries":[{"field":"Preference","text":"B店で食事した"}],"invalidate":[],"retain":["f_event"]}`
	}
	rewrite(day.AddDate(0, 0, 2))
	rewrite(day.AddDate(0, 0, 3))
	if calls != 2 {
		t.Fatalf("edited event should be checked once: %d calls", calls)
	}
}

func TestUncertainOrInvalidPastEventDecisionIsNotCached(t *testing.T) {
	for _, response := range []string{
		`{"summaries":[],"invalidate":[],"retain":[]}`,
		`{"summaries":[],"invalidate":["f_event"],"retain":["f_event"]}`,
		`{"summaries":[],"invalidate":[],"retain":["f_unknown"]}`,
	} {
		t.Run(response, func(t *testing.T) {
			r, s, f := setup(t, answering, "gourmet")
			day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
			ff := fieldFacts{Summary: "保留", SummaryAt: day.AddDate(0, 0, -1), Current: []fact{{ID: "f_event", Text: "2026-10-01 の件", Since: day.AddDate(0, 0, -7)}}}
			if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.summarize = func(_, _ string) string { calls++; return response }
			client, settings := r.clientOf()
			for i := 0; i < 2; i++ {
				err := r.rewriteSummaries(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day.AddDate(0, 0, i), time.UTC)
				if (err != nil) != strings.Contains(response, `"retain":["f_`) {
					t.Fatalf("unexpected result: %v", err)
				}
			}
			stored := r.loadFacts(Builtins[0].forTurn(), time.UTC).ByField["Preference"]
			if calls < 2 || len(stored.Current) != 1 || stored.Current[0].PastReviewed != "" {
				t.Fatalf("uncertain/invalid retention saved: %+v, calls=%d", stored, calls)
			}
		})
	}
}

func TestSummaryFactsAreSentOnceWithChangeAttributes(t *testing.T) {
	r, s, f := setup(t, answering, "gourmet")
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	ff := fieldFacts{Summary: "古いまとめ", SummaryAt: day.AddDate(0, 0, -2), SummaryDirty: true,
		Current: []fact{{ID: "f_new", Text: "2026-10-07 にA店で食事", Since: day.AddDate(0, 0, -1)}},
		Past:    []fact{{ID: "f_old", Text: "以前はB店が好き", Until: day.AddDate(0, 0, -1), By: "f_new"}},
	}
	if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(_, user string) string {
		for _, text := range []string{ff.Current[0].Text, ff.Past[0].Text} {
			if strings.Count(user, text) != 1 {
				t.Errorf("duplicated fact %q: %s", text, user)
			}
		}
		for _, attr := range []string{"追加・変更", "日付の過ぎた事実", "当てはまらなくなった", "置換先:f_new"} {
			if !strings.Contains(user, attr) {
				t.Errorf("missing attribute %s", attr)
			}
		}
		return `{"summaries":[{"field":"Preference","text":"A店で食事した"}],"invalidate":[],"retain":["f_new"]}`
	}
	client, settings := r.clientOf()
	if err := r.rewriteSummaries(context.Background(), client, settings.MiddleModel, Builtins[0].forTurn(), day, time.UTC); err != nil {
		t.Fatal(err)
	}
}

func TestPastEventRetentionSurvivesSourcesAndZoneButNotWords(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)
	f := fact{ID: "f_a", Text: "2026-10-01 に食事した", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, tokyo), From: []string{"e_a"}}
	f.PastReviewed = f.reviewFingerprint(tokyo)
	text := factsText("Preference", fieldFacts{Current: []fact{f}}, tokyo)
	if !strings.Contains(text, "- [f_a] "+f.Text+" from:e_a since:2026-10-01 reviewed:"+f.PastReviewed+"\n") {
		t.Fatalf("review not kept on the fact's line:\n%s", text)
	}
	if parsed := parseFacts(text, time.UTC); parsed.Current[0].Text != f.Text || parsed.Current[0].PastReviewed != parsed.Current[0].reviewFingerprint(time.UTC) {
		t.Fatalf("review lost when read in another zone: %+v", parsed.Current[0])
	}
	f.From = append(f.From, "e_b")
	if parsed := parseFacts(factsText("Preference", fieldFacts{Current: []fact{f}}, tokyo), tokyo); parsed.Current[0].PastReviewed == "" {
		t.Fatal("review lost when a source was added")
	}
	f.Text = "2026-10-02 に食事した"
	if parsed := parseFacts(factsText("Preference", fieldFacts{Current: []fact{f}}, tokyo), tokyo); parsed.Current[0].PastReviewed != "" {
		t.Fatal("stale review survived new words")
	}
}

func TestReconfirmedPastEventIsNotReviewedAgain(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	event := fact{ID: "f_event", Text: "2026-10-01 にA店で食事した", Since: day.AddDate(0, 0, -7), From: []string{"e_a"}}
	event.PastReviewed = event.reviewFingerprint(time.UTC)
	m := memoryFacts{Fields: []string{"Preference"}, ByField: map[string]fieldFacts{"Preference": {Current: []fact{event}}}}
	next, _, problems := planFacts(m, []factOp{{Op: "update", ID: event.ID, Text: event.Text}}, "e_new", day, time.UTC)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got := next.ByField["Preference"].Current[0]
	if strings.Join(got.From, ",") != "e_a,e_new" {
		t.Fatalf("source not added: %+v", got)
	}
	if len(pastDated(next, day)["Preference"]) != 0 {
		t.Fatal("a reconfirmed retained event is up for date review again")
	}
}
