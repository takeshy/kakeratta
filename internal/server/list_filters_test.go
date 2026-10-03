package server

import (
	"bytes"
	"encoding/json"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestListingFiltersBeforePagination(t *testing.T) {
	s, w := app(t)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range []timeline.Entry{
		{ID: "old", Kind: timeline.KindPost, Speaker: timeline.User, Text: "食事", At: day.Add(-24 * time.Hour)},
		{ID: "saved", Kind: timeline.KindRemark, Speaker: "food", Root: "old", ReplyTo: "old", Text: "食事", At: day.Add(time.Hour)},
		{ID: "new", Kind: timeline.KindPost, Speaker: timeline.User, Text: "食事", At: day.Add(2 * time.Hour)},
		{ID: "unsaved", Kind: timeline.KindRemark, Speaker: "food", Root: "new", ReplyTo: "new", Text: "食事", At: day.Add(3 * time.Hour)},
		{ID: "later", Kind: timeline.KindPost, Speaker: timeline.User, Text: "食事", At: day.Add(25 * time.Hour)},
	} {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/entry/saved/favorite", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	q := url.Values{"from": {day.Format(time.RFC3339)}, "to": {day.Add(24*time.Hour - time.Nanosecond).Format(time.RFC3339Nano)}, "favoritesOnly": {"true"}, "limit": {"1"}}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/timeline?"+q.Encode(), nil))
	var page store.TimelinePage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Entries) != 2 || page.Entries[0].ID != "old" || page.Entries[1].ID != "saved" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/speaker/food?"+q.Encode(), nil))
	var result search.Result
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Total != 1 || result.Hits[0].Entry.ID != "saved" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"食事", ""} {
		raw, _ := json.Marshal(map[string]any{"query": query, "mode": "keyword", "favoritesOnly": true, "from": q.Get("from"), "to": q.Get("to")})
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/timeline/search", bytes.NewReader(raw)))
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Total != 1 || result.Hits[0].Entry.ID != "saved" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	q.Del("favoritesOnly")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/timeline?"+q.Encode(), nil))
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Entries) != 2 || page.Entries[0].ID != "new" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	q.Set("from", "invalid")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/timeline?"+q.Encode(), nil))
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}
