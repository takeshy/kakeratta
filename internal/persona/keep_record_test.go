package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// A turn keeps one record in the default field, reading none of the facts or
// older records - only the tags in use. A retry replaces its record, the
// facts are left for the daily pass, and a kakera with nothing to
// look back on is not kept.
func TestATurnKeepsOnlyARecord(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	zone := time.UTC
	old := record{At: time.Now().Add(-24 * time.Hour), ID: "older", Summary: "OLD_RECORD_NOT_IN_PROMPT", Tags: []string{"旧タグ"}}
	original := memo{Facts: []fact{{ID: "f_old", Text: "FACT_NOT_IN_PROMPT"}}, Records: []record{old}}
	if err := s.WriteNote(skill.ID, MemoPath, memoText(original, zone)); err != nil {
		t.Fatal(err)
	}
	remember := "true"
	f.keep = func(system, user string) string {
		for _, unneeded := range []string{"OLD_RECORD_NOT_IN_PROMPT", "FACT_NOT_IN_PROMPT", "FINISHED_ANSWER"} {
			if strings.Contains(user, unneeded) {
				t.Errorf("unneeded keeper context %s:\n%s", unneeded, user)
			}
		}
		if !strings.Contains(user, "使っているタグ: ") || !strings.Contains(user, "旧タグ") {
			t.Errorf("the tags in use were not shown:\n%s", user)
		}
		return `{"remember":` + remember + `,"summary":"残りの肉じゃがをカレーにしたい","tags":["カレー","リメイク"],"expires":"","review":""}`
	}
	client, settings := r.clientOf()
	entry := post(t, s, "残りの肉じゃがをカレーにしたい")
	keep := func(e timeline.Entry) {
		t.Helper()
		if _, err := r.keepNotes(context.Background(), client, settings, keeperTurn{skill: skill, thread: []timeline.Entry{e}, trigger: e, here: zone, answer: "FINISHED_ANSWER"}); err != nil {
			t.Fatal(err)
		}
	}
	keep(entry)
	keep(entry) // durable retry must replace the record, not append again
	note, _ := s.Note(skill.ID, MemoPath)
	ff := parseMemo(note.Text, zone)
	if ff.TidyDirty || len(ff.Facts) != 1 || len(ff.Records) != 2 {
		t.Fatalf("record-only changed other memory: %+v", ff)
	}
	if found, _ := r.findRecords(skill.ID, "リメイク", "", "", 10, zone); len(found) != 1 || found[0].ID != entry.ID {
		t.Fatalf("tagged record: %+v", found)
	}
	if !RecordsTagged(note.Path, note.Text, "カレー") {
		t.Fatal("field tags not searchable")
	}
	remember = "false"
	keep(post(t, s, "こんにちは"))
	note, _ = s.Note(skill.ID, MemoPath)
	if ff = parseMemo(note.Text, zone); len(ff.Records) != 2 {
		t.Fatalf("a kakera with nothing to keep was recorded: %+v", ff.Records)
	}
}

// A deleted kakera takes its record along and requests another daily pass,
// and memory with nothing left is gone.
func TestRecordDeletionRequestsTheDailyPass(t *testing.T) {
	r, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	now := time.Now().UTC()
	entry := post(t, s, "A店に行きたい")
	other := post(t, s, "B店に行きたい")
	recs := []record{
		{At: entry.At, ID: entry.ID, Of: entry.ID, Summary: entry.Text, Tags: []string{"A店"}, Review: recordDate{Day: localDay(now, time.UTC), Days: 7}},
		{At: other.At, ID: other.ID, Of: other.ID, Summary: other.Text, Tags: []string{"B店"}},
	}
	ff := memo{Records: recs}
	if err := s.WriteNote("gourmet", MemoPath, memoText(ff, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if due := r.reviewsDue("gourmet", now, time.UTC); len(due) != 1 || due[0].ID != entry.ID {
		t.Fatalf("due=%+v", due)
	}
	dropped, err := r.dropRecords(context.Background(), "gourmet", []timeline.Entry{entry}, time.UTC)
	if err != nil || !dropped {
		t.Fatalf("drop=%v err=%v", dropped, err)
	}
	if ff = r.loadMemo("gourmet", time.UTC); len(ff.Records) != 1 || ff.Records[0].ID != other.ID || !ff.TidyDirty {
		t.Fatalf("memory after the deletion: %+v", ff)
	}
	if _, err := r.dropRecords(context.Background(), "gourmet", []timeline.Entry{other}, time.UTC); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Note("gourmet", MemoPath); ok {
		t.Fatal("memory with nothing left survived")
	}
}

func TestARecordedPlanPreventsQuietSkip(t *testing.T) {
	today := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	m := memo{Records: []record{{At: today, Summary: "10/19 に予約"}}}
	if !upcomingDated(m, today, 7) {
		t.Fatal("record-only plan ignored")
	}
}
