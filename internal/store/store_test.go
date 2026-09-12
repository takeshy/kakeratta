package store

import (
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/position"
)

func TestShelfReportsProgressAndFinishing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ingest.Finalise(ingest.FromText("本", "著者", "第一章\n\nあいうえお\n\nあとがき\n\nおまけ\n"), "test", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBook(parsed); err != nil {
		t.Fatal(err)
	}

	state := s.State(parsed.ID)
	state.Record(position.Range{Start: position.Zero, End: parsed.ContentEnd}, time.Now())
	if err := s.SaveState(state); err != nil {
		t.Fatal(err)
	}
	shelf, err := s.Shelf()
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 1 {
		t.Fatalf("shelf = %d", len(shelf))
	}
	if !shelf[0].Finished {
		t.Fatal("reaching the end of the body should show as finished")
	}
	if shelf[0].Fraction >= 1 {
		t.Fatalf("fraction = %v, the afterword is still unread", shelf[0].Fraction)
	}
}

func TestMemosAndTimelineSurviveAReopen(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	memo := Memo{ID: "m1", BookID: "b1", Text: "この人怪しい", Position: position.Position{Absolute: 82}}
	if err := s.SaveMemos("b1", []Memo{memo}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(Event{ID: "e1", BookID: "b1", Kind: "memo.created", MemoID: "m1"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	memos, err := reopened.Memos("b1")
	if err != nil || len(memos) != 1 || memos[0].Text != "この人怪しい" {
		t.Fatalf("memos = %+v (%v)", memos, err)
	}
	events, err := reopened.Timeline("b1")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v (%v)", events, err)
	}
}
