package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestCheckedTimelineUpdatePreservesConcurrentEdit(t *testing.T) {
	s := open(t)
	original := timeline.Entry{ID: "post", Kind: timeline.KindPost, Speaker: timeline.User, Text: "original", At: time.Now()}
	if err := s.Append(original); err != nil {
		t.Fatal(err)
	}
	rev := EntryRevision(original)
	if _, err := s.UpdateText("post", "Kakeratta edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTextChecked("post", "GemiHub edit", rev); !errors.Is(err, ErrEntryConflict) {
		t.Fatalf("error = %v", err)
	}
	current, _ := s.Entry("post")
	if current.Text != "Kakeratta edit" {
		t.Fatal("concurrent edit lost")
	}
}

func TestCheckedDeletionProtectsNewReplies(t *testing.T) {
	s := open(t)
	original := timeline.Entry{ID: "post", Kind: timeline.KindPost, Speaker: timeline.User, Text: "original", At: time.Now()}
	if err := s.Append(original); err != nil {
		t.Fatal(err)
	}
	rev := DeletionRevision([]timeline.Entry{original})
	reply := timeline.Entry{ID: "reply", Kind: timeline.KindReply, Speaker: timeline.User, Root: "post", ReplyTo: "post", Text: "new reply", At: original.At.Add(time.Second)}
	if err := s.Append(reply); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteEntryChecked(context.Background(), "post", rev); !errors.Is(err, ErrEntryConflict) {
		t.Fatalf("error = %v", err)
	}
	if _, exists := s.Entry("reply"); !exists {
		t.Fatal("new reply deleted")
	}
	if _, err := s.UpdateTextChecked("reply", "edited reply", EntryRevision(reply)); err != nil {
		t.Fatal(err)
	}
}
