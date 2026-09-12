package knowledge

import (
	"testing"

	"github.com/takeshy/dokking/internal/position"
)

func at(absolute int) position.Position { return position.Position{Absolute: absolute} }

func span(from, to int) position.Range {
	return position.Range{Start: at(from), End: at(to)}
}

func TestReachedIsStrictAtAChapterEdge(t *testing.T) {
	// Chapter one ends and chapter two starts at the same offset. Having read
	// chapter one must not include the first character of chapter two.
	if Reached(at(100), at(100)) {
		t.Fatal("the boundary itself counts as unread")
	}
	if !Reached(at(99), at(100)) {
		t.Fatal("text before the boundary has been read")
	}
}

func TestDigestsAccumulateInReadingOrder(t *testing.T) {
	k := Knowledge{BookID: "b"}
	k.AddDigest(PageDigest{SectionID: "s2", Range: span(100, 200), Text: "駅で男に会う"})
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(0, 100), Text: "家を出る"})
	if len(k.Digests) != 2 || k.Digests[0].Text != "家を出る" {
		t.Fatalf("digests = %v", k.Digests)
	}
	// A page summarised twice is the same page, not a second one.
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(0, 100), Text: "家を出る（やり直し）"})
	if len(k.Digests) != 2 || k.Digests[0].Text != "家を出る（やり直し）" {
		t.Fatalf("digests = %v", k.Digests)
	}
}

func TestNothingPastTheBoundaryComesBack(t *testing.T) {
	k := Knowledge{BookID: "b"}
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(0, 100), Text: "家を出る"})
	k.AddDigest(PageDigest{SectionID: "s2", Range: span(100, 200), Text: "正体が分かる"})
	k.SetSummary(SectionSummary{SectionID: "s1", Position: at(0), End: at(100), Text: "第一章"})
	k.SetSummary(SectionSummary{SectionID: "s2", Position: at(100), End: at(200), Text: "第二章"})

	if got := k.DigestsUpTo(at(100)); len(got) != 1 || got[0].Text != "家を出る" {
		t.Fatalf("digests up to the boundary = %v", got)
	}
	if got := k.SummariesUpTo(at(100)); len(got) != 1 || got[0].Text != "第一章" {
		t.Fatalf("summaries up to the boundary = %v", got)
	}
	// A chapter only half read contributes nothing: its summary covers text the
	// reader has not seen.
	if got := k.SummariesUpTo(at(150)); len(got) != 1 {
		t.Fatalf("a half-read chapter leaked: %v", got)
	}
}

func TestDigestsWithinIsTheSecondStageOfFindingAScene(t *testing.T) {
	k := Knowledge{BookID: "b"}
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(0, 50), Text: "家を出る"})
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(50, 100), Text: "鍵を拾う"})
	k.AddDigest(PageDigest{SectionID: "s2", Range: span(100, 150), Text: "駅に着く"})
	got := k.DigestsWithin(span(0, 100), at(200))
	if len(got) != 2 || got[1].Text != "鍵を拾う" {
		t.Fatalf("digests in the chapter = %v", got)
	}
}

func TestCoversSaysWhatStillNeedsSummarising(t *testing.T) {
	k := Knowledge{BookID: "b"}
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(0, 50), Text: "家を出る"})
	k.AddDigest(PageDigest{SectionID: "s1", Range: span(50, 100), Text: "鍵を拾う"})
	if !k.Covers(span(0, 100)) {
		t.Fatal("two touching digests cover the stretch between them")
	}
	if k.Covers(span(0, 120)) {
		t.Fatal("the last twenty characters have no digest yet")
	}
}
