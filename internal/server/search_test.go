package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestSearchAndThreadAPIRequireAuthenticationAndHideDeletedRoots(t *testing.T) {
	s, w := app(t, "secret")
	post := timeline.Entry{ID: "old", Kind: timeline.KindPost, Text: "赤ワイン"}
	if err := w.Append(post); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(timeline.Entry{ID: "reply", Kind: timeline.KindReply, Root: "old", Text: "おいしい"}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rw := httptest.NewRecorder()
		s.ServeHTTP(rw, r)
		return rw
	}
	for _, path := range []string{"/api/timeline/search", "/api/thread/old"} {
		method := "GET"
		if strings.HasSuffix(path, "search") {
			method = "POST"
		}
		if got := request(method, path, `{}`, ""); got.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, got.Code)
		}
	}
	got := request("POST", "/api/timeline/search", `{"query":"赤ワイン","mode":"keyword"}`, "secret")
	if got.Code != 200 || !strings.Contains(got.Body.String(), "赤ワイン") {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	got = request("GET", "/api/thread/reply", "", "secret")
	if got.Code != 200 || !strings.Contains(got.Body.String(), "赤ワイン") || !strings.Contains(got.Body.String(), "おいしい") {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	got = request("POST", "/api/timeline/search", `{"query":"x","mode":"bogus"}`, "secret")
	if got.Code != 400 {
		t.Fatal(got.Code)
	}
	post.Deleted = true
	if err := w.Append(post); err != nil {
		t.Fatal(err)
	}
	got = request("GET", "/api/thread/reply", "", "secret")
	if got.Code != 404 {
		t.Fatalf("deleted parent visible: %d", got.Code)
	}
}
