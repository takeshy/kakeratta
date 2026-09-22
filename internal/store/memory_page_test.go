package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writtenAt writes a note and sets when it was written, so the order of a
// page does not rest on how fast the test ran.
func writtenAt(t *testing.T, s *Store, path, text string, at time.Time) {
	t.Helper()
	if err := s.WriteNote("gourmet", path, text); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(s.memoryDir("gourmet"), filepath.FromSlash(path)), at, at); err != nil {
		t.Fatal(err)
	}
}

// Walking the pages from the first to the last reads every note once, the
// most recently written first, and only the first page names them all.
func TestMemoryPagesWalkEveryNoteOnceNewestFirst(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		writtenAt(t, s, fmt.Sprintf("wines/%02d.md", i), "note", base.Add(time.Duration(i)*time.Hour))
	}
	// Two notes written at one moment keep one order across the page break.
	writtenAt(t, s, "index.md", "index", base.Add(3*time.Hour))
	var walked []string
	after := ""
	for pages := 0; ; pages++ {
		page, err := s.MemoryPage("gourmet", after, 3, nil)
		if err != nil {
			t.Fatal(err)
		}
		if (after == "") != (len(page.Paths) == 8) {
			t.Fatalf("page after %q names %d notes", after, len(page.Paths))
		}
		for _, note := range page.Notes {
			walked = append(walked, note.Path)
		}
		if page.Next == "" {
			if pages != 2 {
				t.Fatalf("%d pages", pages+1)
			}
			break
		}
		after = page.Next
	}
	want := "wines/06.md,wines/05.md,wines/04.md,wines/03.md,index.md,wines/02.md,wines/01.md,wines/00.md"
	if strings.Join(walked, ",") != want {
		t.Fatalf("walked %v", walked)
	}
}

// A filtered page is full when enough notes match, however many do not, and
// the last page says there is nothing more rather than ending on an empty one.
func TestMemoryPageKeepsOnlyMatchingNotes(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 9 {
		writtenAt(t, s, fmt.Sprintf("%02d.md", i), fmt.Sprint(i%3), base.Add(time.Duration(i)*time.Hour))
	}
	match := func(note Note) bool { return note.Text == "0" }
	first, _ := s.MemoryPage("gourmet", "", 2, match)
	if len(first.Notes) != 2 || first.Notes[0].Path != "06.md" || first.Notes[1].Path != "03.md" || first.Next == "" {
		t.Fatalf("first page: %+v", first)
	}
	last, _ := s.MemoryPage("gourmet", first.Next, 2, match)
	if len(last.Notes) != 1 || last.Notes[0].Path != "00.md" || last.Next != "" {
		t.Fatalf("last page: %+v", last)
	}
	if _, err := s.MemoryPage("gourmet", "not a cursor", 2, nil); err != ErrMemoryCursor {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestMemoryMetaReadsNoNotes(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "a.md", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemory(Memory{PersonaID: "gourmet", Wakes: 4}); err != nil {
		t.Fatal(err)
	}
	if meta := s.MemoryMeta("gourmet"); meta.Wakes != 4 || meta.Notes != nil {
		t.Fatalf("meta: %+v", meta)
	}
	if note, ok := s.Note("gourmet", "/a.md"); !ok || note.Path != "a.md" || note.Text != "a" {
		t.Fatalf("note: %+v %v", note, ok)
	}
	if _, ok := s.Note("gourmet", "missing.md"); ok {
		t.Fatal("missing note found")
	}
}
