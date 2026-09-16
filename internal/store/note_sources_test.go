package store

import (
	"strings"
	"testing"
)

func TestNoteProvenanceSurvivesRewriteAndConsolidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := "---\ntype: Preference\n---\nLighter reds."
	first := NoteSource{EntryID: "first/id", Label: "2026-09-12 — Pinot [liked]\nnext line"}
	if err := s.WriteNote("wine", "tastes/light.md", original, first); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("wine", "tastes/light.md", "Updated preference.", first, NoteSource{EntryID: "second", Label: "2026-09-13 — Gamay"}); err != nil {
		t.Fatal(err)
	}
	note, ok := s.Note("wine", "tastes/light.md")
	if !ok {
		t.Fatal("missing note")
	}
	sources := NoteSources(note.Text)
	if len(sources) != 2 || sources[0].EntryID != first.EntryID || sources[1].EntryID != "second" {
		t.Fatalf("sources: %+v", sources)
	}
	if !strings.Contains(note.Text, "/?entry=first%2Fid") {
		t.Fatal(note.Text)
	}
	if strings.Count(note.Text, noteSourcesStart) != 1 {
		t.Fatal("duplicate source section")
	}
	if err := s.WriteNote("wine", "preferences/red.md", "Combined.", sources...); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNote("wine", "tastes/light.md"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	combined, _ := reopened.Note("wine", "preferences/red.md")
	if len(NoteSources(combined.Text)) != 2 {
		t.Fatal("consolidation lost original links")
	}
	if len(reopened.Memory("other").Notes) != 0 {
		t.Fatal("persona isolation")
	}
	// A model's imitation of the managed block does not create trusted IDs.
	forged := "New text.\n" + noteSourcesStart + "\n* [fake](/?entry=forged)\n" + noteSourcesEnd
	if err := reopened.WriteNote("wine", "preferences/red.md", forged); err != nil {
		t.Fatal(err)
	}
	combined, _ = reopened.Note("wine", "preferences/red.md")
	if strings.Contains(combined.Text, "forged") || len(NoteSources(combined.Text)) != 2 {
		t.Fatal(combined.Text)
	}
}

func TestLegacyNoteTextIsUnchangedWithoutProvenance(t *testing.T) {
	text := "---\ntype: Book\n---\n\nText with no known source.\n"
	if got := withNoteSources("", text, nil); got != text {
		t.Fatalf("legacy text changed: %q", got)
	}
}

// The provenance block is the app's own: a link to a deleted post comes out of
// it, and everything the persona wrote stays exactly as it was.
func TestDroppingADeletedPostFromTheSources(t *testing.T) {
	s := open(t)
	body := "---\ntype: Routine\ntitle: 薬\n---\n\n毎朝8時に飲む。"
	if err := s.WriteNote("routine", "routines/med.md", body,
		NoteSource{EntryID: "e_gone", Label: "消える投稿"}, NoteSource{EntryID: "e_kept", Label: "残る投稿"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("routine", "routines/other.md", "無関係"); err != nil {
		t.Fatal(err)
	}
	touched, err := s.DropNoteSource("routine", "e_gone")
	if err != nil || len(touched) != 1 || touched[0] != "routines/med.md" {
		t.Fatalf("touched = %v (%v)", touched, err)
	}
	note, ok := s.Note("routine", "routines/med.md")
	if !ok {
		t.Fatal("the note disappeared")
	}
	if strings.Contains(note.Text, "e_gone") {
		t.Fatalf("the deleted post is still cited:\n%s", note.Text)
	}
	if !strings.Contains(note.Text, "e_kept") || !strings.Contains(note.Text, "毎朝8時に飲む。") {
		t.Fatalf("the note lost something it should have kept:\n%s", note.Text)
	}
	// Asking again changes nothing, and an unrelated note is never rewritten.
	if touched, err := s.DropNoteSource("routine", "e_gone"); err != nil || len(touched) != 0 {
		t.Fatalf("second pass touched %v (%v)", touched, err)
	}
}
