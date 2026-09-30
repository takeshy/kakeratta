package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	all, _ := runner.notesTool(context.Background(), "gourmet", "notes_list", nil, nil)
	if !strings.Contains(all, "wines/altura.md") || !strings.Contains(all, "tags: 白ワイン, アルゼンチン, ブレンド") || !strings.Contains(all, "days/today.md") {
		t.Fatalf("listing = %s", all)
	}
	some, _ := runner.notesTool(context.Background(), "gourmet", "notes_list", map[string]any{"tag": "ボルドー"}, nil)
	if !strings.Contains(some, "wines/margaux.md") || strings.Contains(some, "wines/altura.md") || strings.Contains(some, "days/today.md") {
		t.Fatalf("narrowed listing = %s", some)
	}
	if none, _ := runner.notesTool(context.Background(), "gourmet", "notes_list", map[string]any{"tag": "日本"}, nil); !strings.Contains(none, "「日本」のメモはありません") {
		t.Fatalf("empty listing = %s", none)
	}
	_ = store.Memory{}
}

// A listing is a page at a time, and says how to ask for the next one.
func TestNoteListingComesInPages(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range notesPageSize + 1 {
		path := fmt.Sprintf("wines/%03d.md", i)
		if err := s.WriteNote("gourmet", path, "---\ntags: [赤]\n---\n"); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(s.Root(), "memory", "gourmet", filepath.FromSlash(path)), at, at); err != nil {
			t.Fatal(err)
		}
	}
	// The newest first: the oldest note is the one left for the next page.
	first, _ := runner.notesTool(context.Background(), "gourmet", "notes_list", map[string]any{"tag": "赤"}, nil)
	if !strings.HasPrefix(first, "- wines/100.md") || strings.Contains(first, "wines/000.md") || !strings.Contains(first, `"tag":"赤"`) {
		t.Fatalf("first page = %s", first)
	}
	var more map[string]any
	if err := json.Unmarshal([]byte(first[strings.Index(first, "{"):strings.LastIndex(first, "}")+1]), &more); err != nil {
		t.Fatalf("no args to continue with: %s", first)
	}
	rest, _ := runner.notesTool(context.Background(), "gourmet", "notes_list", more, nil)
	if !strings.Contains(rest, "wines/000.md") || strings.Contains(rest, "wines/001.md") || strings.Contains(rest, "続き") {
		t.Fatalf("last page = %s", rest)
	}
}

// A second note about one thing is looked for where it would be filed, beside
// the new one; nothing else is read.
func TestDuplicatesAreLookedForBesideTheNote(t *testing.T) {
	runner, s, _ := setup(t, func(string, string) string { return `{"action":"silent"}` }, "gourmet")
	if err := s.WriteNote("gourmet", "wines/altura.md", "---\ntitle: ALTURA White Blend\n---\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := runner.notesTool(context.Background(), "gourmet", "notes_write", map[string]any{"path": "wines/altra.md", "text": "---\ntitle: Altura white blend\n---\n"}, newProvenance()); !strings.Contains(got, "書きませんでした") {
		t.Fatalf("same directory = %s", got)
	}
	if got := runner.siblingNotes("gourmet", "shops/altura.md"); len(got) != 0 {
		t.Fatalf("another directory was read: %+v", got)
	}
}

func TestNoteListingIncludesUpdatedTime(t *testing.T) {
	memory := store.Memory{Notes: []store.Note{{
		Path: "topics/old.md", Text: "---\ntags: [Go]\n---\n\nOld topic", Written: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}}}
	got := listNotes(memory.Notes)
	if !strings.Contains(got, "updated: 2026-08-01T12:00:00Z") {
		t.Fatalf("updated time missing: %s", got)
	}
}
