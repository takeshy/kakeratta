package persona

import (
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// One thing, one note: a second note with the same title, or one a slip away
// from it, is sent back to the path that already exists - unless the new note
// links to the other, which says the two are meant to be different.
func TestOneThingIsFiledOnce(t *testing.T) {
	existing := []store.Note{
		{Path: "wines/altura.md", Text: "---\ntype: Wine\ntitle: ALTURA White Blend\n---\nノートンの白。"},
		{Path: "wines/pinot.md", Text: "---\ntype: Wine\ntitle: \"Domaine X Pinot Noir 2019\"\n---\n軽い。"},
		{Path: "index.md", Text: "# 目次\n* [ALTURA White Blend](/wines/altura.md)"},
	}
	for _, tc := range []struct {
		name, path, text, want string
	}{
		{"same title, another path", "wines/altra-white-blend.md", "---\ntype: Wine\ntitle: Altura white blend\n---\n", "同じ題名"},
		{"a slip of the label", "wines/altra.md", "---\ntype: Wine\ntitle: ALTRA White Blend\n---\n", "よく似た題名"},
		{"rewriting the same path", "wines/altura.md", "---\ntype: Wine\ntitle: ALTURA White Blend\n---\n書き直し。", ""},
		{"a different wine", "wines/malbec.md", "---\ntype: Wine\ntitle: Norton Malbec Reserva\n---\n", ""},
		{"a different vintage that says so", "wines/pinot-2020.md", "---\ntype: Wine\ntitle: Domaine X Pinot Noir 2020\n---\n[2019](/wines/pinot.md) の翌年。", ""},
		{"a different vintage that does not", "wines/pinot-2020.md", "---\ntype: Wine\ntitle: Domaine X Pinot Noir 2020\n---\n", "よく似た題名"},
		{"no front matter", "days/today.md", "雨。", ""},
		{"the index", "index.md", "---\ntitle: ALTURA White Blend\n---\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := duplicateNote(existing, tc.path, tc.text)
			if (tc.want == "" && got != "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if noteTitle("---\ntitle: 'Quoted title'\ndescription: x\n---\nbody") != "Quoted title" {
		t.Fatal("a quoted title was not read")
	}
	if editDistance("altura", "altra") != 1 || editDistance("abc", "xyz") != 3 {
		t.Fatal("edit distance is off")
	}
}

// The check reaches the workflow before it runs, so the persona can fix the
// path rather than lose its remark, and it catches two new notes with one
// title in the same workflow.
func TestAPlanFilingOneThingTwiceIsSentBack(t *testing.T) {
	set := tools.New(tools.Config{}).For(nil)
	existing := []store.Note{{Path: "wines/altura.md", Text: "---\ntype: Wine\ntitle: ALTURA White Blend\n---\n"}}
	twice := operationBatch{Steps: []operation{
		{Type: "write_note", Path: "wines/altra.md", Text: "---\ntype: Wine\ntitle: ALTRA White Blend\n---\n白。"},
	}}
	problems := checkBatch(twice, turnRules{set: set, notes: func(string) []store.Note { return existing }})
	if len(problems) != 1 || !strings.Contains(problems[0], "wines/altura.md") {
		t.Fatalf("problems = %v", problems)
	}
	same := operationBatch{Steps: []operation{
		{Type: "write_note", Path: "wines/altura.md", Text: "---\ntype: Wine\ntitle: ALTURA White Blend\n---\n白。"},
	}}
	if problems := checkBatch(same, turnRules{set: set, notes: func(string) []store.Note { return existing }}); len(problems) != 0 {
		t.Fatalf("rewriting the existing note was refused: %v", problems)
	}
	atOnce := operationBatch{Steps: []operation{
		{Type: "write_note", Path: "wines/a.md", Text: "---\ntitle: Norton Malbec\n---\n"},
		{Type: "write_note", Path: "wines/b.md", Text: "---\ntitle: Norton Malbec\n---\n"},
	}}
	if problems := checkBatch(atOnce, turnRules{set: set}); len(problems) != 1 || !strings.Contains(problems[0], "同じ題名です") {
		t.Fatalf("two new notes with one title were accepted: %v", problems)
	}
}
