package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	kserver "github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestCalendarToRealTimelineSeriesFeedbackAndCancellation(t *testing.T) {
	w, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	app := kserver.New(kserver.Options{Store: w, Catalogue: persona.NewCatalogue(w), Alarm: func(time.Time) error { return nil }})
	t.Cleanup(app.Close)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, httptest.NewRequest("POST", "https://timeline.example/api/mcp/connection", strings.NewReader(`{}`)))
	var key struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	if json.Unmarshal(out.Body.Bytes(), &key) != nil || key.Key == "" {
		t.Fatal(out.Body.String())
	}
	s, items, _ := calendarSetup(t)
	ctx := context.Background()
	enableCalendar(t, s, false)
	base := s.http.Transport
	s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "timeline.example" {
			out := httptest.NewRecorder()
			app.ServeHTTP(out, r)
			return out.Result(), nil
		}
		return base.RoundTrip(r)
	})
	if _, e = s.SetTimelineTarget(ctx, TimelineTargetInput{Enabled: true, URL: "https://timeline.example" + key.Path, Key: key.Key}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SyncCalendar(ctx, false); e != nil {
		t.Fatal(e)
	}
	v, e := s.CalendarInbox(ctx)
	if e != nil || len(v.Events) != 3 {
		t.Fatal(v, e)
	}
	var blocked CalendarInboxEvent
	for _, item := range v.Events {
		if item.EventID == "series_today" {
			blocked = item
		}
	}
	if blocked.TimelineStatus != "pending" || blocked.TimelineEntryID == "" {
		t.Fatal(blocked)
	}
	entries, _ := w.Timeline()
	if len(entries) != 0 {
		t.Fatal("Calendar bypassed review")
	}
	out = httptest.NewRecorder()
	app.ServeHTTP(out, httptest.NewRequest("POST", "/api/imports/"+blocked.TimelineEntryID+"/decision", strings.NewReader(`{"accept":false,"excludeEvent":true}`)))
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if _, e = s.SyncCalendar(ctx, false); e != nil {
		t.Fatal(e)
	}
	v, e = s.CalendarInbox(ctx)
	if e != nil || len(v.Settings.ExcludedEvents) != 1 || v.Settings.ExcludedEvents[0].SeriesID != "series" {
		t.Fatal(v, e)
	}
	settings := v.Settings
	settings.ExcludedEvents = nil
	if _, e = s.SetCalendarInboxSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	// A stale receipt cannot undo the owner's later cancellation.
	r, e := s.read(ctx, "calendar-inbox")
	if e != nil {
		t.Fatal(e)
	}
	r.CalendarInbox.Events = []CalendarInboxEvent{blocked}
	if e = s.publishCalendar(ctx, r.CalendarInbox); e != nil || len(r.CalendarInbox.Settings.ExcludedEvents) != 0 {
		t.Fatal("Cancelled rule restored", e)
	}
	next := (*items)[1]
	next.ID = "new_series_instance"
	*items = append(*items, next)
	if _, e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.CalendarInbox(ctx)
	found := false
	for _, item := range v.Events {
		if item.EventID == next.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("Future recurring occurrence stayed blocked")
	}
}
