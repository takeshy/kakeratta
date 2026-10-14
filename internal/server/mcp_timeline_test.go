package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestGetTimelineRechecksOldThreadsAndExplicitMissingIDs(t *testing.T) {
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
	result, err := s.callMCPTool(context.Background(), "get_timeline", json.RawMessage(`{"limit":1,"ids":["answer","missing"]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var response struct {
		Entries []timeline.Entry  `json:"entries"`
		Authors map[string]string `json:"authors"`
		Missing []string          `json:"missing"`
		Next    string            `json:"next"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 3 || len(response.Missing) != 1 || response.Missing[0] != "missing" || response.Next == "" {
		t.Fatalf("response = %s", raw)
	}
	if response.Authors[timeline.User] == "" || response.Authors["gourmet"] == "" {
		t.Fatalf("authors = %v", response.Authors)
	}
}

func TestGetTimelineWithoutIDsKeepsItsPageShape(t *testing.T) {
	s, _ := app(t)
	result, err := s.callMCPTool(context.Background(), "get_timeline", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if _, ok := fields["missing"]; ok {
		t.Fatalf("missing without ids: %s", raw)
	}
	if string(fields["entries"]) != "[]" {
		t.Fatalf("entries = %s", fields["entries"])
	}
}

func TestSyncTimelineIsNoLongerOffered(t *testing.T) {
	for _, tool := range kakerattaMCPTools() {
		if tool.Name == "sync_timeline" {
			t.Fatal("sync_timeline is still listed")
		}
	}
}

// An ID no entry could have is missing, not a failure of the whole read.
func TestGetTimelineListsMalformedIDsAsMissing(t *testing.T) {
	s, w := app(t)
	at := time.Now().AddDate(-1, 0, 0)
	if err := w.Append(timeline.Entry{ID: "old", At: at, Kind: timeline.KindPost, Speaker: timeline.User, Text: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(timeline.Entry{ID: "new", At: time.Now(), Kind: timeline.KindPost, Speaker: timeline.User, Text: "new"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.callMCPTool(context.Background(), "get_timeline", json.RawMessage(`{"limit":1,"ids":["","a/b","..","line\nbreak","old"]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var response struct {
		Entries []timeline.Entry `json:"entries"`
		Missing []string         `json:"missing"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 || len(response.Missing) != 4 {
		t.Fatalf("response = %s", raw)
	}
}
