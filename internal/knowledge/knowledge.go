// Package knowledge holds the summaries of a book and hands them back only as
// far as the reader has read.
//
// The rule the whole app rests on: for a novel, nothing derived from text past
// the reader's boundary may reach an answer. It holds here by construction
// rather than by filtering. A page digest is written after the reader has read
// that page, so a digest covering unread text is never created in the first
// place - and knowledge that only appears when two pages are combined (the same
// person under two names) cannot be integrated ahead of the reader either.
//
// A technical book is the same mechanism with the sign flipped: its summaries
// are made in one pass at import, because there is nothing to hide.
package knowledge

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/dokking/internal/position"
)

// PageDigest is the one or two lines made for a stretch of text just after the
// reader read it. Digests accumulate; they are never rewritten by a later one.
//
// Compressing them as the book goes would cost exactly what they are for: the
// detail on page 50 is what makes "the bit where he picked up the key" findable
// two hundred pages later.
type PageDigest struct {
	SectionID string         `json:"sectionId"`
	Range     position.Range `json:"range"`
	Text      string         `json:"text"`
	At        time.Time      `json:"at"`
}

// SectionSummary is a chapter compressed once, after the reader has finished
// it. It is positioned at the chapter's start and carries its end, so it can be
// cut off with everything else.
//
// Only confirmed chapters are compressed, so the loss of detail only ever
// happens behind the reader.
type SectionSummary struct {
	SectionID string            `json:"sectionId"`
	Title     string            `json:"title"`
	Position  position.Position `json:"position"`
	End       position.Position `json:"end"`
	Text      string            `json:"text"`
	At        time.Time         `json:"at"`
}

// Knowledge is every summary made for one book.
//
// It belongs to the book, not to a persona: three personas reading the same
// book share it, so a page is summarised once no matter how many are listening.
type Knowledge struct {
	BookID    string              `json:"bookId"`
	Digests   []PageDigest        `json:"digests"`
	Summaries []SectionSummary    `json:"summaries"`
	Breaks    []position.Position `json:"breaks,omitempty"`
}

// AddBreak records a chapter edge the model spotted, for books whose file has
// no structure to read it from. Structure in the file is always preferred:
// there is no reason to have a model guess at something the EPUB already says.
func (k *Knowledge) AddBreak(at position.Position) {
	for _, existing := range k.Breaks {
		if existing.Absolute == at.Absolute {
			return
		}
	}
	k.Breaks = append(k.Breaks, at)
	sort.Slice(k.Breaks, func(i, j int) bool { return k.Breaks[i].Absolute < k.Breaks[j].Absolute })
}

// Reached reports whether text found at p has been read by someone whose
// boundary is at boundary.
//
// The test is strict: a chapter's end and the next chapter's start are the same
// offset, so "read to the end of chapter 1" must not include the first
// character of chapter 2. Everything that filters by position goes through
// here, so the rule is stated once.
func Reached(p, boundary position.Position) bool { return p.Before(boundary) }

// AddDigest files a digest in reading order, replacing one that covers exactly
// the same stretch so a retried page does not appear twice.
func (k *Knowledge) AddDigest(digest PageDigest) {
	if strings.TrimSpace(digest.Text) == "" {
		return
	}
	for i := range k.Digests {
		if k.Digests[i].Range == digest.Range {
			k.Digests[i] = digest
			return
		}
	}
	k.Digests = append(k.Digests, digest)
	sort.SliceStable(k.Digests, func(i, j int) bool {
		return k.Digests[i].Range.Start.Absolute < k.Digests[j].Range.Start.Absolute
	})
}

// SetSummary records a chapter's summary, replacing an earlier one for the same
// chapter.
func (k *Knowledge) SetSummary(summary SectionSummary) {
	if strings.TrimSpace(summary.Text) == "" {
		return
	}
	for i := range k.Summaries {
		if k.Summaries[i].SectionID == summary.SectionID {
			k.Summaries[i] = summary
			return
		}
	}
	k.Summaries = append(k.Summaries, summary)
	sort.SliceStable(k.Summaries, func(i, j int) bool {
		return k.Summaries[i].Position.Absolute < k.Summaries[j].Position.Absolute
	})
}

// Summary returns the chapter's summary if it has been made.
func (k Knowledge) Summary(sectionID string) (SectionSummary, bool) {
	for _, summary := range k.Summaries {
		if summary.SectionID == sectionID {
			return summary, true
		}
	}
	return SectionSummary{}, false
}

// SummariesUpTo returns the chapter summaries the reader has reached. A chapter
// only half read contributes nothing: its summary covers text past the
// boundary.
func (k Knowledge) SummariesUpTo(boundary position.Position) []SectionSummary {
	var out []SectionSummary
	for _, summary := range k.Summaries {
		if summary.End.AtOrBefore(boundary) {
			out = append(out, summary)
		}
	}
	return out
}

// DigestsUpTo returns the page digests inside the boundary. A digest whose
// stretch runs past it is left out whole rather than trimmed: a digest is one
// sentence about a stretch, and half of it is not about half the stretch.
func (k Knowledge) DigestsUpTo(boundary position.Position) []PageDigest {
	var out []PageDigest
	for _, digest := range k.Digests {
		if digest.Range.End.AtOrBefore(boundary) {
			out = append(out, digest)
		}
	}
	return out
}

// DigestsWithin returns the page digests inside one stretch of the book, which
// is the second stage of finding a half-remembered scene: the chapter summary
// says which chapter, and these say where in it.
//
// It narrows by position rather than by chapter id so it works the same for a
// book whose chapters came from the file and one whose chapter edges the model
// had to guess.
func (k Knowledge) DigestsWithin(span position.Range, boundary position.Position) []PageDigest {
	var out []PageDigest
	for _, digest := range k.Digests {
		if digest.Range.Overlaps(span) && digest.Range.End.AtOrBefore(boundary) {
			out = append(out, digest)
		}
	}
	return out
}

// Covers reports whether the digests already account for the whole stretch, so
// a page is not summarised twice when the reader passes over it again.
func (k Knowledge) Covers(span position.Range) bool {
	at := span.Start
	for _, digest := range k.Digests {
		if digest.Range.Start.AtOrBefore(at) && digest.Range.End.After(at) {
			at = digest.Range.End
			if at.AtOrAfter(span.End) {
				return true
			}
		}
	}
	return at.AtOrAfter(span.End)
}

// Lines renders digests for a prompt, each with the offset it covers so an
// answer can point the reader back at the text.
func Lines(digests []PageDigest) string {
	var lines []string
	for _, digest := range digests {
		lines = append(lines, "["+strconv.Itoa(digest.Range.Start.Absolute)+"文字目] "+digest.Text)
	}
	return strings.Join(lines, "\n")
}

// SummaryLines renders chapter summaries for a prompt.
func SummaryLines(summaries []SectionSummary) string {
	var lines []string
	for _, summary := range summaries {
		title := summary.Title
		if title == "" {
			title = "章"
		}
		lines = append(lines, "【"+title+"】"+summary.Text)
	}
	return strings.Join(lines, "\n")
}
