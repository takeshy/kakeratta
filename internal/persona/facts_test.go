package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The keeper changes facts; it never rewrites a field. An update keeps what
// was replaced, with when and by what; an invalidated fact goes to the past;
// a merge carries every kakera its facts rested on; and a field from before
// the layout changed is shown, and its facts can be moved.
func TestFactsChangeOnlyAsAsked(t *testing.T) {
	zone := time.UTC
	day := time.Date(2026, 10, 2, 9, 0, 0, 0, zone)
	m := memoryFacts{Fields: []string{"Preference", "Shop"}, Orphans: []string{"Taste"}, ByField: map[string]fieldFacts{
		"Preference": {Current: []fact{
			{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}},
			{ID: "f_b", Text: "辛いのは平気", From: []string{"e_2"}},
			{ID: "f_c", Text: "辛さは控えめが好き", From: []string{"e_3"}},
		}},
		"Taste": {Current: []fact{{ID: "f_t", Text: "ごま強めは苦手", From: []string{"e_4"}}}},
	}}
	next, changed, problems := planFacts(m, []factOp{
		{Op: "update", ID: "f_a", Text: "最近は塩に寄る"},
		{Op: "invalidate", ID: "f_x"},
		{Op: "merge", Field: "Preference", IDs: []string{"f_b", "f_c"}, Text: "辛さはほどほどが好き"},
		{Op: "update", ID: "f_t", Field: "Preference", Text: "ごま強めは苦手"},
		{Op: "add", Field: "Shop", Text: "三田の二郎に行きたい"},
	}, "e_9", day, zone)
	if len(problems) != 1 || !strings.Contains(problems[0], "f_x はいまの事実にありません") {
		t.Fatalf("problems = %v", problems)
	}
	if strings.Join(changed, ",") != "Preference,Shop,Taste" {
		t.Fatalf("changed = %v", changed)
	}
	pref := next.ByField["Preference"]
	texts := map[string]fact{}
	for _, f := range pref.Current {
		texts[f.Text] = f
	}
	salt, merged, moved := texts["最近は塩に寄る"], texts["辛さはほどほどが好き"], texts["ごま強めは苦手"]
	if salt.ID == "" || strings.Join(salt.From, ",") != "e_1,e_9" {
		t.Fatalf("the update = %+v", salt)
	}
	if len(pref.Past) != 1 || pref.Past[0].ID != "f_a" || pref.Past[0].By != salt.ID || !pref.Past[0].Until.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, zone)) {
		t.Fatalf("the replaced fact = %+v", pref.Past)
	}
	if strings.Join(merged.From, ",") != "e_2,e_3" {
		t.Fatalf("the merge = %+v", merged)
	}
	if moved.ID != "f_t" || len(next.ByField["Taste"].Current) != 0 {
		t.Fatalf("the move = %+v, Taste = %+v", moved, next.ByField["Taste"])
	}
	if shop := next.ByField["Shop"].Current; len(shop) != 1 || strings.Join(shop[0].From, ",") != "e_9" {
		t.Fatalf("the add = %+v", shop)
	}

	// What is written reads back as it was.
	again := parseFacts(factsText("Preference", pref, zone), zone)
	if len(again.Current) != len(pref.Current) || len(again.Past) != 1 || again.Past[0].By != salt.ID || again.Current[0].Text != pref.Current[0].Text {
		t.Fatalf("round trip = %+v", again)
	}

	// A field can hold no more than its limit now; a merge is asked for.
	long := strings.Repeat("あ", maxFactRunes)
	var ops []factOp
	for i := 0; i <= maxFieldRunes/maxFactRunes; i++ {
		ops = append(ops, factOp{Op: "add", Field: "Shop", Text: long})
	}
	if _, _, problems := planFacts(m, ops, "e_9", day, zone); len(problems) != 1 || !strings.Contains(problems[0], "merge でまとめて") {
		t.Fatalf("an overfull field = %v", problems)
	}
}

// A kakera whose records the persona searches by its words is found through
// the timeline, however its record was worded.
func TestRecordsAreFoundByTheirKakera(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	entry := post(t, s, "神保町の担々麺はごまが強すぎた")
	remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: entry.ID, ReplyTo: entry.ID, Text: "ごま強めだったね", At: entry.At}
	if err := s.Append(remark); err != nil {
		t.Fatal(err)
	}
	rec := record{At: entry.At, ID: remark.ID, Of: entry.ID, Summary: "辛いものの感想を言った", Tags: []string{"感想"}}
	if err := s.WriteNote("gourmet", recordsPath(entry.At.In(time.UTC)), recordsText(entry.At.In(time.UTC).Format("2006-01"), []record{rec}, time.UTC)); err != nil {
		t.Fatal(err)
	}
	text, ids := r.readRecords(context.Background(), "gourmet", map[string]any{"query": "担々麺"}, time.UTC)
	if len(ids) != 1 || ids[0] != remark.ID || !strings.Contains(text, "辛いものの感想を言った") {
		t.Fatalf("records = %v\n%s", ids, text)
	}
}

// A field's summary is rewritten at the persona's check-in, before the turn
// reads it, from what changed among its facts - and only for a field where
// something did. It is not rewritten twice in a day.
func TestSummariesAreRewrittenAtTheCheckIn(t *testing.T) {
	var checkIn string
	r, s, f := setup(t, func(system, user string) string {
		if strings.Contains(system, "Daily check-in") {
			checkIn = user
		}
		return `{"action":"silent"}`
	}, "gourmet")
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	pref := fieldFacts{Summary: "豚骨派。", SummaryAt: old, Current: []fact{
		{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}, Since: old.AddDate(0, 0, -10)},
		{ID: "f_b", Text: "最近は塩に寄る", From: []string{"e_2"}, Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
	}}
	shop := fieldFacts{Summary: "二郎が気になっている。", SummaryAt: old, Current: []fact{{ID: "f_s", Text: "三田の二郎に行きたい", Since: old.AddDate(0, 0, -10)}}}
	for field, ff := range map[string]fieldFacts{"Preference": pref, "Shop": shop} {
		if err := s.WriteNote("gourmet", fieldPath(field), factsText(field, ff, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	f.summarize = func(system, user string) string {
		if !strings.Contains(user, "## Preference") || strings.Contains(user, "## Shop") || !strings.Contains(factLineOf(user, "最近は塩に寄る"), "追加・変更") || !strings.Contains(user, "豚骨派。") {
			t.Errorf("the rewrite was not handed the changed field alone:\n%s", user)
		}
		return `{"summaries":[{"field":"Preference","text":"豚骨派だったが、最近は塩に寄っている。"}]}`
	}
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "gourmet")
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("rewrites = %d", len(f.summaries))
	}
	m := r.loadFacts(Builtins[0].forTurn(), time.UTC)
	if got := m.ByField["Preference"]; got.Summary != "豚骨派だったが、最近は塩に寄っている。" || !got.SummaryAt.Equal(day) {
		t.Fatalf("Preference = %+v", got)
	}
	if got := m.ByField["Shop"]; got.Summary != "二郎が気になっている。" || !got.SummaryAt.Equal(old) {
		t.Fatalf("a field with nothing new was rewritten: %+v", got)
	}
	if !strings.Contains(checkIn, "最近は塩に寄っている") {
		t.Fatalf("the check-in did not read the new summary:\n%s", checkIn)
	}
	// The same day, nothing is rewritten again.
	if err := r.rewriteSummaries(context.Background(), nil, "", Builtins[0].forTurn(), store.CheckInCloses(day).Add(-time.Second), time.UTC); err != nil || len(f.summaries) != 1 {
		t.Fatalf("rewrote twice in a day: %v, %d", err, len(f.summaries))
	}
}

// A fact the user puts right keeps its id, its posts and its day; one the user
// forgets is gone, not moved to the past.
func TestAFactIsPutRightOrForgottenByTheUser(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	ff := fieldFacts{Current: []fact{
		{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}, Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "f_b", Text: "辛いのは平気", From: []string{"e_2"}},
	}, Past: []fact{{ID: "f_c", Text: "醤油派", From: []string{"e_0"}, Until: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}}}
	if err := s.WriteNote("gourmet", "Preference.md", factsText("Preference", ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.EditFact(ctx, "gourmet", "Preference", "f_a", "豚骨ラーメンに寄る"); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(ctx, "gourmet", "Preference", "f_c"); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(ctx, "gourmet", "Preference", "f_none"); err != ErrFactNotFound {
		t.Fatalf("forgetting a fact that is not there = %v", err)
	}
	got := r.loadFacts(Builtins[0].forTurn(), time.UTC).ByField["Preference"]
	if len(got.Current) != 2 || got.Current[0].ID != "f_a" || got.Current[0].Text != "豚骨ラーメンに寄る" || strings.Join(got.Current[0].From, ",") != "e_1" || len(got.Past) != 0 {
		t.Fatalf("facts = %+v", got)
	}
}

// A kakera the user lets go of - its record forgotten in a review, or expired
// - takes with it the facts that rested on it alone.
func TestARecordLetGoTakesItsFactsAlong(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	records := []record{
		{At: at, ID: "e_r", Of: "e_1", Summary: "二郎に行きたい"},
		{At: at, ID: "e_2", Summary: "今日のランチ", Expires: recordDate{Day: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Days: 1}},
	}
	if err := s.WriteNote("gourmet", "records/2026-09.md", recordsText("2026-09", records, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ff := fieldFacts{Current: []fact{
		{ID: "f_a", Text: "三田の二郎に行きたい", From: []string{"e_1"}},
		{ID: "f_b", Text: "神田でランチ", From: []string{"e_2"}},
		{ID: "f_c", Text: "ラーメン好き", From: []string{"e_1", "e_9"}},
	}}
	if err := s.WriteNote("gourmet", "Shop.md", factsText("Shop", ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	forgotten, err := r.changeRecords(ctx, "gourmet", []recordChange{{ID: "e_r", Forget: true}}, "", time.Now(), time.UTC)
	if err != nil || !forgotten["e_1"] {
		t.Fatalf("forgotten = %v, %v", forgotten, err)
	}
	if _, err := r.dropFactSourcesHeld(ctx, Builtins[0].forTurn(), forgotten, time.UTC); err != nil {
		t.Fatal(err)
	}
	r.expireRecords(ctx, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), s.Settings())
	got := r.loadFacts(Builtins[0].forTurn(), time.UTC).ByField["Shop"]
	if len(got.Current) != 1 || got.Current[0].ID != "f_c" || strings.Join(got.Current[0].From, ",") != "e_9" {
		t.Fatalf("facts = %+v", got)
	}
}

// A bullet carried over from prose is not part of the fact.
func TestAFactLosesTheBulletItCameWith(t *testing.T) {
	for in, want := range map[string]string{"- 侍女の物語を読んだ": "侍女の物語を読んだ", "・鍵を忘れた": "鍵を忘れた", "C# を学ぶ": "C# を学ぶ"} {
		if got := cleanFact(in); got != want {
			t.Fatalf("cleanFact(%q) = %q", in, got)
		}
	}
}

// A field still kept as prose reads as its summary: a fact added before it is
// split keeps the prose instead of writing over it.
func TestProseReadsAsTheSummaryUntilSplit(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	if err := s.WriteNote("gourmet", "Shop.md", "白楽くり山に行きたい。\n"); err != nil {
		t.Fatal(err)
	}
	f.phaseReply = answeringReply("")
	f.keep = func(system, user string) string {
		if !strings.Contains(user, "白楽くり山に行きたい。") {
			t.Errorf("the prose was not shown as the summary:\n%s", user)
		}
		return `{"summary":"二郎の話","tags":["ラーメン"],"expires":"","review":"","facts":[{"op":"add","field":"Shop","id":"","ids":[],"text":"三田の二郎に行きたい"}]}`
	}
	r.react(post(t, s, "三田の二郎も気になる").ID)
	got := r.loadFacts(Builtins[0].forTurn(), time.UTC).ByField["Shop"]
	if got.Summary != "白楽くり山に行きたい。" || len(got.Current) != 1 || got.Current[0].Text != "三田の二郎に行きたい" {
		t.Fatalf("Shop = %+v", got)
	}
}

// A fact names a day in any of the ways people write one, and has passed only
// when every day it names has.
func TestAFactsDaysArePastOrNot(t *testing.T) {
	today := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]bool{
		"2026-10-03 13:00 に美容室の予約": true,
		"10/3 に美容室":                true,
		"10月3日に美容室":                true,
		"2026年10月6日に会う":            false,
		"10/3 と 10/9 に通院":          false,
		"毎朝8:00に薬を飲む":              false,
		"一風堂のカタ麺が好き":               false,
	} {
		if got := datedPast(fact{Text: text, Since: since}, today); got != want {
			t.Errorf("datedPast(%q) = %v, want %v", text, got, want)
		}
	}
}

// At the check-in, a field whose facts did not change is still handed when a
// fact in it names a day that has gone: a plan whose day passed is retired,
// an event that happened stays, and the summary takes it in.
func TestPastPlansAreRetiredAtTheCheckIn(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), "routine")
	summaryDay := day.AddDate(0, 0, -1)
	errand := fieldFacts{Summary: "10/3 に美容室。9/30 に父へ贈り物。", SummaryAt: summaryDay, Current: []fact{
		{ID: "f_cut", Text: "2026-10-03 13:00 に美容室の予約", Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
		{ID: "f_gift", Text: "2026-09-30 に父へ生ハムを送った", Since: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
		{ID: "f_later", Text: "2026-12-01 に歯医者", Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
	}}
	if err := s.WriteNote("routine", "Errand.md", factsText("Errand", errand, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if !strings.Contains(user, "[f_cut]") || !strings.Contains(user, "[f_gift]") || strings.Contains(factLineOf(user, "2026-12-01 に歯医者"), "日付の過ぎた事実") || strings.Contains(user, "（まとめ文を書き直す分野）") {
			t.Errorf("the past facts were not handed alone:\n%s", user)
		}
		return `{"summaries":[{"field":"Errand","text":"9/30 に父へ贈り物をした。"}],"invalidate":["f_cut"]}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("rewrites = %d", len(f.summaries))
	}
	got := r.loadFacts(sonaSkill(t, r), time.UTC).ByField["Errand"]
	var current []string
	for _, f := range got.Current {
		current = append(current, f.ID)
	}
	if strings.Join(current, ",") != "f_gift,f_later" || len(got.Past) != 1 || got.Past[0].ID != "f_cut" || got.Summary != "9/30 に父へ贈り物をした。" {
		t.Fatalf("Errand = %+v", got)
	}
}

func sonaSkill(t *testing.T, r *Runner) Skill {
	t.Helper()
	skill, ok := r.catalogue.Find(context.Background(), "routine")
	if !ok {
		t.Fatal("no routine persona")
	}
	return skill.forTurn()
}

// factLineOf is the line of a summary request that hands the fact with text.
func factLineOf(user, text string) string {
	for _, line := range strings.Split(user, "\n") {
		if strings.HasPrefix(line, "- ") && strings.HasSuffix(line, "） "+text) {
			return line
		}
	}
	return ""
}
