package pipeline

import (
	"testing"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/position"
)

func longBook(t *testing.T) *book.Book {
	t.Helper()
	paragraph := ""
	for len([]rune(paragraph)) < 800 {
		paragraph += "男は駅の改札を出て、来た道を振り返った。"
	}
	text := "第一章 駅\n\n" + paragraph + "\n\n" + paragraph + "\n\n第二章 屋敷\n\n" + paragraph
	parsed := ingest.ParseText("t", "", text)
	parsed.Category = book.CategoryNovel
	return parsed
}

func TestPagesAreCutOnBlockEdges(t *testing.T) {
	b := longBook(t)
	span := position.Range{Start: position.Zero, End: b.End}
	cut := pages(b, span)
	if len(cut) < 2 {
		t.Fatalf("a book of %d characters made %d pages", b.End.Absolute, len(cut))
	}
	// The pieces have to tile the stretch exactly: a gap is text nothing would
	// ever summarise, and an overlap is a page summarised twice.
	at := span.Start
	for _, page := range cut {
		if page.Start.Absolute != at.Absolute {
			t.Fatalf("page starts at %d, expected %d", page.Start.Absolute, at.Absolute)
		}
		at = page.End
	}
	if at.Absolute != span.End.Absolute {
		t.Fatalf("pages stop at %d, book ends at %d", at.Absolute, span.End.Absolute)
	}
	for _, page := range cut {
		if page.End.Absolute <= page.Start.Absolute {
			t.Fatalf("empty page: %+v", page)
		}
	}
}

func TestPagesNeverReachPastWhatWasRead(t *testing.T) {
	b := longBook(t)
	boundary := b.At(1500)
	for _, page := range pages(b, position.Range{Start: position.Zero, End: boundary}) {
		if page.End.After(boundary) {
			t.Fatalf("a page reaches past the boundary: %+v", page)
		}
	}
}

func TestChaptersComeFromTheFileWhenTheFileHasThem(t *testing.T) {
	b := longBook(t)
	got := chapters(b, knowledge.Knowledge{}, true)
	if len(got) != len(b.Sections) {
		t.Fatalf("chapters = %d, sections = %d", len(got), len(b.Sections))
	}
	if got[0].ID != b.Sections[0].ID {
		t.Fatalf("chapter ids do not match the file's: %q", got[0].ID)
	}
}

// Only a book with no structure of its own falls back to the edges a model
// guessed at. There is no reason to have a model guess at what the EPUB says.
func TestGuessedChapterEdgesAreOnlyForBooksWithoutStructure(t *testing.T) {
	b := longBook(t)
	k := knowledge.Knowledge{Breaks: []position.Position{b.At(900), b.At(1800)}}
	got := chapters(b, k, false)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want two", len(got))
	}
	if got[0].Range.Start.Absolute != 0 || got[0].Range.End.Absolute != 900 {
		t.Fatalf("first segment = %+v", got[0].Range)
	}
	if got[1].Range.Start.Absolute != 900 {
		t.Fatalf("segments do not join: %+v", got[1].Range)
	}
}
