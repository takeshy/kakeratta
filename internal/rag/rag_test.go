package rag

import (
	"strings"
	"testing"

	"github.com/takeshy/dokking/internal/ingest"
)

func TestSearchNeverReturnsUnreadText(t *testing.T) {
	parsed := ingest.ParseText("t", "", strings.Join([]string{
		"第一章 駅", "", "男は駅で鍵を拾った。それは古い鍵だった。", "",
		"第二章 屋敷", "", "屋敷の扉が鍵で開いた。犯人は執事だった。",
	}, "\n"))
	index := Build(parsed)

	// Reading only the first chapter: the ending must not be findable.
	boundary := parsed.Sections[0].Range.End
	hits := index.Search("鍵", boundary, 5)
	if len(hits) == 0 {
		t.Fatal("no hit inside the read range")
	}
	for _, hit := range hits {
		if strings.Contains(hit.Passage.Text, "執事") {
			t.Fatalf("a passage past the boundary was returned: %q", hit.Passage.Text)
		}
		if hit.Passage.Range.Start.After(boundary) {
			t.Fatalf("hit starts past the boundary: %+v", hit.Passage.Range)
		}
	}

	// Having read on, the same query reaches the later scene.
	if hits := index.Search("執事", parsed.End, 5); len(hits) == 0 {
		t.Fatal("the later scene is unreachable after finishing")
	}
}

func TestSearchTruncatesThePassageTheReaderIsStandingIn(t *testing.T) {
	parsed := ingest.ParseText("t", "", "第一章\n\n"+strings.Repeat("鍵の話。", 200))
	index := Build(parsed)
	middle := parsed.At(parsed.End.Absolute / 2)
	for _, hit := range index.Search("鍵", middle, 10) {
		if hit.Passage.Range.Start.Absolute+len([]rune(hit.Passage.Text)) > middle.Absolute+1 {
			t.Fatalf("returned text runs past the boundary: start=%d len=%d boundary=%d",
				hit.Passage.Range.Start.Absolute, len([]rune(hit.Passage.Text)), middle.Absolute)
		}
	}
}
