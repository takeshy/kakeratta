package persona

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// answeringReply answers every post with text and plans nothing else.
func answeringReply(text string) func(system, user string, offered map[string]bool) string {
	return func(system, user string, offered map[string]bool) string {
		switch {
		case offered["submit_answer"]:
			return `{"text":"` + text + `","sources":[]}`
		}
		return `{"operations":[],"consistent":true,"reason":"約束なし"}`
	}
}

// The persona's reply is what a record leads to when there is one; the record
// carries the sentence and the tags the keeper chose, and the facts are left
// for the daily pass.
func TestAnAnsweredKakeraIsRecordedUnderTheReply(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	if err := s.WriteNote("gourmet", MemoPath, memoFile([2]string{"A店に行きたい", ""})); err != nil {
		t.Fatal(err)
	}
	f.phaseReply = answeringReply("二郎、いいね。")
	f.keep = func(system, user string) string {
		if strings.Contains(user, "A店に行きたい") || !strings.Contains(user, "三田の二郎") {
			t.Errorf("the keeper was shown the facts, or not the kakera:\n%s", user)
		}
		return `{"remember":true,"summary":"三田の二郎に行きたいと言った","tags":["ラーメン","#三田"],"expires":"","review":""}`
	}
	entry := post(t, s, "三田の二郎に行ってみたい")
	r.react(entry.ID)

	said := remarks(t, s)
	if len(said) != 1 {
		t.Fatalf("remarks = %+v", said)
	}
	if m := r.loadMemo("gourmet", time.UTC); len(m.Facts) != 1 || m.Facts[0].Text != "A店に行きたい" || m.TidyDirty {
		t.Fatalf("the facts were touched: %+v", m)
	}
	found, _ := r.findRecords("gourmet", "", "", "", 10, time.UTC)
	if len(found) != 1 {
		t.Fatalf("records = %+v", found)
	}
	got := found[0]
	if got.ID != said[0].ID || got.Summary != "三田の二郎に行きたいと言った" || strings.Join(got.Tags, ",") != "ラーメン,三田" {
		t.Fatalf("record = %+v, want it under the reply %s", got, said[0].ID)
	}
	// The reply leads back to the kakera.
	if reply, _ := s.Entry(got.ID); reply.ReplyTo != entry.ID {
		t.Fatalf("the reply does not lead to the kakera: %+v", reply)
	}
	if text := r.recordEntry(got.ID, time.UTC); !strings.Contains(text, "三田の二郎に行ってみたい") || !strings.Contains(text, "二郎、いいね。") {
		t.Fatalf("reading the record's entry = %s", text)
	}
	if records, _ := s.Note("gourmet", MemoPath); !strings.Contains(records.Text, "(/?entry="+got.ID+")") {
		t.Fatalf("the records file does not link the entry:\n%s", records.Text)
	}
}

// A kakera the persona did not answer is recorded under its own id, and an
// edit of it replaces its record rather than adding a second.
func TestASilentKakeraIsRecordedUnderItselfOnce(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("")
	summary := "雨の日はうどん"
	f.keep = func(system, user string) string {
		return `{"remember":true,"summary":"` + summary + `","tags":["うどん"],"expires":"","review":""}`
	}
	entry := post(t, s, "雨の日はうどんにする")
	r.react(entry.ID)
	found, _ := r.findRecords("gourmet", "", "", "", 10, time.UTC)
	if len(found) != 1 || found[0].ID != entry.ID {
		t.Fatalf("records = %+v", found)
	}
	summary = "雨の日は温かいそば"
	trigger, _ := s.Entry(entry.ID)
	if err := r.appendRecord(context.Background(), "gourmet", record{At: trigger.At, ID: trigger.ID, Summary: summary}, trigger, s.Settings().Location()); err != nil {
		t.Fatal(err)
	}
	found, _ = r.findRecords("gourmet", "", "", "", 10, time.UTC)
	if len(found) != 1 || found[0].Summary != summary {
		t.Fatalf("records after the edit = %+v", found)
	}
}

// A record the app cannot keep - no sentence, no tags, a date gone by - is
// sent back with the reasons, and what passes is kept.
func TestTheKeeperIsSentBackUntilItFits(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("いいね。")
	calls := 0
	f.keep = func(system, user string) string {
		calls++
		if calls == 1 {
			return `{"remember":true,"summary":"","tags":[],"expires":"2020-01-01","review":""}`
		}
		for _, want := range []string{"summary がありません", "tags は1〜", "expires は明日以降"} {
			if !strings.Contains(user, want) {
				t.Errorf("the keeper was not told %q:\n%s", want, user)
			}
		}
		return `{"remember":true,"summary":"店の話","tags":["店"],"expires":"","review":""}`
	}
	r.react(post(t, s, "店の話").ID)
	if calls != 2 {
		t.Fatalf("keeper calls = %d", calls)
	}
	memo, _ := s.Note("gourmet", MemoPath)
	ff := parseMemo(memo.Text, time.UTC)
	if len(ff.Facts) != 0 || len(ff.Records) != 1 {
		t.Fatalf("field = %q", memo.Text)
	}
}

// A memory that cannot be kept costs the memory, never the answer, and is
// kept for a later retry.
func TestAKeeperThatFailsCostsOnlyTheMemory(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("駅から5分の店です。")
	f.keep = func(string, string) string { return `{"summary":"","tags":[],"facts":[]}` }
	r.react(post(t, s, "ラーメン屋を教えて").ID)
	if got := remarks(t, s); len(got) != 1 || got[0].Text != "駅から5分の店です。" {
		t.Fatalf("the answer was lost with the memory: %+v", got)
	}
	if paths, _ := s.NotePaths("gourmet"); len(paths) != 0 {
		t.Fatalf("memory refused by the app was written: %v", paths)
	}
	if jobs, _ := s.NoteJobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
}

// The turn is handed its fields whole and its latest records, and finds older
// ones by tag.
func TestATurnIsHandedItsMemory(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	if err := s.WriteNote("gourmet", MemoPath, memoFile([2]string{"豚骨に寄る", ""})); err != nil {
		t.Fatal(err)
	}
	old := timeline.Entry{ID: "e_old", Text: "前の二郎", At: time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)}
	writeRecord(t, s, "gourmet", old, old.ID)
	var answered string
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["read"] && answered == "":
			if answered == "" {
				answered = user
				return `{"steps":[{"type":"records","tool":"","args":"{\"tag\":\"テスト\"}"}]}`
			}
			return "read_done"
		case offered["submit_answer"]:
			if !strings.Contains(user, "前の二郎") {
				t.Errorf("the records read did not come back:\n%s", user)
			}
			return `{"text":"","sources":[]}`
		}
		return `{"operations":[],"consistent":true,"reason":"約束なし"}`
	}
	r.react(post(t, s, "今日も二郎").ID)
	for _, want := range []string{"- [f_t0] 豚骨に寄る", "[e_old] 前の二郎 #テスト"} {
		if !strings.Contains(answered, want) {
			t.Fatalf("the turn was not handed %q:\n%s", want, answered)
		}
	}
}

// Only a kakera of the user's, or the tidy-up after one was deleted, leaves the
// keeper something to do.
func TestOnlyTurnsThatLeaveSomethingToKeepRunTheKeeper(t *testing.T) {
	cases := []struct {
		name         string
		trigger      timeline.Entry
		instructions string
		want         bool
	}{
		{"a post of the user's", timeline.Entry{ID: "p", Speaker: timeline.User}, "", true},
		{"a deletion tidy-up", timeline.Entry{Speaker: "system"}, deletedInstructions, false},
		{"a check-in with reviews", timeline.Entry{Speaker: "system"}, dailyShareInstructions + reviewInstructions, true},
		{"the daily check-in", timeline.Entry{Speaker: "system"}, dailyShareInstructions, false},
		{"a schedule waking", timeline.Entry{Speaker: "system"}, scheduleInstructions, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepsNotes(tc.trigger, tc.instructions); got != tc.want {
				t.Fatalf("keepsNotes = %v, want %v", got, tc.want)
			}
		})
	}
}

// A record line reads back as it was written, tags and all, and a sentence
// holding a "#" does not grow a tag.
func TestRecordLinesRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 34, 0, 0, time.UTC)
	rec := record{At: at, ID: "e_1", Summary: cleanSummary("C# の 本を\n買った"), Tags: []string{"本", "C"}}
	text := memoText(memo{Records: []record{rec}}, time.UTC)
	if !strings.Contains(text, "- 2026-10-01 12:34 [e_1](/?entry=e_1) C＃ の 本を 買った #本 #C") {
		t.Fatalf("text = %s", text)
	}
	back := parseRecords(text, time.UTC)
	if len(back) != 1 || back[0].Summary != rec.Summary || strings.Join(back[0].Tags, ",") != "本,C" || !back[0].At.Equal(at) {
		t.Fatalf("back = %+v", back)
	}
	if !RecordsTagged(MemoPath, text, "#本") || RecordsTagged("index.md", text, "本") {
		t.Fatal("tag filter")
	}
	dated := rec
	dated.Expires = dateFrom("2026-10-02", at, time.UTC)
	dated.Review = recordDate{Day: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Days: 31}
	dated.Asked = "e_ask"
	line := dated.fileLine(time.UTC)
	if !strings.HasSuffix(line, "#本 #C expires:2026-10-02/1d review:2026-11-01/31d asked:e_ask") {
		t.Fatalf("line = %s", line)
	}
	again := parseRecords(line, time.UTC)
	if len(again) != 1 || again[0].Summary != rec.Summary || again[0].Expires != dated.Expires || again[0].Review != dated.Review || again[0].Asked != "e_ask" || strings.Join(again[0].Tags, ",") != "本,C" {
		t.Fatalf("again = %+v", again)
	}
}

// A persona that stays quiet because the post is not its business keeps
// nothing, and the routing sheet records why; one that stays quiet with
// nothing to say still has the kakera recorded.
func TestAnUnrelatedSilenceKeepsNothingAndIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unrelated bool
	}{{"unrelated", true}, {"related", false}} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				switch {
				case offered["submit_answer"]:
					if tc.unrelated {
						return `{"text":"","sources":[],"unrelated":true}`
					}
					return `{"text":"","sources":[],"unrelated":false}`
				}
				return `{"operations":[],"consistent":true,"reason":"約束なし"}`
			}
			entry := post(t, s, "明日は雨らしい")
			r.react(entry.ID)
			record, _ := s.Routing(entry.ID)
			if record == nil {
				t.Fatal("no routing record")
			}
			if tc.unrelated {
				if len(f.keeps) != 0 || !contains(record.Unrelated, "gourmet") {
					t.Fatalf("keeper runs = %d, unrelated = %v", len(f.keeps), record.Unrelated)
				}
				return
			}
			if len(f.keeps) == 0 || len(record.Unrelated) != 0 {
				t.Fatalf("keeper runs = %d, unrelated = %v", len(f.keeps), record.Unrelated)
			}
		})
	}
}

// Only a silence can be unrelated: a persona that speaks is taken as having
// something to say about it.
func TestOnlyASilenceIsUnrelated(t *testing.T) {
	answer, err := ParseAnswer([]byte(`{"text":"雨ですね","sources":[],"unrelated":true}`))
	if err != nil || answer.Unrelated {
		t.Fatalf("answer = %+v, %v", answer, err)
	}
	answer, err = ParseAnswer([]byte(`{"text":"","sources":[],"unrelated":true}`))
	if err != nil || !answer.Unrelated {
		t.Fatalf("answer = %+v, %v", answer, err)
	}
	// An agent that does not know the field is still understood.
	if _, err := ParseAnswer([]byte(`{"text":"","sources":[]}`)); err != nil {
		t.Fatal(err)
	}
}

// The keeper dates a record from what it is, and the dates read back.
func TestTheKeeperDatesARecord(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	f.phaseReply = answeringReply("")
	review := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	calls := 0
	f.keep = func(system, user string) string {
		calls++
		if !strings.Contains(system, "review は、ユーザーから聞いたことのうち") {
			t.Error("the keeper was not told how to date a record")
		}
		if calls == 1 {
			return `{"summary":"二郎に行きたい","tags":["ラーメン"],"expires":"2020-01-01","review":"","facts":[]}`
		}
		return `{"summary":"二郎に行きたい","tags":["ラーメン"],"expires":"","review":"` + review + `","facts":[]}`
	}
	r.react(post(t, s, "二郎に行きたい").ID)
	if calls != 2 {
		t.Fatalf("a date in the past was not sent back: calls = %d", calls)
	}
	found, _ := r.findRecords("gourmet", "", "", "", 10, s.Settings().Location())
	if len(found) != 1 || found[0].Review.Day.Format("2006-01-02") != review || found[0].Expires.set() || found[0].Review.Days < 20 {
		t.Fatalf("records = %+v", found)
	}
}

// A record read while answering a post gets the span it was given afresh;
// one only handed over, or read by a check-in, does not.
func TestARecordReadForAPostIsExtended(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	zone := s.Settings().Location()
	at := time.Now().AddDate(0, 0, -20)
	old := record{At: at, ID: "e_old", Summary: "草加の店に行きたい", Tags: []string{"草加"},
		Review: recordDate{Day: time.Date(at.Year(), at.Month(), at.Day()+21, 0, 0, 0, 0, zone), Days: 21}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo{Records: []record{old}}, zone)); err != nil {
		t.Fatal(err)
	}
	asked := false
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["read"] && !asked:
			if !asked {
				asked = true
				return `{"steps":[{"type":"records","tool":"","args":"{\"tag\":\"草加\"}"}]}`
			}
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"草加の店ですね。","sources":[]}`
		}
		return `{"operations":[],"consistent":true,"reason":"約束なし"}`
	}
	r.react(post(t, s, "草加に行く").ID)
	found := r.recordsByID("gourmet", map[string]bool{"e_old": true}, zone)
	today := time.Now().In(zone)
	want := time.Date(today.Year(), today.Month(), today.Day()+21, 0, 0, 0, 0, zone)
	if len(found) != 1 || !found[0].Review.Day.Equal(want) || found[0].Review.Days != 21 {
		t.Fatalf("the read record was not extended: %+v, want %s", found, want)
	}
}

// A record due for review is handed to the check-in, which asks about it; the
// keeper puts it off a week and marks the post that asked. The user's reply
// to that post settles it.
func TestAReviewIsAskedAndSettledByTheReply(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	settings := s.Settings()
	settings.DailyCheckIns = map[string]store.DailyCheckIn{"gourmet": {Talk: store.CheckInMinimal}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	zone := s.Settings().Location()
	at := time.Now().AddDate(0, 0, -30)
	due := record{At: at, ID: "e_due", Summary: "草加の店に行きたい", Tags: []string{"草加"},
		Review: recordDate{Day: time.Date(at.Year(), at.Month(), at.Day()+21, 0, 0, 0, 0, zone), Days: 21}}
	if err := s.WriteNote("gourmet", MemoPath, memoText(memo{Records: []record{due}}, zone)); err != nil {
		t.Fatal(err)
	}
	week := time.Now().AddDate(0, 0, 7).In(zone).Format("2006-01-02")
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["submit_answer"]:
			if strings.Contains(user, "Review due") {
				if !strings.Contains(user, "[e_due] 草加の店に行きたい") {
					t.Errorf("the due record was not handed over:\n%s", user)
				}
				return `{"text":"そろそろ忘れていい？ 草加の店の話","sources":[]}`
			}
			return `{"text":"了解、忘れるね。","sources":[]}`
		}
		return `{"operations":[],"consistent":true,"reason":"約束なし"}`
	}
	f.keep = func(system, user string) string {
		if strings.Contains(system, "チェックインで、見直しの日が来た記録") {
			return `{"facts":[],"records":[{"id":"e_due","expires":"","review":"` + week + `","forget":false}]}`
		}
		if !strings.Contains(system, "そろそろ忘れていい？」と聞いた投稿への返事") || !strings.Contains(user, "[e_due]") {
			t.Errorf("the keeper of the reply was not handed the asked record:\n%s\n%s", system, user)
		}
		return `{"summary":"忘れていいと言った","tags":["草加"],"expires":"","review":"","facts":[],"records":[{"id":"e_due","expires":"","review":"","forget":true}]}`
	}
	r.dailyAt(context.Background(), store.CheckInCloses(checkInDay(s, time.Now().UTC(), "gourmet")).Add(-time.Minute))
	var said []timeline.Entry
	entries, _ := s.Timeline()
	for _, entry := range entries {
		if entry.Speaker == "gourmet" {
			said = append(said, entry)
		}
	}
	if len(said) != 1 {
		t.Fatalf("the check-in did not ask: %+v", said)
	}
	asked := r.recordsByID("gourmet", map[string]bool{"e_due": true}, zone)
	if len(asked) != 1 || asked[0].Asked != said[0].ID || asked[0].Review.Day.Format("2006-01-02") != week {
		t.Fatalf("the asked record = %+v", asked)
	}
	if post, _ := s.Entry(said[0].ID); post.Review != timeline.ReviewAsked {
		t.Fatalf("the post that asked does not offer the answers: %+v", post)
	}

	reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindReply, Speaker: timeline.User, Root: said[0].Root, ReplyTo: said[0].ID, Text: "もういいよ", At: time.Now()}
	if err := s.Append(reply); err != nil {
		t.Fatal(err)
	}
	r.react(reply.ID)
	if left := r.recordsByID("gourmet", map[string]bool{"e_due": true}, zone); len(left) != 0 {
		t.Fatalf("the record the user let go is still kept: %+v", left)
	}
	if post, _ := s.Entry(said[0].ID); post.Review != timeline.ReviewReplied {
		t.Fatalf("an answer in words did not settle the buttons: %+v", post)
	}
}

// The buttons under a post that asked settle its records without a model:
// forget deletes them and the facts resting on them alone, later puts the
// review off by its span (a week at least), keep clears it. A post not asking
// takes no answer.
func TestAReviewIsSettledByItsButtons(t *testing.T) {
	for _, choice := range []string{timeline.ReviewForget, timeline.ReviewLater, timeline.ReviewKeep} {
		t.Run(choice, func(t *testing.T) {
			r, s, f := setup(t, answering, "gourmet")
			zone := s.Settings().Location()
			kakera := post(t, s, "草加の店に行きたい")
			ask := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: "gourmet", Text: "そろそろ忘れていい？ 草加の店の話", At: time.Now(), Review: timeline.ReviewAsked}
			ask.Root = ask.ID
			if err := s.Append(ask); err != nil {
				t.Fatal(err)
			}
			today := localDay(time.Now(), zone)
			m := memo{
				Facts: []fact{{ID: "f_shop", Text: "草加の店に行きたい", From: []string{kakera.ID}}},
				Records: []record{
					{At: kakera.At, ID: kakera.ID, Summary: "草加の店に行きたい", Review: recordDate{Day: today.AddDate(0, 0, 7), Days: 21}, Asked: ask.ID},
					{At: kakera.At, ID: "e_other", Summary: "別の話", Review: recordDate{Day: today.AddDate(0, 0, 3), Days: 3}},
				},
			}
			if err := s.WriteNote("gourmet", MemoPath, memoText(m, zone)); err != nil {
				t.Fatal(err)
			}
			got, err := r.AnswerReview(context.Background(), ask.ID, choice)
			if err != nil || got.Review != choice {
				t.Fatalf("answer = %+v, %v", got, err)
			}
			if stored, _ := s.Entry(ask.ID); stored.Review != choice {
				t.Fatalf("the post does not keep the answer: %+v", stored)
			}
			after := r.loadMemo("gourmet", zone)
			var asked *record
			for i := range after.Records {
				if after.Records[i].ID == kakera.ID {
					asked = &after.Records[i]
				}
			}
			switch choice {
			case timeline.ReviewForget:
				if asked != nil || len(after.Facts) != 0 || len(after.Records) != 1 {
					t.Fatalf("forget kept %+v", after)
				}
			case timeline.ReviewLater:
				if asked == nil || asked.Asked != "" || !asked.Review.Day.Equal(today.AddDate(0, 0, 21)) || len(after.Facts) != 1 {
					t.Fatalf("later = %+v", asked)
				}
			case timeline.ReviewKeep:
				if asked == nil || asked.Asked != "" || asked.Review.set() {
					t.Fatalf("keep = %+v", asked)
				}
			}
			if len(f.keeps) != 0 {
				t.Fatalf("a model was asked: %d", len(f.keeps))
			}
			if _, err := r.AnswerReview(context.Background(), ask.ID, choice); !errors.Is(err, ErrNoReview) {
				t.Fatalf("a second answer = %v", err)
			}
		})
	}
}

// A body whose line breaks the model escaped is written with real ones.
func TestEscapedLineBreaksInAFieldAreRepaired(t *testing.T) {
	if got := repairNewlines(`・A\n・B`); got != "・A\n・B" {
		t.Fatalf("got %q", got)
	}
	if got := repairNewlines("・A\n・B は \\n と書く"); got != "・A\n・B は \\n と書く" {
		t.Fatalf("a body with real breaks was changed: %q", got)
	}
}

// Several posts are read by their records' ids in one step, a reply with the
// kakera it answered, and each may be cited; more than ten are refused.
func TestRecordedPostsAreReadTogether(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	first := post(t, s, "三田の二郎に行きたい")
	second := post(t, s, "草加のコーヒー")
	reply := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindRemark, Speaker: "gourmet", Root: second.ID, ReplyTo: second.ID, Text: "駅前の喫茶店かな", At: second.At}
	if err := s.Append(reply); err != nil {
		t.Fatal(err)
	}
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	set := r.registry(s.Settings()).For(nil).As("gourmet")
	cited := map[string]string{}
	referenced := map[string]bool{}
	args := `{"ids":["` + first.ID + `","` + reply.ID + `"]}`
	text, _ := r.readAll(context.Background(), skill, []ReadStep{{Type: "records", Args: args}}, set, timeline.Entry{}, time.UTC, map[string]string{}, newLookups(maxReads, maxSearches), cited, referenced)
	for _, want := range []string{"三田の二郎に行きたい", "草加のコーヒー", "駅前の喫茶店かな"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing:\n%s", want, text)
		}
	}
	if cited[first.ID] == "" || cited[reply.ID] == "" || !referenced[first.ID] || !referenced[reply.ID] {
		t.Fatalf("cited = %v, referenced = %v", cited, referenced)
	}
	var many []string
	for i := 0; i <= maxEntryReads; i++ {
		many = append(many, fmt.Sprintf(`"e_%d"`, i))
	}
	text, _ = r.readAll(context.Background(), skill, []ReadStep{{Type: "records", Args: `{"ids":[` + strings.Join(many, ",") + `]}`}}, set, timeline.Entry{}, time.UTC, map[string]string{}, newLookups(maxReads, maxSearches), map[string]string{}, nil)
	if !strings.Contains(text, "10件まで") {
		t.Fatalf("eleven posts were read: %s", text)
	}
}
