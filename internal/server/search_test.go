package server

import (
	"net/http/httptest"
	"strings"
	"testing"

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
