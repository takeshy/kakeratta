package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOfflinePostRetryPreservesEntryAndDeletion(t *testing.T) {
	s, st := app(t, "")
	body := postBody{ClientID: "local-12345678-1234-1234-1234-123456789abc", ClientAt: time.Now().Add(-time.Hour).UTC(), Text: "offline"}
	send := func(body postBody) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/post", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	first := send(body)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	second := send(body)
	if second.Code != 200 {
		t.Fatal(second.Code, second.Body.String())
	}
	entries, _ := st.Timeline()
	if len(entries) != 1 {
		t.Fatalf("duplicate entries: %d", len(entries))
	}
	if !entries[0].At.Equal(body.ClientAt) {
		t.Fatal("offline creation time lost")
	}
	original := entries[0]
	original.Deleted = true
	st.Append(original)
	if send(body).Code != 200 {
		t.Fatal("retry after deletion failed")
	}
	original, _ = st.Entry(body.ClientID)
	if !original.Deleted {
		t.Fatal("retry resurrected deletion")
	}
	body.Text = "different"
	if send(body).Code != 409 {
		t.Fatal("reused ID accepted different content")
	}
}
func TestOfflineIdentityRejectsWrongWorkspace(t *testing.T) {
	s, _ := app(t, "")
	r := httptest.NewRequest("POST", "/api/post", bytes.NewBufferString(`{"text":"wrong account"}`))
	r.Header.Set("X-Kakeratta-Workspace", "another-user")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
