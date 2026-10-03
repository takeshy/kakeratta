package store

import (
	"context"
	"github.com/takeshy/kakeratta/internal/timeline"
	"testing"
	"time"
)

func TestUpdatePlacePreservesEditsAndDeletion(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := timeline.Place{Lat: 39, Lon: 140}
	after := before
	after.Label = "田沢湖生保内"
	after.Country = "日本"
	after.Region = "秋田県"
	entry := timeline.Entry{ID: "location", Kind: timeline.KindPost, Speaker: timeline.User, At: time.Now(), Text: "before", Place: &before}
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateText(entry.ID, "edited"); err != nil {
		t.Fatal(err)
	}
	got, changed, err := s.UpdatePlace(context.Background(), entry.ID, before, after)
	if err != nil || !changed || got.Text != "edited" || got.Place.Region != "秋田県" {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	if _, changed, err := s.UpdatePlace(context.Background(), entry.ID, before, timeline.Place{Label: "stale"}); err != nil || changed {
		t.Fatal("stale lookup overwrote location")
	}
	if _, err := s.DeleteEntry(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.UpdatePlace(context.Background(), entry.ID, after, before); err != nil || changed {
		t.Fatal("deleted entry recreated")
	}
}
