package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestForgetOneNoteKeepsOtherMemoryAndTimeline(t *testing.T) {
	server, workspace := app(t, "")
	path := "tastes/軽い赤 & wine.md"
	for _, item := range []struct{ persona, path string }{{"gourmet", path}, {"gourmet", "other.md"}, {"book", path}} {
		if err := workspace.WriteNote(item.persona, item.path, "Keep the rest."); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.Append(timeline.Entry{ID: "source", Kind: timeline.KindPost, Speaker: timeline.User, Text: "Original post", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	remove := func(path string) int {
		request := httptest.NewRequest(http.MethodDelete, "/api/personas/gourmet/memory/note?path="+url.QueryEscape(path), nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code
	}
	for _, bad := range []string{"", "../other.md", "missing.md", "/other.md"} {
		if code := remove(bad); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Fatalf("invalid %q: %d", bad, code)
		}
		if len(workspace.Memory("gourmet").Notes) != 2 {
			t.Fatal("invalid request deleted memory")
		}
	}
	if code := remove(path); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if _, ok := workspace.Note("gourmet", path); ok {
		t.Fatal("selected note remains")
	}
	if _, ok := workspace.Note("gourmet", "other.md"); !ok {
		t.Fatal("another note was deleted")
	}
	if _, ok := workspace.Note("book", path); !ok {
		t.Fatal("another persona's note was deleted")
	}
	if _, ok := workspace.Entry("source"); !ok {
		t.Fatal("original post was deleted")
	}
	if code := remove(path); code != http.StatusNotFound {
		t.Fatalf("repeat delete: %d", code)
	}
}

func TestForgetOneNoteRequiresAuthentication(t *testing.T) {
	server, workspace := app(t, "secret")
	if err := workspace.WriteNote("gourmet", "private.md", "Private memory"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/personas/gourmet/memory/note?path=private.md", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d", response.Code)
	}
	if _, ok := workspace.Note("gourmet", "private.md"); !ok {
		t.Fatal("unauthenticated deletion")
	}
}
