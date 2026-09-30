package search

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A name on the timeline lists what that speaker said, newest first, in the
// pages a search is read in, with the same hits.
func TestBySpeakerListsWhatOneSpeakerSaid(t *testing.T) {
	workspace, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	post := timeline.Entry{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Text: "父に送った", At: base, Root: "p"}
	if err := workspace.Append(post); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		speaker := "father"
		if i%5 == 0 {
			speaker = "mother"
		}
		entry := timeline.Entry{ID: fmt.Sprintf("r%02d", i), Kind: timeline.KindRemark, Speaker: speaker, Text: fmt.Sprintf("発言%d", i), At: base.Add(time.Duration(i+1) * time.Minute), Root: "p", ReplyTo: "p"}
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	service := New(workspace)
	ctx := context.Background()

	first, err := service.BySpeaker(ctx, "father", 10, 0)
	if err != nil || first.Total != 20 || len(first.Hits) != 10 {
		t.Fatalf("first page = %d hits of %d, %v", len(first.Hits), first.Total, err)
	}
	if first.Hits[0].Entry.ID != "r24" || first.Hits[0].Snippet != "発言24" {
		t.Fatalf("newest first: %+v", first.Hits[0])
	}
	second, _ := service.BySpeaker(ctx, "father", 10, 10)
	if len(second.Hits) != 10 || second.Hits[0].Entry.ID == first.Hits[9].Entry.ID {
		t.Fatalf("second page = %+v", second.Hits)
	}
	for _, hit := range append(first.Hits, second.Hits...) {
		if hit.Entry.Speaker != "father" {
			t.Fatalf("someone else's entry: %+v", hit.Entry)
		}
	}
	if past, _ := service.BySpeaker(ctx, "father", 10, 100); len(past.Hits) != 0 || past.Total != 20 {
		t.Fatalf("past the end = %+v", past)
	}
	if own, _ := service.BySpeaker(ctx, timeline.User, 20, 0); own.Total != 1 || own.Hits[0].Entry.ID != "p" {
		t.Fatalf("the user's own = %+v", own)
	}
	if _, err := service.BySpeaker(ctx, "  ", 10, 0); !errors.Is(err, ErrSpeaker) {
		t.Fatalf("no speaker = %v", err)
	}
}
