package book

import (
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/position"
)

func sample() *Book {
	blocks := func(sectionID string, base int, texts ...string) []Block {
		var out []Block
		offset := base
		for i, text := range texts {
			id := sectionID + "-b" + string(rune('1'+i))
			start := position.Position{SectionID: sectionID, BlockID: id, Absolute: offset}
			offset += len([]rune(text))
			out = append(out, Block{
				ID: id, Kind: BlockParagraph, Plain: text,
				Range: position.Range{
					Start: start,
					End:   position.Position{SectionID: sectionID, BlockID: id, CharOffset: len([]rune(text)), Absolute: offset},
				},
			})
		}
		return out
	}
	first := blocks("s1", 0, "あいうえお", "かきくけこ")
	second := blocks("s2", 10, "さしすせそ")
	return &Book{
		ID: "b", Title: "t",
		Sections: []Section{
			{ID: "s1", Blocks: first, Range: position.Range{Start: first[0].Range.Start, End: first[1].Range.End}},
			{ID: "s2", Blocks: second, Range: position.Range{Start: second[0].Range.Start, End: second[0].Range.End}},
		},
		End:        second[0].Range.End,
		ContentEnd: second[0].Range.End,
	}
}

func TestResolveFillsInTheDerivedOffset(t *testing.T) {
	b := sample()
	resolved := b.Resolve(position.Position{SectionID: "s1", BlockID: "s1-b2", CharOffset: 3})
	if resolved.Absolute != 8 {
		t.Fatalf("absolute = %d, want 8", resolved.Absolute)
	}
	// Out-of-range offsets are clamped rather than trusted.
	clamped := b.Resolve(position.Position{SectionID: "s1", BlockID: "s1-b1", CharOffset: 99})
	if clamped.Absolute != 5 {
		t.Fatalf("clamped absolute = %d, want 5", clamped.Absolute)
	}
}

func TestAtRoundTripsWithResolve(t *testing.T) {
	b := sample()
	for _, absolute := range []int{0, 4, 5, 9, 12} {
		at := b.At(absolute)
		if at.Absolute != absolute {
			t.Fatalf("At(%d).Absolute = %d", absolute, at.Absolute)
		}
		if b.Resolve(at).Absolute != absolute {
			t.Fatalf("round trip broke at %d", absolute)
		}
	}
}

func TestTextUpToStopsAtTheBoundary(t *testing.T) {
	b := sample()
	text := b.TextUpTo(position.Zero, b.At(7))
	if text != "あいうえお\nかき" {
		t.Fatalf("text = %q", text)
	}
}

func TestCompletionIsDerivedFromContentEnd(t *testing.T) {
	b := sample()
	// An afterword after the body must not keep the book unfinished.
	b.End = position.Position{Absolute: 40}
	state := ReaderState{}
	state.Record(position.Range{Start: position.Zero, End: b.ContentEnd}, time.Now())
	if !b.IsCompleted(state) {
		t.Fatal("reaching the end of the body should count as finished")
	}
	partly := ReaderState{}
	partly.Record(position.Range{Start: position.Zero, End: b.At(9)}, time.Now())
	if b.IsCompleted(partly) {
		t.Fatal("finished before the body ended")
	}
}
