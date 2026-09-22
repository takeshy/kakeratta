package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestForgetOneNoteKeepsOtherMemoryAndTimeline(t *testing.T) {
	server, workspace := app(t)
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

// A note can be put right by hand - a wine misread off its label stays
// misspelt otherwise - and the links the app keeps from the note to its posts
// survive the edit untouched.
func TestEditingANoteKeepsItsSources(t *testing.T) {
	server, workspace := app(t)
	if err := workspace.WriteNote("gourmet", "wines/altra.md", "type: Wine\ntitle: ALTRA White Blend\n\nノートンの白。", store.NoteSource{EntryID: "p1", Label: "ラベルの写真"}); err != nil {
		t.Fatal(err)
	}
	edit := func(path, body string) (int, string) {
		request := httptest.NewRequest(http.MethodPut, "/api/personas/gourmet/memory/note?path="+url.QueryEscape(path), strings.NewReader(body))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	code, reply := edit("wines/altra.md", `{"text":"type: Wine\ntitle: ALTURA White Blend\n\nノートンの白。"}`)
	if code != http.StatusOK {
		t.Fatalf("edit: %d %s", code, reply)
	}
	var returned store.Note
	_ = json.Unmarshal([]byte(reply), &returned)
	note, _ := workspace.Note("gourmet", "wines/altra.md")
	if !strings.Contains(note.Text, "ALTURA") || strings.Contains(note.Text, "ALTRA White") {
		t.Fatalf("the body was not replaced: %s", note.Text)
	}
	if !strings.Contains(note.Text, "/?entry=p1") {
		t.Fatalf("the source link did not survive the edit: %s", note.Text)
	}
	if returned.Text != note.Text {
		t.Fatal("the reply is not the note as saved")
	}
	// The managed block is the app's; a copy pasted back in does not double it.
	code, _ = edit("wines/altra.md", `{"text":`+strconv.Quote(note.Text+"\n追記")+`}`)
	if code != http.StatusOK {
		t.Fatalf("edit with block: %d", code)
	}
	if note, _ = workspace.Note("gourmet", "wines/altra.md"); strings.Count(note.Text, "/?entry=p1") != 1 {
		t.Fatalf("the source block was doubled: %s", note.Text)
	}
	for _, bad := range []struct{ path, body string }{
		{"wines/missing.md", `{"text":"x"}`}, {"", `{"text":"x"}`}, {"wines/altra.md", `{"text":"  "}`}, {"wines/altra.md", `broken`},
	} {
		if code, _ := edit(bad.path, bad.body); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Fatalf("%q %q: %d", bad.path, bad.body, code)
		}
	}
}

// The notes only grow, so the memory reader asks for them a page at a time,
// narrowed by tag on the server, and fetches a linked note on its own.
func TestMemoryComesInPages(t *testing.T) {
	server, workspace := app(t)
	for i := range 5 {
		tags := "[白]"
		if i%2 == 0 {
			tags = "[赤, 軽め]"
		}
		if err := workspace.WriteNote("gourmet", "wines/"+strconv.Itoa(i)+".md", "---\ntags: "+tags+"\n---\n本文"); err != nil {
			t.Fatal(err)
		}
	}
	get := func(target string) (int, store.MemoryPage) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		var page store.MemoryPage
		_ = json.Unmarshal(response.Body.Bytes(), &page)
		return response.Code, page
	}
	code, first := get("/api/personas/gourmet/memory?limit=2")
	if code != http.StatusOK || len(first.Notes) != 2 || first.Next == "" || len(first.Paths) != 5 {
		t.Fatalf("first page: %d %+v", code, first)
	}
	seen := map[string]bool{}
	for page := first; ; {
		for _, note := range page.Notes {
			if seen[note.Path] {
				t.Fatalf("%s came twice", note.Path)
			}
			seen[note.Path] = true
		}
		if page.Next == "" {
			break
		}
		_, page = get("/api/personas/gourmet/memory?limit=2&after=" + url.QueryEscape(page.Next))
		if page.Paths != nil {
			t.Fatalf("a later page names every note: %+v", page)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("walked %d notes", len(seen))
	}
	if code, _ := get("/api/personas/gourmet/memory?after=wines%2F1.md"); code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", code)
	}
	_, red := get("/api/personas/gourmet/memory?tag=" + url.QueryEscape("赤"))
	if len(red.Notes) != 3 || red.Next != "" {
		t.Fatalf("tagged: %+v", red)
	}
	if err := workspace.WriteNote("gourmet", "shops/a.md", "---\ntype: Shop\ntags: [赤]\n---\n店"); err != nil {
		t.Fatal(err)
	}
	_, shops := get("/api/personas/gourmet/memory?type=shop")
	if len(shops.Notes) != 1 || shops.Notes[0].Path != "shops/a.md" {
		t.Fatalf("by type: %+v", shops)
	}
	_, both := get("/api/personas/gourmet/memory?type=Shop&tag=" + url.QueryEscape("白"))
	if len(both.Notes) != 0 {
		t.Fatalf("by type and tag: %+v", both)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/personas/gourmet/memory/note?path="+url.QueryEscape("wines/4.md"), nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "wines/4.md") {
		t.Fatalf("one note: %d %s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/personas/gourmet/memory/note?path=../x.md", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("outside path: %d", response.Code)
	}
}
