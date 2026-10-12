package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestTimelineSyncRetriesAndKeepsKakerattaEdits(t *testing.T) {
	s, w := app(t)
	change := timelineSyncChange{ID: "gh-test", At: time.Now().UTC(), Text: "original"}
	for i := 0; i < 2; i++ {
		status, err := s.syncTimelineChange(context.Background(), change)
		if err != nil || status != "applied" {
			t.Fatalf("status=%s err=%v", status, err)
		}
	}
	original, _ := w.Entry("gh-test")
	rev := store.EntryRevision(original)
	if _, err := w.UpdateText("gh-test", "Kakeratta edit"); err != nil {
		t.Fatal(err)
	}
	change.Revision = rev
	change.Text = "GemiHub edit"
	status, err := s.syncTimelineChange(context.Background(), change)
	if err != nil || status != "conflict" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	current, _ := w.Entry("gh-test")
	if current.Text != "Kakeratta edit" {
		t.Fatal("Kakeratta edit was overwritten")
	}
}

func TestTimelineSyncChecksOldThreadsAndExplicitMissingIDs(t *testing.T) {
	s, w := app(t)
	at := time.Now().AddDate(-1, 0, 0)
	for _, entry := range []timeline.Entry{
		{ID: "old", At: at, Kind: timeline.KindPost, Speaker: timeline.User, Text: "old"},
		{ID: "answer", At: at.Add(time.Second), Kind: timeline.KindRemark, Speaker: "gourmet", Root: "old", ReplyTo: "old", Text: "answer"},
		{ID: "new", At: time.Now(), Kind: timeline.KindPost, Speaker: timeline.User, Text: "new"},
	} {
		if err := w.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.callMCPTool(context.Background(), "sync_timeline", json.RawMessage(`{"limit":1,"ids":["old","missing"]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var response struct {
		Records []timelineSyncRecord `json:"records"`
		Missing []string             `json:"missing"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Records) != 3 || len(response.Missing) != 1 || response.Missing[0] != "missing" {
		t.Fatalf("response = %s", raw)
	}
	for _, record := range response.Records {
		if record.AuthorName == "" || record.Revision == "" || record.DeleteRevision == "" {
			t.Fatalf("incomplete record: %+v", record)
		}
	}
}

func TestTimelineSyncDoesNotResurrectDeletedUnacknowledgedCreate(t *testing.T) {
	s, w := app(t)
	change := timelineSyncChange{ID: "gh-lost-response", At: time.Now().UTC(), Text: "original"}
	if status, err := s.syncTimelineChange(context.Background(), change); err != nil || status != "applied" {
		t.Fatalf("status=%s error=%v", status, err)
	}
	if _, err := w.DeleteEntry(context.Background(), change.ID); err != nil {
		t.Fatal(err)
	}
	if status, err := s.syncTimelineChange(context.Background(), change); err != nil || status != "missing" {
		t.Fatalf("status=%s error=%v", status, err)
	}
	if _, exists := w.Entry(change.ID); exists {
		t.Fatal("deleted entry resurrected")
	}
}

func TestTimelineSyncRoutesOnlyNewHumanPostsOnce(t *testing.T) {
	s, w := app(t)
	s.Halt()
	historical := timelineSyncChange{ID: "gh-historical", At: time.Now().AddDate(-1, 0, 0), Text: "historical", React: false}
	fresh := timelineSyncChange{ID: "gh-fresh", At: time.Now(), Text: "fresh", React: true}
	for _, change := range []timelineSyncChange{historical, fresh, fresh} {
		if status, err := s.syncTimelineChange(context.Background(), change); err != nil || status != "applied" {
			t.Fatalf("status=%s error=%v", status, err)
		}
	}
	reactions, err := w.Reactions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]int{}
	for _, reaction := range reactions {
		if reaction.Route {
			routes[reaction.Entry]++
		}
	}
	if routes[historical.ID] != 0 || routes[fresh.ID] != 1 {
		t.Fatalf("routing counts = %v", routes)
	}
}
