package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The span the persona gave a note starts again from when it is read.
func TestExtendedDatesRestartTheSpanFromNow(t *testing.T) {
	written := time.Date(2026, 9, 20, 15, 0, 0, 0, tokyo)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, tokyo)
	for _, tc := range []struct {
		name, text, want string
		moved            bool
	}{
		{"next-day expires", "---\ntype: Shop\nexpires: 2026-09-21\n---\n候補", "expires: 2026-09-26", true},
		{"weeks of review", "---\ntype: Shop\nreview: 2026-10-11 # 3 weeks\n---\n行きたい店", "review: 2026-10-16 # 3 weeks", true},
		{"quoted", "---\nexpires: \"2026-09-21\"\n---\n候補", `expires: "2026-09-26"`, true},
		{"timestamp", "---\nexpires: 2026-09-21T15:00:00+09:00\n---\n候補", "expires: 2026-09-26T10:00:00+09:00", true},
		{"both", "---\nexpires: 2026-09-21\nreview: 2026-09-27\n---\n両方", "expires: 2026-09-26\nreview: 2026-10-02", true},
		{"no dates", "---\ntype: Shop\n---\nずっと", "type: Shop", false},
		{"never", "---\nexpires: never\n---\nずっと", "expires: never", false},
		{"in the body only", "---\ntype: Shop\n---\nexpires: 2026-09-21", "expires: 2026-09-21", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, moved := extendedDates(tc.text, written, now, tokyo)
			if moved != tc.moved || !strings.Contains(got, tc.want) {
				t.Fatalf("moved=%v\n%s", moved, got)
			}
		})
	}
	// Already later than the restarted span: left alone.
	if _, moved := extendedDates("---\nreview: 2026-12-01\n---\n先", now, now, tokyo); moved {
		t.Fatal("a date that is already later moved")
	}
}

// A note tabe reads while answering a post has its dates moved; a note it
// reads at check-in does not, or asking about it would keep being put off.
func TestANoteReadForAPostKeepsLonger(t *testing.T) {
	read := false
	r, s, _ := setup(t, func(system, user string) string {
		switch {
		case strings.Contains(system, "振り分け"):
			return `{"deliver":["gourmet"]}`
		case strings.HasPrefix(system, "あなたは「tabe」") && !read:
			read = true
			return `{"action":"tool","tool":"notes_read","args":{"paths":["shops/soka.md"]}}`
		}
		return `{"action":"silent"}`
	}, "gourmet")
	writeNotes(t, s, "gourmet", map[string]string{"shops/soka.md": "---\ntype: Shop\nexpires: 2026-01-02\nreview: 2026-01-15\n---\n草加の店"})
	// Written long ago: the next-day and two-week spans start from then.
	file := filepath.Join(s.Root(), "memory", "gourmet", "shops", "soka.md")
	then := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, then, then); err != nil {
		t.Fatal(err)
	}
	entry := post(t, s, "草加に行く")
	r.react(entry.ID)
	note, _ := s.Note("gourmet", "shops/soka.md")
	if strings.Contains(note.Text, "expires: 2026-01-02") || strings.Contains(note.Text, "review: 2026-01-15") {
		t.Fatalf("the dates did not move:\n%s", note.Text)
	}
	if !strings.Contains(note.Text, "草加の店") {
		t.Fatalf("the body changed:\n%s", note.Text)
	}
}
