package store

import (
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/position"
)

func prepared(t *testing.T) (*Store, *book.Book) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ingest.Finalise(ingest.FromText("本", "著者", "第一章\n\nあいうえおかきくけこ\n\nさしすせそ\n"), "test", "x")
	if err != nil {
		t.Fatal(err)
	}
	parsed.Category = book.CategoryNovel
	if err := s.SaveBook(parsed); err != nil {
		t.Fatal(err)
	}
	return s, parsed
}

// The calendar has no data of its own: the read entries already say which day
// each stretch was read on, so a day spent reading without writing anything
// down is still a day with something in it.
func TestCalendarComesOutOfTheReadEntries(t *testing.T) {
	s, parsed := prepared(t)
	state := s.State(parsed.ID)
	yesterday := time.Now().Add(-24 * time.Hour)
	state.Record(position.Range{Start: position.Zero, End: parsed.At(5)}, yesterday)
	state.Record(position.Range{Start: parsed.At(5), End: parsed.At(10)}, time.Now())
	if err := s.SaveState(state); err != nil {
		t.Fatal(err)
	}
	days, err := s.Calendar()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("days = %+v, want the two days that were read on", days)
	}
	// Newest first, and each day knows which book it was.
	if days[0].Date < days[1].Date {
		t.Fatalf("days are not newest first: %+v", days)
	}
	if len(days[0].Books) != 1 || days[0].Books[0].Title != "本" {
		t.Fatalf("day = %+v", days[0])
	}
	if days[0].Chars != 5 {
		t.Fatalf("chars = %d, want 5", days[0].Chars)
	}
}

// Only the reader's own entries go in the timeline, and it runs across books.
func TestTimelineRunsAcrossBooks(t *testing.T) {
	s, parsed := prepared(t)
	now := time.Now()
	if err := s.AppendEvent(Event{
		ID: "e1", BookID: parsed.ID, Kind: "memo.created", Text: "この人怪しい", At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(Event{
		ID: "e2", BookID: parsed.ID, Kind: "question.asked", Text: "この人だれ", TurnID: "t9", At: now,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.AllTimeline()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Event.ID != "e2" {
		t.Fatalf("entries = %+v, want newest first", entries)
	}
	if entries[0].Title != "本" {
		t.Fatalf("the entry does not say which book it belongs to: %+v", entries[0])
	}
	// A question points at the message it was, so following it lands on the
	// question with the answer underneath.
	if entries[0].Event.TurnID != "t9" {
		t.Fatal("the entry has no link back into the thread")
	}
}

// Taking a persona out of a book must not erase what it wrote: adding it again
// should bring back the one that was reading along.
func TestAPersonaKeepsItsNotesWhenItIsTakenOut(t *testing.T) {
	s, parsed := prepared(t)
	memory := s.PersonaState(parsed.ID, "mikage")
	memory.Notes = append(memory.Notes, PersonaNote{
		Text: "この男、名前を名乗っていない", Position: position.Position{Absolute: 42}, At: time.Now(),
	})
	memory.LastProcessed = position.Position{Absolute: 120}
	if err := s.SavePersonaState(memory); err != nil {
		t.Fatal(err)
	}

	state := s.State(parsed.ID)
	state.Personas = []string{"mikage"}
	if err := s.SaveState(state); err != nil {
		t.Fatal(err)
	}
	state.Personas = nil
	if err := s.SaveState(state); err != nil {
		t.Fatal(err)
	}

	back := s.PersonaState(parsed.ID, "mikage")
	if len(back.Notes) != 1 || back.Notes[0].Position.Absolute != 42 {
		t.Fatalf("notes = %+v", back.Notes)
	}
	if back.LastProcessed.Absolute != 120 {
		t.Fatalf("lastProcessed = %d", back.LastProcessed.Absolute)
	}
}

// Personas do not share notes. A technical persona has seen the whole book, and
// its notes reaching a novel persona would take the boundary apart from inside.
func TestPersonasDoNotShareNotes(t *testing.T) {
	s, parsed := prepared(t)
	one := s.PersonaState(parsed.ID, "koyomi")
	one.Notes = append(one.Notes, PersonaNote{Text: "最終章で正体が明かされる"})
	if err := s.SavePersonaState(one); err != nil {
		t.Fatal(err)
	}
	other := s.PersonaState(parsed.ID, "mikage")
	if len(other.Notes) != 0 {
		t.Fatalf("another persona's notes leaked: %+v", other.Notes)
	}
}
