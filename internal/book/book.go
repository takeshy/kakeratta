// Package book holds the parsed structure of a book and the reader's state in
// it. Both are plain data: parsing lives in internal/ingest, persistence in
// internal/store, and the knowledge boundary in internal/knowledge.
package book

import (
	"time"

	"github.com/takeshy/dokking/internal/position"
)

// Category is what kind of book this is, and it decides everything about how
// the AI is allowed to see it.
//
// The boundary is a property of the book, not something a persona is trusted to
// respect: a novel hides what is past the reader, a technical book brings the
// future in on purpose. Both are the same mechanism with the sign flipped, so
// this one field is what the rest of the app branches on.
type Category string

const (
	// CategoryUnknown means the reader has not chosen yet. Nothing that sends
	// text to a model may run until they have.
	CategoryUnknown   Category = ""
	CategoryNovel     Category = "novel"
	CategoryTechnical Category = "technical"
)

func (c Category) Valid() bool { return c == CategoryNovel || c == CategoryTechnical }

// Format of the source file.
type Format string

const (
	FormatAozora Format = "aozora"
	FormatEPUB   Format = "epub"
	FormatText   Format = "text"
	FormatPDF    Format = "pdf"
)

// BlockKind distinguishes what a block is for display and for read-aloud: a
// heading is spoken differently, an image is skipped.
type BlockKind string

const (
	BlockParagraph BlockKind = "paragraph"
	BlockHeading   BlockKind = "heading"
	BlockImage     BlockKind = "image"
)

// Block is the smallest addressable unit of a book: one paragraph, heading or
// image.
//
// Plain is the body text and nothing else: every offset, and therefore every
// bookmark, highlight and search hit, is counted over it. What the source adds
// for the eye - ruby readings, editorial annotations - is kept beside it with
// offsets into it, so the display can show them without ever moving a position.
type Block struct {
	ID    string         `json:"id"`
	Kind  BlockKind      `json:"kind"`
	Plain string         `json:"plain"`
	Ruby  []Ruby         `json:"ruby,omitempty"`
	Notes []Note         `json:"notes,omitempty"`
	Image string         `json:"image,omitempty"`
	Range position.Range `json:"range"`
}

// Ruby is a reading shown above a run of the body text.
type Ruby struct {
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Text   string `json:"text"`
}

// Note is an editorial annotation shown at a point in the body text, such as
// Aozora Bunko's ［＃...］ instructions. It is never part of Plain, so it is
// never indexed, never read aloud and never shown to a model.
type Note struct {
	Offset int    `json:"offset"`
	Text   string `json:"text"`
}

// Section is a chapter or a spine item: the unit the ingest pipeline summarises
// and the unit it can retry on its own.
type Section struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Level  int            `json:"level"`
	Blocks []Block        `json:"blocks"`
	Range  position.Range `json:"range"`
}

// Book is the parsed work.
//
// End is where the file ends; ContentEnd is where the work itself ends, before
// an afterword, commentary or colophon. Finishing is derived from ContentEnd so
// an EPUB with fifty pages of appendix can still be finished.
type Book struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Author     string            `json:"author"`
	Category   Category          `json:"category"`
	Format     Format            `json:"format"`
	Source     string            `json:"source"`
	AddedAt    time.Time         `json:"addedAt"`
	Sections   []Section         `json:"sections"`
	End        position.Position `json:"end"`
	ContentEnd position.Position `json:"contentEnd"`
	// Speakable is false when the text layer is too broken to read out loud.
	//
	// Only read-aloud depends on the quality of that layer. Display, selection
	// and quoting do not - a quote is an anchor on a range, and it asks nothing
	// of reading order - so such a book is still perfectly readable by eye and
	// is offered without the listening half rather than refused.
	Speakable bool `json:"speakable"`
}

// ReaderState is where the reader is, and which parts of the book they have
// actually read.
//
// Current moves both ways; Entries only ever gain ground. The AI's boundary is
// the far end of Entries, so going back to an earlier scene - the feature the
// app exists for - never rewinds what the AI knows.
//
// Furthest is kept as a derived mirror of that far end. It is stored so state
// written by an older version still opens, and so the shelf can draw progress
// without walking the intervals.
type ReaderState struct {
	BookID   string            `json:"bookId"`
	Current  position.Position `json:"current"`
	Furthest position.Position `json:"furthest"`
	Entries  []ReadEntry       `json:"entries"`
	Personas []string          `json:"personas"`
	// Ready is set once the reader has been through the opening choices. It is
	// what tells "reading with nobody" - a proper way to read - apart from
	// "has not chosen yet", which no count of personas can.
	Ready      bool       `json:"ready"`
	Mode       string     `json:"mode"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// MaxPersonas is the ceiling from the spec. Three voices about one book is
// already a crowded room; the limit is there so a page event cannot fan out
// into an unbounded number of model calls.
const MaxPersonas = 3

// Boundary is how far the reader has read: the far end of the read intervals.
// Everything the AI may see about a novel is cut off here.
func (s *ReaderState) Boundary() position.Position {
	boundary := s.Furthest
	for _, entry := range s.Entries {
		boundary = position.Max(boundary, entry.Range.End)
	}
	return boundary
}

// Record adds a span the reader actually stayed on. It returns the part that
// was not already covered - what is newly read, and therefore what the
// summaries and the personas have to catch up with.
func (s *ReaderState) Record(span position.Range, at time.Time) []position.Range {
	fresh := s.record(span, at)
	s.Furthest = s.Boundary()
	return fresh
}

// HasPersona reports whether this persona is currently in the book.
func (s *ReaderState) HasPersona(id string) bool {
	for _, existing := range s.Personas {
		if existing == id {
			return true
		}
	}
	return false
}

// IsCompleted is derived, never stored: the reader has finished when the far
// end of what they have read reaches the end of the work's body. Gaps in the
// middle do not matter - they know they finished it, and keeping the review
// shut over a skipped chapter would only be pedantic.
//
// It is a novel's notion. A technical book is never "finished", so nothing is
// unlocked by it there.
func (b *Book) IsCompleted(s ReaderState) bool {
	if b.Category == CategoryTechnical {
		return false
	}
	return b.ContentEnd.Absolute > 0 && s.Boundary().AtOrAfter(b.ContentEnd)
}

// Section returns the section with the given id.
func (b *Book) Section(id string) *Section {
	for i := range b.Sections {
		if b.Sections[i].ID == id {
			return &b.Sections[i]
		}
	}
	return nil
}

// SectionAt returns the section containing p, or nil when p is past the end.
func (b *Book) SectionAt(p position.Position) *Section {
	for i := range b.Sections {
		if b.Sections[i].Range.Contains(p) {
			return &b.Sections[i]
		}
	}
	if len(b.Sections) > 0 && p.AtOrAfter(b.End) {
		return &b.Sections[len(b.Sections)-1]
	}
	return nil
}

// Resolve fills in the derived Absolute offset of a position built by a client
// from the canonical triple alone, and clamps it into the book.
func (b *Book) Resolve(p position.Position) position.Position {
	for si := range b.Sections {
		section := &b.Sections[si]
		if section.ID != p.SectionID {
			continue
		}
		for bi := range section.Blocks {
			block := &section.Blocks[bi]
			if block.ID != p.BlockID {
				continue
			}
			offset := p.CharOffset
			if offset < 0 {
				offset = 0
			}
			if length := len([]rune(block.Plain)); offset > length {
				offset = length
			}
			return position.Position{
				SectionID:  section.ID,
				BlockID:    block.ID,
				CharOffset: offset,
				Absolute:   block.Range.Start.Absolute + offset,
			}
		}
	}
	return b.At(p.Absolute)
}

// At returns the canonical position for an absolute offset.
func (b *Book) At(absolute int) position.Position {
	if absolute <= 0 || len(b.Sections) == 0 {
		return position.Zero
	}
	if absolute >= b.End.Absolute {
		return b.End
	}
	for si := range b.Sections {
		section := &b.Sections[si]
		if absolute >= section.Range.End.Absolute {
			continue
		}
		for bi := range section.Blocks {
			block := &section.Blocks[bi]
			if absolute >= block.Range.End.Absolute {
				continue
			}
			offset := absolute - block.Range.Start.Absolute
			if offset < 0 {
				offset = 0
			}
			return position.Position{
				SectionID:  section.ID,
				BlockID:    block.ID,
				CharOffset: offset,
				Absolute:   absolute,
			}
		}
	}
	return b.End
}

// TextUpTo returns the body text from the start of the section containing from
// up to (and not past) the boundary. It is how a prompt gets the reader's
// current surroundings without ever reaching past what they have read.
func (b *Book) TextUpTo(from, boundary position.Position) string {
	if boundary.Before(from) {
		boundary = from
	}
	var out []rune
	for si := range b.Sections {
		section := &b.Sections[si]
		if section.Range.End.AtOrBefore(from) || section.Range.Start.AtOrAfter(boundary) {
			continue
		}
		for bi := range section.Blocks {
			block := &section.Blocks[bi]
			if block.Range.End.AtOrBefore(from) || block.Range.Start.AtOrAfter(boundary) {
				continue
			}
			runes := []rune(block.Plain)
			start := 0
			if from.Absolute > block.Range.Start.Absolute {
				start = from.Absolute - block.Range.Start.Absolute
			}
			end := len(runes)
			if boundary.Absolute < block.Range.End.Absolute {
				end = boundary.Absolute - block.Range.Start.Absolute
			}
			if start < 0 {
				start = 0
			}
			if end > len(runes) {
				end = len(runes)
			}
			if start >= end {
				continue
			}
			if len(out) > 0 {
				out = append(out, '\n')
			}
			out = append(out, runes[start:end]...)
		}
	}
	return string(out)
}
