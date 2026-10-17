package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestMarkScheduledEntryDone(t *testing.T) {
	server, workspace := app(t)
	now := time.Now()
	for _, e := range []timeline.Entry{
		{ID: "s", Kind: timeline.KindPost, Speaker: "cafe", Root: "s", Text: "花、買った?", At: now, Scheduled: true},
		{ID: "p", Kind: timeline.KindPost, Speaker: "cafe", Root: "p", Text: "ふつうの投稿", At: now},
	} {
		if err := workspace.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path string) (*httptest.ResponseRecorder, timeline.Entry) {
		t.Helper()
		out := httptest.NewRecorder()
		server.ServeHTTP(out, httptest.NewRequest(method, path, nil))
		var entry timeline.Entry
		if out.Code == 200 {
			if err := json.Unmarshal(out.Body.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
		}
		return out, entry
	}

	out, first := request("PUT", "/api/entry/s/done")
	if out.Code != 200 || first.DoneAt == nil {
		t.Fatalf("mark: %d %s", out.Code, out.Body.String())
	}
	if !strings.Contains(first.Render(nil, time.UTC), "済みの印") {
		t.Fatal("persona context does not show the done mark")
	}
	_, again := request("PUT", "/api/entry/s/done")
	if again.DoneAt == nil || !again.DoneAt.Equal(*first.DoneAt) {
		t.Fatal("a repeated mark moved the done time")
	}
	if saved, _ := workspace.Entry("s"); saved.DoneAt == nil {
		t.Fatal("done mark was not stored")
	}
	if out, cleared := request("DELETE", "/api/entry/s/done"); out.Code != 200 || cleared.DoneAt != nil {
		t.Fatalf("clear: %d %s", out.Code, out.Body.String())
	}
	if saved, _ := workspace.Entry("s"); saved.DoneAt != nil {
		t.Fatal("done mark was not cleared")
	}
	for _, id := range []string{"p", "missing"} {
		if out, _ := request("PUT", "/api/entry/"+id+"/done"); out.Code != 404 {
			t.Fatalf("%s: %d %s", id, out.Code, out.Body.String())
		}
	}
}

// The buttons under a check-in post that asked about records answer it once;
// the persona reads the answer with the thread.
func TestAReviewQuestionIsAnsweredByItsButtons(t *testing.T) {
	server, workspace := app(t)
	ask := timeline.Entry{ID: "ask", Kind: timeline.KindPost, Speaker: "gourmet", Root: "ask", Text: "そろそろ忘れていい？", At: time.Now(), Review: timeline.ReviewAsked}
	if err := workspace.Append(ask); err != nil {
		t.Fatal(err)
	}
	answer := func(body string) (int, timeline.Entry) {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/entry/ask/review", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(out, request)
		var entry timeline.Entry
		_ = json.Unmarshal(out.Body.Bytes(), &entry)
		return out.Code, entry
	}
	if code, _ := answer(`{"choice":"maybe"}`); code != 400 {
		t.Fatalf("an unknown answer: %d", code)
	}
	code, entry := answer(`{"choice":"keep"}`)
	if code != 200 || entry.Review != timeline.ReviewKeep || !strings.Contains(entry.Render(nil, time.UTC), "忘れないで") {
		t.Fatalf("keep: %d %+v", code, entry)
	}
	if code, _ := answer(`{"choice":"forget"}`); code != 409 {
		t.Fatalf("a second answer: %d", code)
	}
}
