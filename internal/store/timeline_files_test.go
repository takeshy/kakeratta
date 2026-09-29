package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestDailyReadsAcrossMidnightAndSkipUnrelatedDays(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC)
	entries := []timeline.Entry{
		{ID: "parent", Kind: timeline.KindPost, At: at},
		{ID: "reply", Root: "parent", ReplyTo: "parent", Kind: timeline.KindRemark, At: at.Add(2 * time.Minute)},
		{ID: "old", Kind: timeline.KindPost, At: at.AddDate(-1, 0, 0)},
	}
	for _, entry := range entries {
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	// A broken unrelated day must not affect a selected conversation or page.
	if err := os.WriteFile(s.path("timeline", "2025", "09", "29.json"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	thread, err := SearchThread(context.Background(), s, "reply")
	if err != nil || len(thread) != 2 {
		t.Fatalf("thread = %+v, err = %v", thread, err)
	}
	page, err := s.TimelinePage(context.Background(), 1, "")
	if err != nil || len(page.Entries) != 2 || page.Next == "" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	window, err := SearchTimelineWindow(context.Background(), s, entries[1].At, entries[1].At)
	if err != nil || len(window) != 2 {
		t.Fatalf("window = %+v, err = %v", window, err)
	}
}

func TestDailyTimeZoneAndOriginalBucket(t *testing.T) {
	s := open(t)
	settings := s.Settings()
	settings.TimeZone = "Asia/Tokyo"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC)
	entry := timeline.Entry{ID: "post", Kind: timeline.KindPost, At: at}
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	settings.TimeZone = "UTC"
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	entry.Text = "updated"
	entry.At = at.AddDate(0, 0, 1)
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	var saved []timeline.Entry
	if err := readJSON(s.path("timeline", "2026", "09", "30.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || !saved[0].At.Equal(at) || saved[0].Text != "updated" {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := os.Stat(s.path("timeline", "2026", "09", "29.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected second bucket: %v", err)
	}
}

func TestPhysicalDeletionAcrossDaysSurvivesReopen(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC)
	parent := timeline.Entry{ID: "parent", Kind: timeline.KindPost, At: at}
	reply := timeline.Entry{ID: "reply", Kind: timeline.KindRemark, Root: parent.ID, ReplyTo: parent.ID, At: at.Add(2 * time.Minute)}
	for _, entry := range []timeline.Entry{parent, reply} {
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := s.DeleteEntry(context.Background(), parent.ID)
	if err != nil || len(gone) != 2 {
		t.Fatalf("deleted=%+v, err=%v", gone, err)
	}
	for _, day := range []string{"29.json", "30.json"} {
		if _, err := os.Stat(s.path("timeline", "2026", "09", day)); !os.IsNotExist(err) {
			t.Fatalf("day remains: %v", err)
		}
	}
	if err := s.Append(reply); err == nil {
		t.Fatal("late reply recreated deleted thread")
	}
	if err := s.UpdateEntry(parent); err == nil {
		t.Fatal("late update recreated deleted post")
	}
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reopened.Timeline()
	if err != nil || len(entries) != 0 {
		t.Fatalf("restored=%+v, err=%v", entries, err)
	}
}
