package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestSearchAndThreadAPIHideDeletedRoots(t *testing.T) {
	s, w := app(t)
	post := timeline.Entry{ID: "old", Kind: timeline.KindPost, Text: "赤ワイン"}
	if err := w.Append(post); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(timeline.Entry{ID: "reply", Kind: timeline.KindReply, Root: "old", Text: "おいしい"}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		rw := httptest.NewRecorder()
		s.ServeHTTP(rw, r)
		return rw
	}
	got := request("POST", "/api/timeline/search", `{"query":"赤ワイン"}`)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "赤ワイン") || !strings.Contains(got.Body.String(), `"semanticUnavailable":true`) {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	got = request("GET", "/api/thread/reply", "")
	if got.Code != 200 || !strings.Contains(got.Body.String(), "赤ワイン") || !strings.Contains(got.Body.String(), "おいしい") {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	got = request("POST", "/api/timeline/search", `{"query":"x","mode":"bogus"}`)
	if got.Code != 400 {
		t.Fatal(got.Code)
	}
	post.Deleted = true
	if err := w.Append(post); err != nil {
		t.Fatal(err)
	}
	got = request("GET", "/api/thread/reply", "")
	if got.Code != 404 {
		t.Fatalf("deleted parent visible: %d", got.Code)
	}
}

// A name on the timeline opens the list of what that speaker said.
func TestSpeakerListIsServed(t *testing.T) {
	server, workspace := app(t)
	base := time.Now().Add(-time.Hour)
	entries := []timeline.Entry{
		{ID: "p1", Kind: timeline.KindPost, Speaker: timeline.User, Text: "父に送った", At: base, Root: "p1"},
		{ID: "r1", Kind: timeline.KindRemark, Speaker: "father", Text: "覚えました", At: base.Add(time.Minute), Root: "p1", ReplyTo: "p1"},
		{ID: "r2", Kind: timeline.KindRemark, Speaker: "father", Text: "届いた頃に聞きますね", At: base.Add(2 * time.Minute), Root: "p1", ReplyTo: "p1"},
	}
	for _, entry := range entries {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) (int, search.Result) {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		var result search.Result
		_ = json.Unmarshal(recorder.Body.Bytes(), &result)
		return recorder.Code, result
	}
	code, result := get("/api/speaker/father?limit=1")
	if code != http.StatusOK || result.Total != 2 || len(result.Hits) != 1 || result.Hits[0].Entry.ID != "r2" {
		t.Fatalf("first page = %d %+v", code, result)
	}
	if _, next := get("/api/speaker/father?limit=1&offset=1"); len(next.Hits) != 1 || next.Hits[0].Entry.ID != "r1" {
		t.Fatalf("second page = %+v", next)
	}
	if code, _ := get("/api/speaker/%20"); code != http.StatusBadRequest {
		t.Fatalf("no speaker = %d", code)
	}
}
