package persona

import (
	"strings"
	"testing"
	"time"
)

func TestExpiresIsReadFromTheFrontMatter(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	cases := []struct {
		text string
		want time.Time
		ok   bool
	}{
		{"---\ntype: Shop\nexpires: 2026-09-26\n---\n\n候補", time.Date(2026, 9, 26, 0, 0, 0, 0, tokyo), true},
		{"---\ntype: Shop\nexpires: \"2026-09-26\"  # 翌日\n---\n", time.Date(2026, 9, 26, 0, 0, 0, 0, tokyo), true},
		{"---\ntype: Shop\nexpires: 2026-09-26T09:30:00+09:00\n---\n", time.Date(2026, 9, 26, 9, 30, 0, 0, tokyo), true},
		{"---\ntype: Shop\nexpires: 2026-09-26 09:30\n---\n", time.Date(2026, 9, 26, 9, 30, 0, 0, tokyo), true},
		{"---\ntype: Shop\nexpires: never\n---\n", time.Time{}, false},
		{"---\ntype: Shop\nexpires:\n---\n", time.Time{}, false},
		{"---\ntype: Shop\nexpires: 明日\n---\n", time.Time{}, false},
		{"---\ntype: Shop\n---\n\nexpires: 2026-09-26", time.Time{}, false},
		{"expires: 2026-09-26", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := noteExpires(c.text, tokyo)
		if ok != c.ok || (ok && !got.Equal(c.want)) {
			t.Errorf("%q: got %v %v, want %v %v", c.text, got, ok, c.want, c.ok)
		}
	}
}

// The app deletes a note whose expires date has passed, leaves every other
// note alone, and wakes the persona once to tidy its index - telling it that
// this was its own arrangement, not the user's.
func TestExpiredNotesAreDeletedAndThePersonaToldOnce(t *testing.T) {
	calls := 0
	var seen string
	r, s, _ := setup(t, func(system, user string) string {
		calls++
		seen = system + "\n" + user
		return `{"action":"silent"}`
	}, "gourmet")
	settings := s.Settings()
	settings.MiddleModel = "m"
	settings.TimeZone = "Asia/Tokyo"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{
		"index.md":             "# 目次\n\n* [候補](/shops/soka-lunch.md)\n* [候補2](/shops/soka-dinner.md)\n* [傾向](/tastes/tonkotsu.md)",
		"shops/soka-lunch.md":  "---\ntype: Shop\nexpires: 2026-09-20\n---\n\n草加のランチ候補",
		"shops/soka-dinner.md": "---\ntype: Shop\nexpires: 2026-09-21T10:00:00+09:00\n---\n\n草加の夕食候補",
		"shops/later.md":       "---\ntype: Shop\nexpires: 2099-01-01\n---\n\nまだ先",
		"tastes/tonkotsu.md":   "---\ntype: Preference\n---\n\n豚骨に寄る",
		"shops/kept.md":        "---\ntype: Shop\nexpires: never\n---\n\n行きたいと言っていた店",
	}
	for path, text := range notes {
		if err := s.WriteNote("gourmet", path, text); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	r.expireNotes(now, s.Settings())
	r.expireNotes(now, s.Settings())

	for _, path := range []string{"shops/soka-lunch.md", "shops/soka-dinner.md"} {
		if _, ok := s.Note("gourmet", path); ok {
			t.Fatalf("%s survived its expires date", path)
		}
	}
	for _, path := range []string{"index.md", "shops/later.md", "tastes/tonkotsu.md", "shops/kept.md"} {
		if _, ok := s.Note("gourmet", path); !ok {
			t.Fatalf("%s was deleted without an expires date that had passed", path)
		}
	}

	r.stopMu.Lock()
	queued := len(r.queue)
	var gone forgotten
	if queued == 1 && r.queue[0].forgotten != nil {
		gone = *r.queue[0].forgotten
	}
	r.stopMu.Unlock()
	if queued != 1 || !gone.expired || gone.skill != "gourmet" || len(gone.paths) != 2 {
		t.Fatalf("queued %d wakes: %+v", queued, gone)
	}

	r.forgottenAt(gone)
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	for _, want := range []string{"期限が来たメモを本体が消しました", "shops/soka-lunch.md", "shops/soka-dinner.md"} {
		if !strings.Contains(seen, want) {
			t.Fatalf("the persona was not told %q:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "ユーザーが、あなたのメモを消しました") {
		t.Fatalf("an expired note was reported as the user's deletion:\n%s", seen)
	}
}

// A note the user deleted and one the app expired are different news, so the
// two wakes are not folded into one.
func TestExpiredAndUserDeletedNotesWakeSeparately(t *testing.T) {
	r, _, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	r.NoteForgotten("gourmet", "shops/a.md")
	r.notesExpired("gourmet", []string{"shops/b.md"})
	r.notesExpired("gourmet", []string{"shops/c.md"})
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if len(r.queue) != 2 {
		t.Fatalf("queued %d wakes", len(r.queue))
	}
	if r.queue[0].forgotten.expired || len(r.queue[0].forgotten.paths) != 1 {
		t.Fatalf("user deletion = %+v", *r.queue[0].forgotten)
	}
	if !r.queue[1].forgotten.expired || len(r.queue[1].forgotten.paths) != 2 {
		t.Fatalf("expiry = %+v", *r.queue[1].forgotten)
	}
}

// Without a model to wake, nothing is deleted: an index left pointing at a
// missing note is worse than a note kept a day too long.
func TestNothingExpiresWithoutAModelToTidyUp(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "shops/old.md", "---\ntype: Shop\nexpires: 2020-01-01\n---\n\n古い候補"); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.LowModel, settings.MiddleModel = "", ""
	r.expireNotes(time.Now(), settings)
	if _, ok := s.Note("gourmet", "shops/old.md"); !ok {
		t.Fatal("deleted a note with nobody to tidy up after it")
	}
}
