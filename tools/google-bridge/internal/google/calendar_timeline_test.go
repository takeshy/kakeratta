package google

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHermesRelaysCalendarSeriesFeedbackAndCancellation(t *testing.T) {
	app, w, path, key := reviewApp(t)
	s, events, _ := calendarSetup(t)
	ctx := context.Background()
	enableCalendar(t, s, false)
	result, e := s.SyncCalendar(ctx, false)
	if e != nil || result.DeliveryPending != 3 {
		t.Fatal(result, e)
	}
	batch, e := s.ReviewContext(ctx, "calendar")
	if e != nil || len(batch.Items) != 3 {
		t.Fatal(batch, e)
	}
	var blocked ReviewItem
	for _, item := range batch.Items {
		receipt := relayReview(t, app, path, key, item)
		if _, e = s.ReviewReceipt(ctx, ReviewReceiptInput{Service: "calendar", Token: item.Token, Receipt: receipt}); e != nil {
			t.Fatal(e)
		}
		if item.Arguments["event"].(map[string]any)["eventId"] == "series_today" {
			blocked = item
		}
	}
	if blocked.Token == "" {
		t.Fatal("series event missing")
	}
	entries, _ := w.Timeline()
	if len(entries) != 0 {
		t.Fatal("Calendar bypassed owner review")
	}
	pending := relayReview(t, app, path, key, blocked)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, httptest.NewRequest("POST", "/api/imports/"+pending.EntryID+"/decision", strings.NewReader(`{"accept":false,"excludeEvent":true}`)))
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	input := ReviewReceiptInput{Service: "calendar", Token: blocked.Token, Receipt: relayReview(t, app, path, key, blocked)}
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal(e)
	}
	view, e := s.CalendarInbox(ctx)
	if e != nil || len(view.Settings.ExcludedEvents) != 1 || view.Settings.ExcludedEvents[0].SeriesID != "series" {
		t.Fatal(view, e)
	}
	cfg := view.Settings
	cfg.ExcludedEvents = nil
	if _, e = s.SetCalendarInboxSettings(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal(e)
	}
	view, _ = s.CalendarInbox(ctx)
	if len(view.Settings.ExcludedEvents) != 0 {
		t.Fatal("stale receipt restored exclusion")
	}
	next := (*events)[1]
	next.ID = "new_series_instance"
	*events = append(*events, next)
	if _, e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	view, _ = s.CalendarInbox(ctx)
	found := false
	for _, item := range view.Events {
		if item.EventID == next.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("future recurring occurrence stayed blocked")
	}
}
