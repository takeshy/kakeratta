package persona

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// dailyScript answers the day's summary, the month's summary and the
// check-ins of tabe and shiori, and remembers what each was handed.
type dailyScript struct {
	mu        sync.Mutex
	summaries int
	monthly   string
	prompts   map[string]string
	summary   string
}

func (d *dailyScript) reply(system, user string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.Contains(system, "定期チェックインで読む内部向けのまとめ"):
		d.summaries++
		return d.summary
	case strings.Contains(system, "今月のまとめ"):
		d.monthly = user
		return "### 人\n- 佐藤さんと釣り [→](/?entry=x)"
	}
	for _, name := range []string{"tabe", "shiori"} {
		if strings.HasPrefix(system, "あなたは「"+name+"」") {
			d.prompts[name] = user
		}
	}
	return `{"action":"silent"}`
}

func newDailyScript() *dailyScript {
	return &dailyScript{prompts: map[string]string{}, summary: "- 釣りの約束（佐藤さん） [e_x]"}
}

// Every persona's check-in is handed one summary of the day, written once,
// instead of each reading the same posts.
func TestCheckInsShareOneDailySummary(t *testing.T) {
	script := newDailyScript()
	r, s, _ := setup(t, script.reply, "gourmet", "book")
	day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet", "book")
	first := store.CheckInCloses(day).Add(-time.Minute)
	r.dailyAt(context.Background(), first)

	next := checkInDay(s, day.AddDate(0, 0, 1), "gourmet", "book")
	at := store.CheckInCloses(next).Add(-time.Minute)
	post := appendAt(t, s, "佐藤さんと釣りに行く約束をした", first.Add(time.Hour))
	script.prompts = map[string]string{}
	r.dailyAt(context.Background(), at)
	if script.summaries != 1 {
		t.Fatalf("day's summary written %d times", script.summaries)
	}
	for _, name := range []string{"tabe", "shiori"} {
		prompt := script.prompts[name]
		if !strings.Contains(prompt, script.summary) || strings.Contains(prompt, post.Text) {
			t.Fatalf("%s was not handed the summary instead of the post:\n%s", name, prompt)
		}
	}
}

// A day's summary that fails or is cut off is not kept; the check-ins are
// handed the posts as they are instead.
func TestAFailedDaySummaryHandsThePostsInFull(t *testing.T) {
	script := newDailyScript()
	script.summary = ""
	r, s, _ := setup(t, script.reply, "gourmet")
	day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet")
	first := store.CheckInCloses(day).Add(-time.Minute)
	r.dailyAt(context.Background(), first)

	next := checkInDay(s, day.AddDate(0, 0, 1), "gourmet")
	post := appendAt(t, s, "あとから来た投稿", first.Add(time.Hour))
	r.dailyAt(context.Background(), store.CheckInCloses(next).Add(-time.Minute))
	if script.summaries != 1 || len(r.dailySummaries()) != 0 {
		t.Fatalf("summaries asked=%d kept=%d", script.summaries, len(r.dailySummaries()))
	}
	if prompt := script.prompts["tabe"]; !strings.Contains(prompt, post.ID) || !strings.Contains(prompt, post.Text) {
		t.Fatalf("the post was not handed in full:\n%s", prompt)
	}
}

// Deleting a post drops the summaries that may hold it; the posts they
// covered are handed as they are, and the deleted one is gone.
func TestDeletingAPostDropsTheDaySummaryHoldingIt(t *testing.T) {
	script := newDailyScript()
	r, s, _ := setup(t, script.reply, "gourmet")
	day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), "gourmet")
	first := store.CheckInCloses(day).Add(-time.Minute)
	kept := appendAt(t, s, "残る投稿", first.Add(-2*time.Hour))
	gone := appendAt(t, s, "消す投稿", first.Add(-time.Hour))
	r.dailyAt(context.Background(), first)
	if len(r.dailySummaries()) != 1 {
		t.Fatal("no day's summary was written")
	}
	r.forgetDailySummaries(gone.At)
	if len(r.dailySummaries()) != 0 {
		t.Fatal("the summary holding the deleted post survived")
	}
	digest, _ := r.checkInDigest(context.Background(), first.Add(-24*time.Hour), first.Add(time.Minute), time.UTC)
	if !strings.Contains(digest, kept.Text) {
		t.Fatalf("the covered post was not handed in full:\n%s", digest)
	}
}

// The month's summary is written from the day's summaries, reading in full
// only the posts none of them holds.
func TestMonthlySummaryReadsTheDaySummaries(t *testing.T) {
	script := newDailyScript()
	r, s, _ := setup(t, script.reply)
	day := summaryDay(s)
	at := store.CheckInAt(s.Root(), store.DailyID, day)
	covered := appendAt(t, s, "まとめに入った投稿", at.Add(-10*24*time.Hour))
	uncovered := appendAt(t, s, "まとめにない投稿", at.Add(-2*24*time.Hour))
	head := "<!-- kakeratta:daily from=" + covered.At.Add(-time.Hour).UTC().Format(time.RFC3339Nano) + " to=" + covered.At.Add(time.Hour).UTC().Format(time.RFC3339Nano) + " -->\n"
	if err := s.WriteNote(store.SummaryID, dailySummaryDir+"d.md", head+"- 日ごとのまとめの項目 [e_covered]"); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(context.Background(), at)
	if !strings.Contains(script.monthly, "日ごとのまとめの項目") || strings.Contains(script.monthly, covered.Text) || !strings.Contains(script.monthly, uncovered.Text) {
		t.Fatalf("the month read was:\n%s", script.monthly)
	}
}
