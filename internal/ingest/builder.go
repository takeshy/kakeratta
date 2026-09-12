// Package ingest turns a source file into the canonical structure: sections,
// blocks and the positions that every other part of the app is expressed in.
//
// Nothing here splits by bytes or pages. A chapter cut in half breaks a
// character's first appearance, and the pipeline that summarises and extracts
// entities works one section at a time for exactly that reason.
package ingest

import (
	"fmt"
	"strings"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/position"
)

// builder assigns identifiers and positions while a parser walks a source.
// Offsets are counted over plain text, so display markup never moves them.
type builder struct {
	sections []book.Section
	absolute int
}

func (b *builder) startSection(title string, level int) {
	b.closeSection()
	id := fmt.Sprintf("s%d", len(b.sections)+1)
	b.sections = append(b.sections, book.Section{
		ID:    id,
		Title: strings.TrimSpace(title),
		Level: level,
		Range: position.Range{Start: position.Position{SectionID: id, Absolute: b.absolute}},
	})
}

func (b *builder) current() *book.Section {
	if len(b.sections) == 0 {
		b.startSection("", 1)
	}
	return &b.sections[len(b.sections)-1]
}

// addBlock appends one paragraph, heading or image. plain is the body text that
// offsets are counted over; ruby and notes decorate it for display only.
func (b *builder) addBlock(kind book.BlockKind, plain string, ruby []book.Ruby, notes []book.Note, image string) {
	plain = strings.TrimSpace(plain)
	if plain == "" && image == "" {
		return
	}
	section := b.current()
	id := fmt.Sprintf("%s-b%d", section.ID, len(section.Blocks)+1)
	start := position.Position{SectionID: section.ID, BlockID: id, Absolute: b.absolute}
	b.absolute += len([]rune(plain))
	end := position.Position{
		SectionID:  section.ID,
		BlockID:    id,
		CharOffset: len([]rune(plain)),
		Absolute:   b.absolute,
	}
	section.Blocks = append(section.Blocks, book.Block{
		ID:    id,
		Kind:  kind,
		Plain: plain,
		Ruby:  ruby,
		Notes: notes,
		Image: image,
		Range: position.Range{Start: start, End: end},
	})
}

func (b *builder) closeSection() {
	if len(b.sections) == 0 {
		return
	}
	section := &b.sections[len(b.sections)-1]
	section.Range.End = position.Position{SectionID: section.ID, Absolute: b.absolute}
	if blocks := section.Blocks; len(blocks) > 0 {
		section.Range.End = blocks[len(blocks)-1].Range.End
	}
}

// finish drops empty sections and returns the structure with the book's end.
func (b *builder) finish() ([]book.Section, position.Position) {
	b.closeSection()
	kept := b.sections[:0]
	for _, section := range b.sections {
		if len(section.Blocks) > 0 {
			kept = append(kept, section)
		}
	}
	b.sections = kept
	if len(b.sections) == 0 {
		return nil, position.Zero
	}
	last := b.sections[len(b.sections)-1]
	return b.sections, last.Range.End
}

// trailingTitles name the parts that follow the work itself. Finishing a book
// is measured against the end of the body, or an EPUB with a long afterword
// would never count as read.
var trailingTitles = []string{
	"あとがき", "後書き", "訳者あとがき", "解説", "解題", "奥付", "底本", "付録", "初出",
	"afterword", "appendix", "acknowledg", "about the author", "colophon", "notes",
	"copyright", "glossary", "index",
}

func looksTrailing(title string) bool {
	lowered := strings.ToLower(strings.TrimSpace(title))
	if lowered == "" {
		return false
	}
	for _, marker := range trailingTitles {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// contentEnd is where the work's body stops. Only a trailing run of matching
// sections counts: a chapter called "Notes" in the middle of a book is body.
func contentEnd(sections []book.Section, end position.Position) position.Position {
	cut := len(sections)
	for i := len(sections) - 1; i >= 0; i-- {
		if !looksTrailing(sections[i].Title) {
			break
		}
		cut = i
	}
	if cut == 0 || cut == len(sections) {
		return end
	}
	return sections[cut].Range.Start
}
