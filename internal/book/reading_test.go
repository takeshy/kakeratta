package book

import (
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/position"
)

func span(b *Book, from, to int) position.Range {
	return position.Range{Start: b.At(from), End: b.At(to)}
}

func TestReadingOnJoinsOneSitting(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 5), at)
	state.Record(span(b, 5, 9), at.Add(2*time.Minute))
	if len(state.Entries) != 1 {
		t.Fatalf("entries = %d, want one sitting", len(state.Entries))
	}
	if got := state.Entries[0].Range; got.Start.Absolute != 0 || got.End.Absolute != 9 {
		t.Fatalf("entry = %d-%d, want 0-9", got.Start.Absolute, got.End.Absolute)
	}
}

func TestReadingSplitsOnADayABreakOrAJump(t *testing.T) {
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		next time.Time
		from int
		to   int
	}{
		{"日をまたぐ", at.Add(14 * time.Hour), 5, 9},
		{"間が空く", at.Add(2 * time.Hour), 5, 9},
		{"離れたページに飛ぶ", at.Add(time.Minute), 9, 12},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			b := sample()
			state := ReaderState{}
			state.Record(span(b, 0, 5), at)
			state.Record(span(b, test.from, test.to), test.next)
			if len(state.Entries) != 2 {
				t.Fatalf("entries = %d, want two", len(state.Entries))
			}
		})
	}
}

func TestReadingKeepsSkippedPagesOut(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 4), at)
	state.Record(span(b, 8, 12), at.Add(time.Minute))
	ranges := state.ReadRanges()
	if len(ranges) != 2 {
		t.Fatalf("ranges = %v, want the gap kept", ranges)
	}
	if state.ReadChars() != 8 {
		t.Fatalf("read chars = %d, want 8 - the skipped stretch is not read", state.ReadChars())
	}
	// The boundary is the far end, so the AI still knows nothing past it.
	if state.Boundary().Absolute != 12 {
		t.Fatalf("boundary = %d, want 12", state.Boundary().Absolute)
	}
}

func TestRecordReportsOnlyWhatIsNew(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 8), at)
	fresh := state.Record(span(b, 4, 12), at.Add(time.Minute))
	if len(fresh) != 1 || fresh[0].Start.Absolute != 8 || fresh[0].End.Absolute != 12 {
		t.Fatalf("fresh = %v, want only 8-12", fresh)
	}
	// Re-reading a stretch is not new text, so nothing is summarised again.
	if again := state.Record(span(b, 0, 4), at.Add(2*time.Minute)); len(again) != 0 {
		t.Fatalf("re-reading reported %v as new", again)
	}
}

func TestGoingBackDoesNotRewindTheBoundary(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 12), at)
	state.Current = b.At(2)
	if state.Boundary().Absolute != 12 {
		t.Fatalf("boundary = %d after going back", state.Boundary().Absolute)
	}
}

func TestCompletionIgnoresGapsButNotTheEnd(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 3), at)
	state.Record(span(b, 9, b.ContentEnd.Absolute), at.Add(time.Hour))
	if !b.IsCompleted(state) {
		t.Fatal("a gap in the middle should not keep the book unfinished")
	}
	// A technical book has no notion of finishing, so nothing unlocks.
	b.Category = CategoryTechnical
	if b.IsCompleted(state) {
		t.Fatal("technical books never report as finished")
	}
}

func TestDaysAreTheCalendar(t *testing.T) {
	b := sample()
	first := time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 5), first)
	state.Record(span(b, 5, 12), first.Add(24*time.Hour))
	days := state.Days()
	if days["2026-09-11"] != 5 || days["2026-09-12"] != 7 {
		t.Fatalf("days = %v", days)
	}
}

func TestReadTextLeavesOutWhatWasSkipped(t *testing.T) {
	b := sample()
	at := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	state := ReaderState{}
	state.Record(span(b, 0, 5), at)
	state.Record(span(b, 10, 15), at.Add(time.Hour))
	if text := b.ReadText(state, 0); text != "あいうえお\nさしすせそ" {
		t.Fatalf("text = %q", text)
	}
}

func TestOldStateWithoutEntriesStillHasARange(t *testing.T) {
	b := sample()
	state := ReaderState{Furthest: b.At(9)}
	ranges := state.ReadRanges()
	if len(ranges) != 1 || ranges[0].End.Absolute != 9 {
		t.Fatalf("ranges = %v, want everything up to the old furthest position", ranges)
	}
}

func TestFlickingThroughIsNotReading(t *testing.T) {
	// Twelve seconds is enough for one page and nowhere near enough for ten.
	one := position.Range{Start: position.Position{Absolute: 0}, End: position.Position{Absolute: 900}}
	if RequiredDwell(one) != DwellSeconds {
		t.Fatalf("one page needs %v seconds", RequiredDwell(one))
	}
	ten := position.Range{Start: position.Position{Absolute: 0}, End: position.Position{Absolute: 10 * PageChars}}
	if RequiredDwell(ten) != 10*DwellSeconds {
		t.Fatalf("ten pages need %v seconds", RequiredDwell(ten))
	}
}
