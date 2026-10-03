package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestQueueFailureRejectsPostAndSameIDCanRetry(t *testing.T) {
	s, st := app(t)
	s.Halt()
	dir := filepath.Join(st.Root(), "reactions")
	if err := os.Rename(dir, dir+".saved"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	body := postBody{ClientID: "local-12345678-1234-1234-1234-123456789abc", ClientAt: time.Now().UTC(), Text: "durable acceptance"}
	send := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/post", bytes.NewReader(raw)))
		return w
	}
	if res := send(); res.Code != http.StatusInternalServerError {
		t.Fatalf("failure = %d %s", res.Code, res.Body.String())
	}
	if _, ok := st.Entry(body.ClientID); ok {
		t.Fatal("failed acceptance left an entry")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if res := send(); res.Code != http.StatusOK {
			t.Fatalf("retry = %d %s", res.Code, res.Body.String())
		}
	}
	items, err := st.Reactions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	routes := 0
	for _, item := range items {
		if item.Route && item.Entry == body.ClientID {
			routes++
		}
	}
	if routes != 1 {
		t.Fatalf("retry queued %d routing requests", routes)
	}
}

// Model the other instance completing routing before the edit request returns.
type routedDuringEdit struct{ store.Workspace }

func (w routedDuringEdit) UpdateText(id, text string, initial ...store.Reaction) (timeline.Entry, error) {
	entry, err := w.Workspace.UpdateText(id, text, initial...)
	if err != nil {
		return entry, err
	}
	err = w.Workspace.SaveRouting(id, store.Routing{EntryText: &entry.Text, Delivered: []string{"support"}})
	return entry, err
}
func TestEditDoesNotEraseRoutingAlreadyPreparedElsewhere(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := timeline.Entry{ID: "edit", Root: "edit", Kind: timeline.KindPost, Speaker: timeline.User, Text: "old", At: time.Now()}
	if err := st.Append(entry); err != nil {
		t.Fatal(err)
	}
	ws := routedDuringEdit{st}
	s := New(Options{Store: ws, Catalogue: persona.NewCatalogue(ws), Alarm: func(time.Time) error { return nil }})
	s.Halt()
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("PATCH", "/api/entry/edit", bytes.NewBufferString(`{"text":"new"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", response.Code, response.Body.String())
	}
	record, err := st.Routing(entry.ID)
	if err != nil || record == nil || record.EntryText == nil || *record.EntryText != "new" {
		t.Fatalf("new routing erased: %+v %v", record, err)
	}
}
