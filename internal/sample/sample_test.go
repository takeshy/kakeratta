package sample_test

import (
	"strings"
	"testing"

	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/rag"
	"github.com/takeshy/dokking/internal/sample"
)

// The bundled book is what a first run shows, so it has to parse into the shape
// the rest of the app assumes: several chapters, an afterword outside the body,
// and text that can be searched only as far as the reader has read.
func TestSampleBookIsUsableOutOfTheBox(t *testing.T) {
	parsed, err := ingest.Finalise(ingest.FromText(sample.Title, sample.Author, sample.Text), "sample", sample.Text)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) < 5 {
		t.Fatalf("sections = %d", len(parsed.Sections))
	}
	if !parsed.ContentEnd.Before(parsed.End) {
		t.Fatal("the afterword should sit outside the body")
	}

	index := rag.Build(parsed)
	firstChapterEnd := parsed.Sections[0].Range.End
	for _, hit := range index.Search("鍵", firstChapterEnd, 5) {
		if strings.Contains(hit.Passage.Text, "祖父") {
			t.Fatalf("the reveal is reachable from chapter one: %q", hit.Snippet)
		}
	}
	if len(index.Search("鍵", parsed.End, 5)) == 0 {
		t.Fatal("the key is unfindable even after finishing")
	}
}
