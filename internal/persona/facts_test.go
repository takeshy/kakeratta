package persona

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestMemoryTidyDoesNotAskForSeparateRecordDeletion(t *testing.T) {
	skill := Skill{ID: "test", Name: "test"}
	for _, ids := range [][]string{nil, {"record"}} {
		tool := tidyTool(ids, nil, false)
		schema, err := json.Marshal(tool.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(schema), "drop_records") || strings.Contains(tidyPrompt(skill, false), "drop_records") {
			t.Fatal("memory tidying still requests a separate deletion list")
		}
	}
}

func TestFoldingRecordsDeletesAbsorbedRecords(t *testing.T) {
	for _, kind := range []string{"add", "update", "merge"} {
		t.Run(kind, func(t *testing.T) {
			r, s, f := setup(t, nil, "gourmet")
			now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			entry := appendAt(t, s, "最近は塩味を選ぶ", now.Add(-time.Hour))
			unfolded := appendAt(t, s, "週末の店はまだ検討中", now.Add(-2*time.Hour))
			m := memo{Facts: []fact{
				{ID: "f_a", Text: "塩味が好き", From: []string{"old-a"}},
				{ID: "f_b", Text: "あっさりした味が好き", From: []string{"old-b"}},
			}, Records: []record{
				{ID: "e_pending", Of: unfolded.ID, At: unfolded.At, Summary: unfolded.Text},
				{ID: "e_folded", Of: entry.ID, At: entry.At, Summary: entry.Text},
			}}
			if err := s.WriteNote("gourmet", MemoPath, memoText(m, time.UTC)); err != nil {
				t.Fatal(err)
			}
			op := tidyOp{factOp: factOp{Op: kind, Text: "あっさりした塩味を好む"}, Records: []string{"e_folded"}}
			if kind == "update" {
				op.ID = "f_a"
			}
			if kind == "merge" {
				op.IDs = []string{"f_a", "f_b"}
			}
			f.summarize = func(system, user string) string {
				body, _ := json.Marshal(map[string]any{"facts": []tidyOp{op}, "review_records": []string{}})
				return string(body)
			}
			skill, _ := r.catalogue.Find(context.Background(), "gourmet")
			client, _ := r.clientOf()
			if _, err := r.tidyMemory(context.Background(), client, "test", skill, now, time.UTC); err != nil {
				t.Fatal(err)
			}
			got := r.loadMemo("gourmet", time.UTC)
			if len(got.Records) != 1 || got.Records[0].ID != "e_pending" {
				t.Fatalf("folded record retained or unprocessed record lost: %+v", got.Records)
			}
			grounded := false
			for _, fact := range got.Facts {
				grounded = grounded || contains(fact.From, entry.ID)
			}
			if !grounded {
				t.Fatal("the folded record lost its fact's source link")
			}
			if _, ok := s.Entry(entry.ID); !ok {
				t.Fatal("the original timeline post was deleted")
			}
		})
	}
}

// Facts change only as asked. An update rewrites a fact in place and keeps its
// id; an invalidated fact is gone; a merge carries every kakera its facts
// rested on; and the facts may not grow past their limit.
func TestFactsChangeOnlyAsAsked(t *testing.T) {
	zone := time.UTC
	day := time.Date(2026, 10, 2, 9, 0, 0, 0, zone)
	m := memo{Facts: []fact{
		{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}},
		{ID: "f_b", Text: "辛いのは平気", From: []string{"e_2"}},
		{ID: "f_c", Text: "辛さは控えめが好き", From: []string{"e_3"}},
		{ID: "f_d", Text: "醤油派", From: []string{"e_0"}},
	}}
	next, changed, problems := planFacts(m, []factOp{
		{Op: "update", ID: "f_a", Text: "最近は塩に寄る"},
		{Op: "invalidate", ID: "f_x"},
		{Op: "invalidate", ID: "f_d"},
		{Op: "merge", IDs: []string{"f_b", "f_c"}, Text: "辛さはほどほどが好き"},
		{Op: "add", Text: "三田の二郎に行きたい"},
	}, "e_9", day, zone)
	if len(problems) != 1 || !strings.Contains(problems[0], "f_x はいまの事実にありません") {
		t.Fatalf("problems = %v", problems)
	}
	if !changed || !next.TidyDirty {
		t.Fatalf("changed = %v, %+v", changed, next)
	}
	texts := map[string]fact{}
	for _, f := range next.Facts {
		texts[f.Text] = f
	}
	salt, merged, added := texts["最近は塩に寄る"], texts["辛さはほどほどが好き"], texts["三田の二郎に行きたい"]
	if salt.ID != "f_a" || strings.Join(salt.From, ",") != "e_1,e_9" || !salt.Since.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, zone)) {
		t.Fatalf("the update = %+v", salt)
	}
	if len(next.Facts) != 3 || texts["豚骨に寄る"].ID != "" || texts["醤油派"].ID != "" {
		t.Fatalf("the replaced or invalidated facts were kept: %+v", next.Facts)
	}
	if strings.Join(merged.From, ",") != "e_2,e_3" {
		t.Fatalf("the merge = %+v", merged)
	}
	if strings.Join(added.From, ",") != "e_9" {
		t.Fatalf("the add = %+v", added)
	}

	// What is written reads back as it was.
	again := parseMemo(memoText(next, zone), zone)
	if len(again.Facts) != len(next.Facts) || again.Facts[0].Text != next.Facts[0].Text || !again.TidyDirty {
		t.Fatalf("round trip = %+v", again)
	}

	// The facts can hold no more than their limit; a merge is asked for.
	long := strings.Repeat("あ", maxFactRunes)
	var ops []factOp
	for i := 0; i <= maxFactsRunes/maxFactRunes; i++ {
		ops = append(ops, factOp{Op: "add", Text: long})
	}
	if _, _, problems := planFacts(m, ops, "e_9", day, zone); len(problems) != 1 || !strings.Contains(problems[0], "merge でまとめて") {
		t.Fatalf("overfull facts = %v", problems)
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
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo{Records: []record{rec}}, time.UTC)); err != nil {
		t.Fatal(err)
	}
	text, ids := r.readRecords(context.Background(), "gourmet", map[string]any{"query": "担々麺"}, time.UTC)
	if len(ids) != 1 || ids[0] != remark.ID || !strings.Contains(text, "辛いものの感想を言った") {
		t.Fatalf("records = %v\n%s", ids, text)
	}
}

// At the persona's check-in, before the turn reads its memory, the daily
// pass folds the records kept since it last looked into facts, deletes the
// records it folded, and hands a stale one to the user's review.
// With nothing new, it costs no call.
func TestMemoryIsTidiedAtTheCheckIn(t *testing.T) {
	var checkIn string
	r, s, f := setup(t, func(system, user string) string {
		if strings.Contains(system, "Daily check-in") {
			checkIn = user
		}
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "gourmet")
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	m := memo{CheckedAt: old.Add(time.Hour),
		Facts: []fact{{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}, Since: old}},
		Records: []record{
			{At: old, ID: "e_old", Summary: "白楽くり山に行きたい"},
			{At: day.Add(-2 * time.Hour), ID: "e_r", Of: "e_k", Summary: "最近は塩ばかり食べている"},
			{At: day.Add(-time.Hour), ID: "e_x", Summary: "今日のランチ", Expires: recordDate{Day: day.AddDate(0, 0, 1), Days: 1}},
		}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(m, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if !strings.Contains(user, "最近は塩ばかり食べている of:e_k（新規）") || strings.Contains(user, "白楽くり山に行きたい（新規）") ||
			strings.Contains(user, "今日のランチ") || !strings.Contains(user, "[f_a] 豚骨に寄る") {
			t.Errorf("the pass was not handed the new records with the facts:\n%s", user)
		}
		if strings.Contains(system, "suggest_persona") == false {
			t.Errorf("no new persona could be suggested:\n%s", system)
		}
		return `{"facts":[{"op":"update","id":"f_a","ids":[],"text":"以前は豚骨に寄っていたが、最近は塩に寄る","records":["e_r"]}],` +
			`"review_records":["e_old"],"suggest_persona":null}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("passes = %d", len(f.summaries))
	}
	got := r.loadMemo("gourmet", time.UTC)
	if got.TidyDirty || got.CheckedAt.IsZero() {
		t.Fatalf("Memo = %+v", got)
	}
	if len(got.Facts) != 1 || got.Facts[0].ID != "f_a" || got.Facts[0].Text != "以前は豚骨に寄っていたが、最近は塩に寄る" || strings.Join(got.Facts[0].From, ",") != "e_1,e_k" {
		t.Fatalf("facts = %+v", got.Facts)
	}
	if len(got.Records) != 2 || got.Records[0].ID != "e_old" || !got.Records[0].Review.Day.Equal(day) || got.Records[1].ID != "e_x" {
		t.Fatalf("records = %+v", got.Records)
	}
	if !strings.Contains(checkIn, "以前は豚骨に寄っていたが、最近は塩に寄る") {
		t.Fatalf("the check-in did not read the enriched fact:\n%s", checkIn)
	}
	// With nothing kept since, nothing is asked again.
	if _, err := r.tidyMemory(context.Background(), nil, "", Builtins[0].forTurn(), store.CheckInCloses(day).Add(-time.Second), time.UTC); err != nil || len(f.summaries) != 1 {
		t.Fatalf("tidied twice: %v, %d", err, len(f.summaries))
	}
}

// A record goes only once a fact rests on it, and a suggestion of a new
// persona reaches the check-in, which asks the user, and is not made again
// for a while.
func TestTidyingKeepsUnfoldedRecordsAndSuggestsAPersona(t *testing.T) {
	var checkIn, instructions string
	r, s, f := setup(t, func(system, user string) string {
		if strings.Contains(system, "Daily check-in") {
			checkIn, instructions = user, system
		}
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "gourmet")
	memo := memo{Records: []record{{At: day.Add(-time.Hour), ID: "e_r", Summary: "ぬか床を仕込んだ"}}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if len(f.summaries) == 1 {
			return `{"facts":[{"op":"add","text":"発酵食づくりに関心がある","records":["e_missing"]}],"review_records":[],"suggest_persona":null}`
		}
		if !strings.Contains(user, "記録 e_missing は渡していません") {
			t.Errorf("the unknown source record was not sent back:\n%s", user)
		}
		return `{"facts":[],"review_records":[],"suggest_persona":{"topic":"発酵食づくり","reason":"ぬか床の記録が続いている"}}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	got := r.loadMemo("gourmet", time.UTC)
	if len(got.Records) != 1 || !got.SuggestedAt.Equal(day) {
		t.Fatalf("Memo = %+v", got)
	}
	if !strings.Contains(checkIn, "Suggested new persona: 発酵食づくり\nWhy: ぬか床の記録が続いている") || !strings.Contains(instructions, "新しく作って分けませんか") {
		t.Fatalf("the check-in was not asked to suggest it:\n%s", checkIn)
	}
	// The persona stayed silent, yet the post that carries the buttons is out.
	said, _ := s.SpeakerEntries(context.Background(), "gourmet")
	if len(said) != 1 || said[0].Suggestion == nil || said[0].Suggestion.Status != timeline.SuggestionAsked ||
		said[0].Suggestion.Topic != "発酵食づくり" || !strings.Contains(said[0].Text, "発酵食づくり") {
		t.Fatalf("suggesting post = %+v", said)
	}
	if answered, err := r.AnswerSuggestion(said[0].ID, timeline.SuggestionDeclined, ""); err != nil || answered.Suggestion.Status != timeline.SuggestionDeclined {
		t.Fatalf("declined = %+v, %v", answered.Suggestion, err)
	}
	if _, err := r.AnswerSuggestion(said[0].ID, timeline.SuggestionCreated, "ferment"); !errors.Is(err, ErrNoSuggestion) {
		t.Fatalf("answered twice: %v", err)
	}
	got.Records = append(got.Records, record{At: got.CheckedAt.Add(time.Minute), ID: "e_s", Summary: "ぬか床をかき混ぜた"})
	if err := s.WriteNote("gourmet", MemoPath, memoText(got, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if strings.Contains(system, "suggest_persona") {
			t.Errorf("a new persona was offered again within a month")
		}
		return `{"facts":[],"review_records":[]}`
	}
	client, current := r.clientOf()
	if _, err := r.tidyMemory(context.Background(), client, personaModel(current), Builtins[0].forTurn(), got.CheckedAt.Add(time.Hour), time.UTC); err != nil || len(f.summaries) != 3 {
		t.Fatalf("the next pass = %v, %d", err, len(f.summaries))
	}
}

// A fact the user puts right keeps its id, its posts and its day; one the user
// forgets is gone.
func TestAFactIsPutRightOrForgottenByTheUser(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	ff := memo{Facts: []fact{
		{ID: "f_a", Text: "豚骨に寄る", From: []string{"e_1"}, Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "f_b", Text: "辛いのは平気", From: []string{"e_2"}},
		{ID: "f_c", Text: "醤油派", From: []string{"e_0"}},
	}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.EditFact(ctx, "gourmet", "f_a", "豚骨ラーメンに寄る"); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(ctx, "gourmet", "f_c"); err != nil {
		t.Fatal(err)
	}
	if err := r.ForgetFact(ctx, "gourmet", "f_none"); err != ErrFactNotFound {
		t.Fatalf("forgetting a fact that is not there = %v", err)
	}
	got := r.loadMemo("gourmet", time.UTC)
	if len(got.Facts) != 2 || got.Facts[0].ID != "f_a" || got.Facts[0].Text != "豚骨ラーメンに寄る" || strings.Join(got.Facts[0].From, ",") != "e_1" {
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
	ff := memo{Records: records, Facts: []fact{
		{ID: "f_a", Text: "三田の二郎に行きたい", From: []string{"e_1"}},
		{ID: "f_b", Text: "神田でランチ", From: []string{"e_2"}},
		{ID: "f_c", Text: "ラーメン好き", From: []string{"e_1", "e_9"}},
	}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	forgotten, err := r.changeRecords(ctx, "gourmet", []recordChange{{ID: "e_r", Forget: true}}, "", time.Now(), time.UTC)
	if err != nil || !forgotten["e_1"] {
		t.Fatalf("forgotten = %v, %v", forgotten, err)
	}
	if _, err := r.dropFactSourcesHeld(ctx, "gourmet", forgotten, time.UTC); err != nil {
		t.Fatal(err)
	}
	r.expireRecords(ctx, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), s.Settings())
	got := r.loadMemo("gourmet", time.UTC)
	if len(got.Facts) != 1 || got.Facts[0].ID != "f_c" || strings.Join(got.Facts[0].From, ",") != "e_9" {
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

// At the check-in, a field with nothing new is still handed when a fact in it
// names a day that has gone: a plan whose day passed is deleted, an event that
// happened stays.
func TestPastPlansAreRetiredAtTheCheckIn(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	settings := s.Settings()
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), "routine")
	errand := memo{Facts: []fact{
		{ID: "f_cut", Text: "2026-10-03 13:00 に美容室の予約", Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
		{ID: "f_gift", Text: "2026-09-30 に父へ生ハムを送った", Since: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
		{ID: "f_later", Text: "2026-12-01 に歯医者", Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
	}}
	if err := s.WriteNote("routine", MemoPath, memoText(errand, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.summarize = func(system, user string) string {
		if !strings.Contains(factLineOf(user, "2026-10-03 13:00 に美容室の予約"), "日付の審査対象") || !strings.Contains(factLineOf(user, "2026-09-30 に父へ生ハムを送った"), "日付の審査対象") || strings.Contains(factLineOf(user, "2026-12-01 に歯医者"), "日付の審査対象") || !strings.Contains(user, "（ありません）") {
			t.Errorf("the past facts were not handed alone:\n%s", user)
		}
		return `{"facts":[],"invalidate":["f_cut"],"retain":["f_gift"]}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(day).Add(-time.Minute))
	if len(f.summaries) != 1 {
		t.Fatalf("rewrites = %d", len(f.summaries))
	}
	got := r.loadMemo("routine", time.UTC)
	var current []string
	for _, f := range got.Facts {
		current = append(current, f.ID)
	}
	if strings.Join(current, ",") != "f_gift,f_later" || got.Facts[0].PastReviewed == "" {
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

// factLineOf is the line of a tidy request that hands the fact with text.
func factLineOf(user, text string) string {
	for _, line := range strings.Split(user, "\n") {
		if strings.HasPrefix(line, "- [f_") && strings.Contains(line, "] "+text+"（") {
			return line
		}
	}
	return ""
}

func TestTidyRejectsUnsupportedFactAndKeepsRecordUntilGrounded(t *testing.T) {
	r, s, f := setup(t, answering, "gourmet")
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	m := memo{Records: []record{{ID: "e_reply", Of: "e_post", At: day.Add(-time.Hour), Summary: "夜はカフェインを避ける"}}}
	before := memoText(m, time.UTC)
	if err := s.WriteNote("gourmet", MemoPath, before); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.summarize = func(system, user string) string {
		calls++
		if strings.Contains(system, "- summary:") || strings.Contains(user, "いまのまとめ文") {
			t.Fatal("daily pass still uses a prose summary")
		}
		if calls == 1 {
			return `{"facts":[{"op":"add","text":"夜はカフェインを避ける","records":[]}]}`
		}
		if !strings.Contains(user, "add には根拠の記録") {
			t.Error("missing explanation for unsupported fact")
		}
		note, _ := s.Note("gourmet", MemoPath)
		if note.Text != before {
			t.Error("rejected submission changed stored memory")
		}
		return `{"facts":[{"op":"add","text":"夜はカフェインを避ける","records":["e_reply"]}]}`
	}
	client, settings := r.clientOf()
	if _, err := r.tidyMemory(context.Background(), client, personaModel(settings), Builtins[0].forTurn(), day, time.UTC); err != nil {
		t.Fatal(err)
	}
	got := r.loadMemo("gourmet", time.UTC)
	if calls != 2 || len(got.Records) != 0 || len(got.Facts) != 1 || strings.Join(got.Facts[0].From, ",") != "e_post" {
		t.Fatalf("grounded memory = %+v, calls = %d", got, calls)
	}
	note, _ := s.Note("gourmet", MemoPath)
	if strings.Contains(note.Text, "まとめ") || strings.Contains(note.Text, "summary") {
		t.Fatalf("summary persisted: %s", note.Text)
	}
}

func TestFactsUseTheFormerSummaryBudget(t *testing.T) {
	var ops []factOp
	for i := 0; i < 20; i++ {
		ops = append(ops, factOp{Op: "add", Text: strings.Repeat("あ", 200)})
	}
	m, _, problems := planFacts(memo{}, ops, "e_source", time.Now(), time.UTC)
	if len(problems) != 0 || factsRunes(m.Facts) != 4000 {
		t.Fatalf("facts cannot use the 4000-character budget: %v", problems)
	}
	if _, _, problems := planFacts(m, []factOp{{Op: "add", Text: "一字でも超える"}}, "e_source", time.Now(), time.UTC); len(problems) == 0 {
		t.Fatal("facts grew past 4000 characters")
	}
}

func TestTidyToolDoesNotRequestSummary(t *testing.T) {
	tool := tidyTool([]string{"e_record"}, []string{"f_past"}, true)
	props := tool.Parameters["properties"].(map[string]any)
	if _, exists := props["summary"]; exists {
		t.Fatal("daily pass still offers summary generation")
	}
	for _, key := range tool.Parameters["required"].([]string) {
		if key == "summary" {
			t.Fatal("daily pass still requires a summary")
		}
	}
}
