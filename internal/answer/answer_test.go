package answer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/rag"
	"github.com/takeshy/dokking/internal/store"
)

func sample(t *testing.T) (*book.Book, *rag.Index) {
	t.Helper()
	parsed := ingest.ParseText("t", "", strings.Join([]string{
		"第一章 駅", "", "男は駅で鍵を拾った。", "",
		"第二章 屋敷", "", "犯人は執事だった。",
	}, "\n"))
	parsed.Category = book.CategoryNovel
	return parsed, rag.Build(parsed)
}

// read marks a stretch as read, which is the only way a boundary moves.
func read(b *book.Book, to position.Position) book.ReaderState {
	state := book.ReaderState{}
	state.Record(position.Range{Start: position.Zero, End: to}, time.Now())
	return state
}

func TestAnUncategorisedBookAnswersNothing(t *testing.T) {
	parsed, index := sample(t)
	parsed.Category = book.CategoryUnknown
	reply, err := Answer(context.Background(), Deps{Book: parsed, Index: index}, "この人だれ")
	if err != nil {
		t.Fatal(err)
	}
	if !reply.Locked {
		t.Fatal("a book with no category must not reach a model")
	}
}

func TestReviewStaysLockedUntilTheBookIsFinished(t *testing.T) {
	parsed, index := sample(t)
	deps := Deps{Book: parsed, Index: index, State: read(parsed, parsed.Sections[0].Range.End)}
	reply, err := Answer(context.Background(), deps, "伏線を振り返って")
	if err != nil {
		t.Fatal(err)
	}
	if !reply.Locked || reply.Text != "" {
		t.Fatalf("review answered before the end: %+v", reply)
	}

	deps.State = read(parsed, parsed.ContentEnd)
	reply, err = Answer(context.Background(), deps, "この本を振り返って")
	if err == nil && reply.Locked {
		t.Fatal("review is still locked after finishing")
	}
}

func TestSceneSourcesStayBehindTheBoundary(t *testing.T) {
	parsed, index := sample(t)
	deps := Deps{Book: parsed, Index: index, State: read(parsed, parsed.Sections[0].Range.End)}
	reply, err := Answer(context.Background(), deps, "鍵を拾った場面はどこ")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Sources) == 0 {
		t.Fatal("no source returned for a scene the reader has read")
	}
	for _, source := range reply.Sources {
		if source.Position.After(deps.State.Boundary()) {
			t.Fatalf("source past the boundary: %+v", source)
		}
		if strings.Contains(source.Snippet, "執事") {
			t.Fatalf("unread text leaked into a snippet: %q", source.Snippet)
		}
	}
}

// The person path has nothing pre-resolved to draw on: it hands over the text
// the reader read. What matters is that the text stops where they did.
func TestTheMaterialAPersonQuestionGetsStopsAtTheBoundary(t *testing.T) {
	parsed, _ := sample(t)
	state := read(parsed, parsed.Sections[0].Range.End)
	deps := Deps{Book: parsed, State: state}
	material := readMaterial(deps, state.Boundary())
	if !strings.Contains(material, "鍵を拾った") {
		t.Fatalf("read text missing: %q", material)
	}
	if strings.Contains(material, "執事") {
		t.Fatalf("text from past the boundary leaked: %q", material)
	}
}

func TestSkippedStretchesAreNotInTheMaterial(t *testing.T) {
	parsed, _ := sample(t)
	state := book.ReaderState{}
	now := time.Now()
	// Read the first chapter, then jump past the second and read the end.
	state.Record(position.Range{Start: position.Zero, End: parsed.Sections[0].Range.End}, now)
	state.Record(position.Range{
		Start: parsed.Sections[1].Range.End, End: parsed.ContentEnd,
	}, now.Add(time.Hour))
	material := readMaterial(Deps{Book: parsed, State: state}, state.Boundary())
	if strings.Contains(material, "執事") {
		t.Fatalf("a skipped chapter reached the prompt: %q", material)
	}
}

// A half-read chapter has no summary, so the recap falls back to the page
// digests for the part being read now.
func TestRecapUsesDigestsForTheChapterInProgress(t *testing.T) {
	parsed, _ := sample(t)
	state := read(parsed, parsed.Sections[0].Range.End)
	k := knowledge.Knowledge{BookID: parsed.ID}
	k.AddDigest(knowledge.PageDigest{
		SectionID: parsed.Sections[0].ID,
		Range:     position.Range{Start: position.Zero, End: parsed.Sections[0].Range.End},
		Text:      "駅で鍵を拾う",
	})
	reply, _ := recap(context.Background(), Deps{Book: parsed, State: state, Knowledge: k},
		state.Boundary(), Reply{Mode: ModeRecap})
	if !strings.Contains(reply.Text, "駅で鍵を拾う") {
		t.Fatalf("the digest of the chapter in progress is missing: %q", reply.Text)
	}
}

// A technical book takes one path, and finishing it unlocks nothing because
// there is nothing to unlock.
func TestATechnicalBookTakesTheTechnicalPath(t *testing.T) {
	parsed, index := sample(t)
	parsed.Category = book.CategoryTechnical
	deps := Deps{Book: parsed, Index: index, State: read(parsed, parsed.Sections[0].Range.End)}
	reply, _ := Answer(context.Background(), deps, "この API はまだ使えますか")
	if reply.Mode != ModeTechnical || reply.Locked {
		t.Fatalf("reply = %+v", reply)
	}
	// The boundary for a technical book is the whole book, not where the reader is.
	if reply.Boundary.Absolute != parsed.End.Absolute {
		t.Fatalf("boundary = %d, want the end of the book", reply.Boundary.Absolute)
	}
}

func TestHistoryIsCarriedButTrimmed(t *testing.T) {
	var turns []store.Turn
	for i := 0; i < 20; i++ {
		turns = append(turns, store.Turn{Role: "user", Text: "質問"}, store.Turn{Role: "assistant", Text: "答え"})
	}
	deps := Deps{History: turns}
	messages := deps.recent()
	if len(messages) != historyTurns {
		t.Fatalf("messages = %d, want %d", len(messages), historyTurns)
	}
	// The most recent exchange has to be the one that survives.
	if messages[len(messages)-1].Content != "答え" || messages[len(messages)-1].Role != "assistant" {
		t.Fatalf("last message = %+v", messages[len(messages)-1])
	}
}

func TestHistoryDropsEmptyTurns(t *testing.T) {
	deps := Deps{History: []store.Turn{{Role: "user", Text: "  "}, {Role: "assistant", Text: "答え"}}}
	if messages := deps.recent(); len(messages) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}
