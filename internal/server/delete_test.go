package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Deleting a post deletes the conversation under it: the personas' remarks and
// the user's own lines, with what the router decided about each. Deleting a
// remark deletes it and what answered it, directly or further down, and
// nothing else.
func TestDeletingAPostDeletesTheConversationUnderIt(t *testing.T) {
	server, workspace := app(t)
	now := time.Now()
	for _, entry := range []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Root: "p", Text: "担々麺", At: now},
		{ID: "r", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "p", ReplyTo: "p", Text: "ごま強め", At: now.Add(time.Second)},
		{ID: "u", Kind: timeline.KindReply, Speaker: timeline.User, Root: "p", ReplyTo: "r", Text: "たしかに", At: now.Add(2 * time.Second)},
		{ID: "q", Kind: timeline.KindPost, Speaker: timeline.User, Root: "q", Text: "別の投稿", At: now.Add(3 * time.Second)},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"p", "u", "q"} {
		if err := workspace.SaveRouting(id, store.Routing{Delivered: []string{"gourmet"}, Excluded: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(id string) int {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/entry/"+id, nil))
		return response.Code
	}
	if code := remove("p"); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	for _, id := range []string{"p", "r", "u"} {
		if entry, ok := workspace.Entry(id); ok {
			t.Fatalf("%s survived its post: %+v", id, entry)
		}
		if record, _ := workspace.Routing(id); record != nil {
			t.Fatalf("%s kept its routing record", id)
		}
	}
	if _, ok := workspace.Entry("q"); !ok {
		t.Fatal("another post was deleted")
	}
	if record, _ := workspace.Routing("q"); record == nil {
		t.Fatal("another post lost its routing record")
	}

	// A remark takes the replies under it, and theirs; a sibling stays.
	for _, entry := range []timeline.Entry{
		{ID: "r2", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "q", ReplyTo: "q", Text: "いいね", At: now.Add(4 * time.Second)},
		{ID: "u2", Kind: timeline.KindReply, Speaker: timeline.User, Root: "q", ReplyTo: "r2", Text: "それ違う", At: now.Add(5 * time.Second)},
		{ID: "r3", Kind: timeline.KindRemark, Speaker: "gourmet", Root: "q", ReplyTo: "u2", Text: "でもいいね", At: now.Add(6 * time.Second)},
		{ID: "s2", Kind: timeline.KindRemark, Speaker: "move", Root: "q", ReplyTo: "q", Text: "別の話", At: now.Add(7 * time.Second)},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if code := remove("r2"); code != http.StatusOK {
		t.Fatalf("delete remark: %d", code)
	}
	for _, id := range []string{"r2", "u2", "r3"} {
		if _, ok := workspace.Entry(id); ok {
			t.Fatalf("%s survived the remark it answered", id)
		}
	}
	if _, ok := workspace.Entry("s2"); !ok {
		t.Fatal("a remark that did not answer it was deleted")
	}
	if _, ok := workspace.Entry("q"); !ok {
		t.Fatal("the remark took its post with it")
	}
}
