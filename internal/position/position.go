// Package position holds the canonical position model.
//
// Every format - EPUB, Aozora Bunko, plain text - is normalised into the same
// triple at ingest time, and every other part of the app (display, read-aloud,
// bookmarks, highlights, memo quotes, retrieval, the AI's knowledge boundary)
// is expressed in it. Nothing downstream may invent its own notion of "where".
package position

// Position is one point in a book.
//
// SectionID/BlockID/CharOffset is the canonical triple. Absolute is the number
// of characters of body text before that point, derived from the triple when
// the book is parsed; it exists so two positions can be compared without
// carrying the book's structure around, and it is what every comparison below
// uses. A Position built by hand should be resolved through an Index first.
type Position struct {
	SectionID  string `json:"sectionId"`
	BlockID    string `json:"blockId"`
	CharOffset int    `json:"charOffset"`
	Absolute   int    `json:"absolute"`
}

// Zero is the start of a book: nothing has been read yet.
var Zero = Position{}

func (p Position) IsZero() bool { return p == Position{} }

func (p Position) Before(other Position) bool     { return p.Absolute < other.Absolute }
func (p Position) After(other Position) bool      { return p.Absolute > other.Absolute }
func (p Position) AtOrBefore(other Position) bool { return p.Absolute <= other.Absolute }
func (p Position) AtOrAfter(other Position) bool  { return p.Absolute >= other.Absolute }

// Max returns the further of the two, which is how a furthest position moves:
// it never goes backwards when the reader turns back to an earlier scene.
func Max(a, b Position) Position {
	if b.After(a) {
		return b
	}
	return a
}

func Min(a, b Position) Position {
	if b.Before(a) {
		return b
	}
	return a
}

// Range is a half-open span [Start, End) of body text.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

func (r Range) Contains(p Position) bool {
	return p.AtOrAfter(r.Start) && p.Before(r.End)
}

// Overlaps reports whether any part of the range lies within the other.
func (r Range) Overlaps(other Range) bool {
	return r.Start.Before(other.End) && other.Start.Before(r.End)
}

// Length is the number of characters the range covers.
func (r Range) Length() int { return r.End.Absolute - r.Start.Absolute }

// Progress reports how far into a book of the given length a position is, as a
// fraction between 0 and 1. A zero-length book counts as finished.
func Progress(p Position, end Position) float64 {
	if end.Absolute <= 0 {
		return 1
	}
	if p.Absolute <= 0 {
		return 0
	}
	if p.Absolute >= end.Absolute {
		return 1
	}
	return float64(p.Absolute) / float64(end.Absolute)
}
