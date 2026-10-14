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
