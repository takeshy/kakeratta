package persona

import (
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

var tokyo, _ = time.LoadLocation("Asia/Tokyo")

func writeNotes(t *testing.T, s *store.Store, persona string, notes map[string]string) {
	t.Helper()
	for path, text := range notes {
		if err := s.WriteNote(persona, path, text); err != nil {
			t.Fatal(err)
		}
	}
}

// Due notes come oldest first and at most three at a time; the index, a
// note that is not due, and one the sweep will delete anyway are left out.
func TestReviewsDueAreOldestFirstAndCapped(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{
		"index.md":         "---\nreview: 2020-01-01\n---\n# 目次",
		"shops/a.md":       "---\ntype: Shop\nreview: 2026-09-03\n---\n",
		"shops/b.md":       "---\ntype: Shop\nreview: 2026-09-01\n---\n",
		"shops/c.md":       "---\ntype: Shop\nreview: 2026-09-02\n---\n",
		"shops/d.md":       "---\ntype: Shop\nreview: 2026-09-04\n---\n",
		"shops/future.md":  "---\ntype: Shop\nreview: 2099-01-01\n---\n",
		"shops/expires.md": "---\ntype: Shop\nreview: 2020-01-01\nexpires: 2020-01-02\n---\n",
		"tastes/no.md":     "---\ntype: Preference\n---\n",
	})
	skill, _ := r.catalogue.Find("gourmet")
	due := r.reviewsDue(skill, time.Date(2026, 9, 25, 0, 0, 0, 0, tokyo), tokyo)
	var paths []string
	for _, note := range due {
		paths = append(paths, note.Path)
	}
	if strings.Join(paths, ",") != "shops/b.md,shops/c.md,shops/a.md" {
		t.Fatalf("due = %v", paths)
	}
	list := reviewList(due, tokyo)
	if !strings.Contains(list, "shops/b.md (review: 2026-09-01)") {
		t.Fatalf("list = %q", list)
	}
}

// Every active persona checks in once a day at its own minute of the user's
// 07:00-21:00, with nothing due as with a review due, which rides inside.
func TestEveryActivePersonaChecksInOnceADay(t *testing.T) {
	calls := 0
	var system, user string
	r, s, _ := setup(t, func(sys, usr string) string {
		if strings.HasPrefix(sys, "あなたは「tabe」") {
			calls++
			system, user = sys, usr
		}
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.TimeZone = "Asia/Tokyo"
	settings.DailyCheckIns = map[string]store.DailyCheckIn{"gourmet": {Talk: store.CheckInMinimal}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	day := checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, tokyo), "gourmet")
	at := store.CheckInAt(s.Root(), "gourmet", day)

	// It wakes at its minute with nothing due.
	writeNotes(t, s, "gourmet", map[string]string{"tastes/keep.md": "---\ntype: Preference\n---\n豚骨"})
	r.dailyAt(at.Add(-time.Minute))
	if calls != 0 {
		t.Fatal("woke before its minute")
	}
	r.dailyAt(at)
	if calls != 1 || !strings.Contains(user, "Daily check-in") || !strings.Contains(user, "Nothing needs the user's decision") {
		t.Fatalf("calls=%d trigger:\n%s", calls, user)
	}
	if !strings.Contains(system, "ユーザーの判断が必要なことだけ") {
		t.Fatal("the check-in was not told to speak only when needed")
	}
	r.dailyAt(store.CheckInCloses(day).Add(-time.Minute))
	if calls != 1 {
		t.Fatal("woke twice in a day")
	}

	// The next day, with a note due: the review rides inside.
	writeNotes(t, s, "gourmet", map[string]string{"shops/soka.md": "---\ntype: Shop\nreview: 2026-09-20\n---\n草加の店"})
	next := checkInDay(s, day.AddDate(0, 0, 1), "gourmet")
	r.dailyAt(store.CheckInAt(s.Root(), "gourmet", next))
	if calls != 2 || !strings.Contains(user, "shops/soka.md (review: 2026-09-20)") || !strings.Contains(system, "見直し時期が来たメモ") {
		t.Fatalf("calls=%d review trigger:\n%s", calls, user)
	}

	// Missed until the window shut - the server was down - it waits for tomorrow.
	later := next.AddDate(0, 0, 1)
	r.dailyAt(store.CheckInCloses(later))
	if calls != 2 {
		t.Fatal("a check-in ran after 21:00")
	}

	// An inactive persona does not check in.
	settings.Active = nil
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r.dailyAt(store.CheckInCloses(checkInDay(s, later.AddDate(0, 0, 1), "gourmet")).Add(-time.Minute))
	if calls != 2 {
		t.Fatal("an inactive persona woke")
	}
}

// The persona asks in a post of its own; the user's answer under it reaches
// that persona whatever routing says, told which note it had asked about,
// and "もういい" is answered with forget_note and a word.
func TestReviewPostThenReplyWakesItsAuthor(t *testing.T) {
	const moved = "---\ntype: Shop\nreview: 2026-10-02\n---\n草加の店"
	r, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "明示的に求めているか"):
			return `{"escalate":false}`
		case strings.Contains(system, "振り分け"):
			return `{"deliver":[]}`
		case strings.HasPrefix(system, "あなたは「tabe」") && strings.Contains(system, "Review への返事"):
			return `{"summary":"forget","steps":[{"type":"forget_note","path":"shops/soka.md","text":"","tool":"","args":"{}","at":"","daily":false,"note":"","id":""},{"type":"speak","path":"","text":"わかった、忘れるね","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		case strings.HasPrefix(system, "あなたは「tabe」") && !strings.Contains(user, `"notes_read"`):
			// An existing note is read before it is rewritten.
			return `{"action":"tool","tool":"notes_read","args":{"paths":["shops/soka.md"]}}`
		case strings.HasPrefix(system, "あなたは「tabe」"):
			return `{"summary":"ask","steps":[{"type":"write_note","path":"shops/soka.md","text":` + jsonString(moved) + `,"tool":"","args":"{}","at":"","daily":false,"note":"","id":""},{"type":"speak","path":"","text":"そろそろ忘れていい？ 草加の店の話","tool":"","args":"{}","at":"","daily":false,"note":"","id":""}]}`
		}
		return `{"action":"silent"}`
	}, "gourmet", "move")
	settings := s.Settings()
	settings.TimeZone = "Asia/Tokyo"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	writeNotes(t, s, "gourmet", map[string]string{"shops/soka.md": "---\ntype: Shop\nreview: 2026-09-20\n---\n草加の店"})

	// 20:59 is past every persona's minute, so both check in.
	r.dailyAt(store.CheckInCloses(checkInDay(s, time.Date(2026, 9, 25, 12, 0, 0, 0, tokyo), "gourmet", "move")).Add(-time.Minute))
	entries, _ := s.Timeline()
	if len(entries) != 1 || entries[0].Speaker != "gourmet" || entries[0].Kind != timeline.KindPost || len(entries[0].Notes) != 1 || entries[0].Notes[0] != "shops/soka.md" {
		t.Fatalf("review post: %+v", entries)
	}
	if note, ok := s.Note("gourmet", "shops/soka.md"); !ok || !strings.Contains(note.Text, "review: 2026-10-02") {
		t.Fatalf("the asked note was not pushed ahead: %+v", note)
	}

	reply := replyTo(t, s, entries[0], "もういい")
	r.reactEntry(reply.ID, nil, false)
	prompt := stub.lastPromptFor("tabe")
	for _, want := range []string{"Review への返事", "聞いたメモ: shops/soka.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the author was not told %q:\n%s", want, prompt)
		}
	}
	if _, ok := s.Note("gourmet", "shops/soka.md"); ok {
		t.Fatal("the note survived もういい")
	}
	entries, _ = s.Timeline()
	var remarks []timeline.Entry
	for _, entry := range entries {
		if entry.Speaker == "gourmet" && entry.ReplyTo == reply.ID {
			remarks = append(remarks, entry)
		}
	}
	if len(remarks) != 1 || remarks[0].Text != "わかった、忘れるね" {
		t.Fatalf("remarks = %+v", remarks)
	}
	// michi checked in that morning like every active persona, but the
	// answer under tabe's question was not routed to it.
	if !strings.Contains(stub.lastPromptFor("michi"), "Daily check-in") {
		t.Fatal("a persona routing left out was woken")
	}
}

// A reply under a persona's own check-in post also reaches it, without any
// review text when nothing is under review.
func TestAReplyUnderAPersonasOwnPostReachesIt(t *testing.T) {
	r, s, stub := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "明示的に求めているか"):
			return `{"escalate":false}`
		case strings.Contains(system, "振り分け"):
			return `{"deliver":[]}`
		}
		return `{"action":"speak","text":"そうだね"}`
	}, "gourmet")
	post := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: "gourmet", Text: "今日は何か食べた？", At: time.Now()}
	post.Root = post.ID
	if err := s.Append(post); err != nil {
		t.Fatal(err)
	}
	reply := replyTo(t, s, post, "ラーメン")
	r.reactEntry(reply.ID, nil, false)
	prompt := stub.lastPromptFor("tabe")
	if prompt == "" {
		t.Fatal("the author was not woken")
	}
	if strings.Contains(prompt, "Review への返事") {
		t.Fatal("review text on a plain reply")
	}
}

// Forgetting counts as leaving something behind: "もういい" is answered with
// forget_note and a word, and the check lets it through.
func TestForgetNoteCountsAsANoteWhenSpeaking(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	workflow := operationBatch{Steps: []operation{
		{Type: "forget_note", Path: "shops/soka.md"},
	}}
	if problems := checkBatch(workflow, turnRules{set: set, notes: func(string) []store.Note { return nil }}); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if problems := checkBatch(operationBatch{Steps: []operation{}}, turnRules{set: set}); len(problems) != 0 {
		t.Fatal(problems)
	}
}
