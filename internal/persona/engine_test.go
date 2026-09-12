package persona

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/dokking/internal/book"
	"github.com/takeshy/dokking/internal/ingest"
	"github.com/takeshy/dokking/internal/knowledge"
	"github.com/takeshy/dokking/internal/position"
	"github.com/takeshy/dokking/internal/store"
)

func runner(t *testing.T) (*Runner, *store.Store, *book.Book) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parsed := ingest.ParseText("t", "", strings.Join([]string{
		"第一章 駅", "", "男は駅で鍵を拾った。", "",
		"第二章 屋敷", "", "犯人は執事だった。",
	}, "\n"))
	parsed.Category = book.CategoryNovel
	if err := s.SaveBook(parsed); err != nil {
		t.Fatal(err)
	}
	return NewRunner(s, NewCatalogue(s.Root()), nil), s, parsed
}

// The whole point of the design: a novel persona's context does not contain the
// text past the reader. It is not filtered out - it is never put in.
func TestANovelPersonaIsNeverHandedUnreadText(t *testing.T) {
	r, _, b := runner(t)
	state := book.ReaderState{}
	state.Record(position.Range{Start: position.Zero, End: b.Sections[0].Range.End}, time.Now())

	k := knowledge.Knowledge{BookID: b.ID}
	k.SetSummary(knowledge.SectionSummary{
		SectionID: b.Sections[0].ID, Title: "第一章",
		Position: b.Sections[0].Range.Start, End: b.Sections[0].Range.End, Text: "駅で鍵を拾う",
	})
	k.SetSummary(knowledge.SectionSummary{
		SectionID: b.Sections[1].ID, Title: "第二章",
		Position: b.Sections[1].Range.Start, End: b.Sections[1].Range.End, Text: "犯人は執事",
	})

	material, anchor, ok := r.material(context.Background(), Builtins[0], b, state, k, TriggerPage, store.Settings{})
	if !ok {
		t.Fatal("nothing to read, but the reader has just read a chapter")
	}
	if !strings.Contains(material, "鍵を拾った") {
		t.Fatalf("the stretch just read is missing: %q", material)
	}
	if strings.Contains(material, "執事") {
		t.Fatalf("text or a summary from past the boundary reached the persona: %q", material)
	}
	if anchor.Absolute != state.Boundary().Absolute {
		t.Fatalf("anchor = %d, want the boundary", anchor.Absolute)
	}
}

// A stretch the reader flicked past is not something the companion beside them
// read either.
func TestSkippedStretchesDoNotReachAPersona(t *testing.T) {
	r, _, b := runner(t)
	state := book.ReaderState{}
	now := time.Now()
	state.Record(position.Range{Start: position.Zero, End: b.Sections[0].Range.End}, now)
	state.Record(position.Range{
		Start: b.Sections[1].Range.End, End: b.ContentEnd,
	}, now.Add(time.Hour))

	material, _, ok := r.material(context.Background(), Builtins[0], b, state, knowledge.Knowledge{}, TriggerPage, store.Settings{})
	if ok && strings.Contains(material, "執事") {
		t.Fatalf("a skipped chapter reached the persona: %q", material)
	}
}

// Waking up with nothing new to read would only cost a model call.
func TestAPageEventWithNothingNewDoesNotWake(t *testing.T) {
	r, s, b := runner(t)
	state := book.ReaderState{}
	state.Record(position.Range{Start: position.Zero, End: b.Sections[0].Range.End}, time.Now())
	memory := s.PersonaState(b.ID, Builtins[0].ID)
	memory.LastProcessed = state.Boundary()
	if err := s.SavePersonaState(memory); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := r.material(context.Background(), Builtins[0], b, state, knowledge.Knowledge{}, TriggerPage, store.Settings{}); ok {
		t.Fatal("woke up with nothing new to read")
	}
}

// A technical persona may see the whole book, and only speaks about what goes
// stale. With no search configured it has no grounds, so it stays quiet.
func TestATechnicalPersonaNeedsSearchBeforeItSpeaksUp(t *testing.T) {
	r, _, b := runner(t)
	b.Category = book.CategoryTechnical
	state := book.ReaderState{Current: position.Zero}
	if _, _, ok := r.material(context.Background(), Builtins[2], b, state, knowledge.Knowledge{}, TriggerPage, store.Settings{}); ok {
		t.Fatal("a page event fired with no search configured and nothing that goes stale")
	}
	// Asked directly, it answers from the book without needing the web.
	if _, _, ok := r.material(context.Background(), Builtins[2], b, state, knowledge.Knowledge{}, TriggerSaid, store.Settings{}); !ok {
		t.Fatal("a technical persona should still answer when spoken to")
	}
}
