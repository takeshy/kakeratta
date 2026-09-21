package persona

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// Tags come out of the front matter however they were written, the listing a
// persona is handed carries them, and the listing can be narrowed to one.
func TestTagsAreReadListedAndFilteredBy(t *testing.T) {
	inline := "---\ntype: Wine\ntags: [白ワイン, \"アルゼンチン\", ブレンド]   # 手がかり\n---\n本文"
	if got := noteTags(inline); !reflect.DeepEqual(got, []string{"白ワイン", "アルゼンチン", "ブレンド"}) {
		t.Fatalf("inline tags = %v", got)
	}
	block := "---\ntype: Wine\ntags:\n  - 赤ワイン\n  - 'ボルドー'\n  - 赤ワイン\ntitle: x\n---\n"
	if got := noteTags(block); !reflect.DeepEqual(got, []string{"赤ワイン", "ボルドー"}) {
		t.Fatalf("block tags = %v", got)
	}
	if noteTags("本文だけ") != nil || noteTags("---\ntype: Wine\n---\n") != nil {
		t.Fatal("tags found where there are none")
	}
	if !hasTag([]string{"Bordeaux"}, " bordeaux ") {
		t.Fatal("tags are compared with spelling in mind")
	}

	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "wines/altura.md", inline); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "wines/margaux.md", block); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "days/today.md", "雨"); err != nil {
		t.Fatal(err)
	}
	all, _ := runner.notesTool("gourmet", "notes_list", nil, nil)
	if !strings.Contains(all, "wines/altura.md") || !strings.Contains(all, "tags: 白ワイン, アルゼンチン, ブレンド") || !strings.Contains(all, "days/today.md") {
		t.Fatalf("listing = %s", all)
	}
	some, _ := runner.notesTool("gourmet", "notes_list", map[string]any{"tag": "ボルドー"}, nil)
	if !strings.Contains(some, "wines/margaux.md") || strings.Contains(some, "wines/altura.md") || strings.Contains(some, "days/today.md") {
		t.Fatalf("narrowed listing = %s", some)
	}
	if none, _ := runner.notesTool("gourmet", "notes_list", map[string]any{"tag": "日本"}, nil); !strings.Contains(none, "「日本」のメモはありません") {
		t.Fatalf("empty listing = %s", none)
	}
	_ = store.Memory{}
}

func TestNoteListingIncludesUpdatedTime(t *testing.T) {
	memory := store.Memory{Notes: []store.Note{{
		Path: "topics/old.md", Text: "---\ntags: [Go]\n---\n\nOld topic", Written: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}}}
	got := listNotes(memory)
	if !strings.Contains(got, "updated: 2026-08-01T12:00:00Z") {
		t.Fatalf("updated time missing: %s", got)
	}
}
