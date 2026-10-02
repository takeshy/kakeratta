package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestFavoritesListSearchAndDeletion(t *testing.T) {
	server, workspace := app(t)
	now := time.Now()
	for _, e := range []timeline.Entry{
		{ID: "p", Kind: timeline.KindPost, Speaker: timeline.User, Root: "p", Text: "保存", At: now},
		{ID: "r", Kind: timeline.KindRemark, Speaker: "cafe", Root: "p", ReplyTo: "p", Text: "保存した回答", At: now.Add(time.Second)},
		{ID: "q", Kind: timeline.KindPost, Speaker: "move", Root: "q", Text: "保存", At: now.Add(2 * time.Second)},
	} {
		if err := workspace.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		out := httptest.NewRecorder()
		server.ServeHTTP(out, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return out
	}
	for _, id := range []string{"p", "r", "q"} {
		if out := request("PUT", "/api/entry/"+id+"/favorite", ""); out.Code != 200 {
			t.Fatalf("save %s: %d %s", id, out.Code, out.Body.String())
		}
	}
	var first, duplicate favorite
	json.Unmarshal(request("PUT", "/api/entry/r/favorite", "").Body.Bytes(), &first)
	json.Unmarshal(request("PUT", "/api/entry/r/favorite", "").Body.Bytes(), &duplicate)
	if first != duplicate {
		t.Fatal("retry changed saved timestamp")
	}
	list := func(path string) (int, []string) {
		t.Helper()
		out := request("GET", path, "")
		if out.Code != 200 {
			t.Fatal(out.Body.String())
		}
		var page struct {
			Items []favorite `json:"items"`
			Total int        `json:"total"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		return page.Total, ids
	}
	if total, ids := list("/api/favorites?limit=1"); total != 3 || len(ids) != 1 || ids[0] != "q" {
		t.Fatalf("recent order: %d %v", total, ids)
	}
	if _, ids := list("/api/favorites?limit=1&offset=1"); len(ids) != 1 || ids[0] != "r" {
		t.Fatalf("page: %v", ids)
	}
	if total, ids := list("/api/favorites?speaker=cafe"); total != 1 || len(ids) != 1 || ids[0] != "r" {
		t.Fatalf("speaker filter: %d %v", total, ids)
	}
	// Unselected matches preceding a saved reply must not consume its page.
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("unsaved-%d", i)
		if err := workspace.Append(timeline.Entry{ID: id, Kind: timeline.KindPost, Speaker: timeline.User, Root: id, Text: "保存", At: now.Add(time.Hour + time.Duration(i)*time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	out := request("POST", "/api/timeline/search", `{"query":"保存","mode":"keyword","favoritesOnly":true,"limit":1,"offset":1}`)
	var result search.Result
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result.Total != 3 || len(result.Hits) != 1 || result.Hits[0].Entry.ID != "r" {
		t.Fatalf("selected search: %d %s", out.Code, out.Body.String())
	}
	out = request("POST", "/api/timeline/search", `{"query":"","favoritesOnly":true}`)
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result.Total != 3 {
		t.Fatalf("empty selected query: %d %s", out.Code, out.Body.String())
	}
	if out := request("DELETE", "/api/entry/q/favorite", ""); out.Code != 204 {
		t.Fatal(out.Body.String())
	}
	if total, _ := list("/api/favorites"); total != 2 {
		t.Fatalf("remove: %d", total)
	}
	if out := request("DELETE", "/api/entry/p", ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if total, _ := list("/api/favorites"); total != 0 {
		t.Fatalf("deleted favorites: %d", total)
	}
	if records, err := workspace.Records(t.Context(), store.RecordFavorites); err != nil || len(records) != 0 {
		t.Fatalf("cleanup: %v %v", records, err)
	}
	if out := request("PUT", "/api/entry/p/favorite", ""); out.Code != 404 {
		t.Fatalf("deleted save: %d", out.Code)
	}
	out = request("PUT", "/api/entry/q/favorite", "")
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	cross := httptest.NewRequest(http.MethodDelete, "/api/entry/q/favorite", nil)
	cross.Header.Set("Origin", "https://other.example")
	blocked := httptest.NewRecorder()
	server.ServeHTTP(blocked, cross)
	if blocked.Code != 403 {
		t.Fatalf("origin: %d", blocked.Code)
	}
}
