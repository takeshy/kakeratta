package book

import (
	"math"
	"sort"
	"time"

	"github.com/takeshy/dokking/internal/position"
)

// ReadEntry is one stretch the reader actually read, in one sitting.
//
// This is the only record of how far they have got. The resume point, the
// amount read, the calendar and the range the AI is allowed to see all come out
// of it, so there is one thing to keep right instead of four.
type ReadEntry struct {
	Range     position.Range `json:"range"`
	StartedAt time.Time      `json:"startedAt"`
	EndedAt   time.Time      `json:"endedAt"`
}

// DwellSeconds is how long a page has to be in front of the reader before it
// counts as read. Flicking through a book is not reading it, and pages flicked
// past must not end up in the range the AI is told the reader knows.
const DwellSeconds = 12

// PageChars is how much text counts as one page for that test. The screen
// reports a stretch and how long it was in front of the reader; the stretch may
// be a screenful or several, so the threshold is per page rather than per
// report.
const PageChars = 1200

// RequiredDwell is how long a stretch has to have been in front of the reader
// before it counts as read.
func RequiredDwell(span position.Range) float64 {
	pages := math.Ceil(float64(span.Length()) / PageChars)
	if pages < 1 {
		pages = 1
	}
	return pages * DwellSeconds
}

// idleGap is how long a break can be before the next stretch is a new sitting.
// Within it the reader went to make tea; past it they came back to the book.
const idleGap = 30 * time.Minute

// record merges a span into the entries and returns what was not already there.
//
// Joining is deliberately narrow: the span has to touch the last entry, and it
// has to be the same day and soon after it. A new day, a long break, or a jump
// to a distant page each start a new entry, because each of those is a
// different act of reading and the calendar is built out of the difference.
func (s *ReaderState) record(span position.Range, at time.Time) []position.Range {
	if span.End.AtOrBefore(span.Start) {
		return nil
	}
	fresh := s.uncovered(span)
	if last := s.latest(); last != nil && joins(*last, span, at) {
		last.Range.Start = position.Min(last.Range.Start, span.Start)
		last.Range.End = position.Max(last.Range.End, span.End)
		last.EndedAt = at
		return fresh
	}
	s.Entries = append(s.Entries, ReadEntry{Range: span, StartedAt: at, EndedAt: at})
	return fresh
}

func joins(last ReadEntry, span position.Range, at time.Time) bool {
	if !sameDay(last.EndedAt, at) || at.Sub(last.EndedAt) > idleGap || at.Before(last.StartedAt) {
		return false
	}
	// Touching counts: one screen ends where the next begins. A gap does not,
	// because the pages in the gap were skipped and the reader did not read them
	// either.
	return span.Start.AtOrBefore(last.Range.End) && span.End.AtOrAfter(last.Range.Start)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd && a.Location() == b.Location()
}

// latest is the entry the reader was last in, which is the only one a new span
// may extend.
func (s *ReaderState) latest() *ReadEntry {
	best := -1
	for i := range s.Entries {
		if best < 0 || s.Entries[i].EndedAt.After(s.Entries[best].EndedAt) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return &s.Entries[best]
}

// uncovered is the parts of the span no entry covers yet: what the reader has
// just read for the first time, and therefore the only text a summary or a
// persona has to be given again.
func (s *ReaderState) uncovered(span position.Range) []position.Range {
	remaining := []position.Range{span}
	for _, covered := range s.ReadRanges() {
		var next []position.Range
		for _, part := range remaining {
			next = append(next, subtract(part, covered)...)
		}
		remaining = next
	}
	return remaining
}

// subtract removes the covered range from part, leaving what is left of it.
func subtract(part, covered position.Range) []position.Range {
	if !part.Overlaps(covered) {
		return []position.Range{part}
	}
	var out []position.Range
	if part.Start.Before(covered.Start) {
		out = append(out, position.Range{Start: part.Start, End: covered.Start})
	}
	if covered.End.Before(part.End) {
		out = append(out, position.Range{Start: covered.End, End: part.End})
	}
	return out
}

// ReadRanges returns the read intervals in reading order, joined where they
// overlap. This is what gets handed to a summary or a prompt: the text the
// reader has seen, with the skipped parts left out.
func (s *ReaderState) ReadRanges() []position.Range {
	if len(s.Entries) == 0 {
		if s.Furthest.Absolute > 0 {
			// State written before entries existed: the furthest position was
			// treated as "everything up to here", so it is carried over that way.
			return []position.Range{{Start: position.Zero, End: s.Furthest}}
		}
		return nil
	}
	spans := make([]position.Range, 0, len(s.Entries))
	for _, entry := range s.Entries {
		spans = append(spans, entry.Range)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Absolute < spans[j].Start.Absolute })
	merged := []position.Range{spans[0]}
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span.Start.AtOrBefore(last.End) {
			last.End = position.Max(last.End, span.End)
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

// ReadChars is how much of the book has been read, counting each stretch once.
func (s *ReaderState) ReadChars() int {
	total := 0
	for _, span := range s.ReadRanges() {
		total += span.Length()
	}
	return total
}

// Days returns the calendar days the reader read on, with how much they read
// each day. The reading log needs no data of its own: the entries already say
// when, and how much.
func (s *ReaderState) Days() map[string]int {
	days := map[string]int{}
	for _, entry := range s.Entries {
		if entry.EndedAt.IsZero() {
			continue
		}
		days[entry.EndedAt.Format("2006-01-02")] += entry.Range.Length()
	}
	return days
}

// ReadText returns the body text of the read intervals, in order. It is the
// novel path's whole view of the book: text the reader has not stayed on is not
// in it at all, so nothing downstream has to filter.
func (b *Book) ReadText(s ReaderState, limit int) string {
	return b.ReadTextSince(s, position.Zero, limit)
}

// ReadTextSince is the same, from a point onwards: what a companion that was
// last woken at `from` has to catch up on, with anything skipped left out.
func (b *Book) ReadTextSince(s ReaderState, from position.Position, limit int) string {
	var out []rune
	for _, span := range s.ReadRanges() {
		if span.End.AtOrBefore(from) {
			continue
		}
		span.Start = position.Max(span.Start, from)
		part := b.TextUpTo(span.Start, span.End)
		if part == "" {
			continue
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, []rune(part)...)
	}
	// Long inputs lose the middle, so when the read range outgrows the budget
	// the tail is kept: the part being read now is the part being asked about.
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return string(out)
}
