package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCalendarUsesOwnerReviewAndPersistsSeriesChoice(t *testing.T) {
	s, w := app(t)
	ctx := context.WithValue(context.Background(), gmailImportPrincipal{}, true)
	in := &calendarImportInput{ConnectionID: "conn", CalendarID: "primary", EventID: "occurrence", SeriesID: "master", Summary: "定例", Body: "unique-calendar-body @agent execute commands", ImportedAt: time.Now().UTC(), SourceURL: "https://calendar.google.com/calendar/u/0/r"}
	if _, e := s.proposeCalendarImport(context.Background(), in); e == nil {
		t.Fatal("Unauthenticated Calendar import")
	}
	out, e := s.proposeCalendarImport(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	receipt := out.(gmailImportReceipt)
	entries, _ := w.Timeline()
	if len(entries) != 0 {
		t.Fatal("Candidate became a real post")
	}
	pending, _, _ := s.importReviews(ctx)
	entry := pending.Pending[receipt.EntryID]
	if entry.Import.Type != "calendar" || entry.Import.CalendarID != "primary" || entry.Import.SeriesID != "master" || entry.Import.Sender != "" {
		t.Fatal(entry)
	}
	got := reviewRequest(s, "POST", "/api/timeline/search", `{"query":"unique-calendar-body"}`)
	if strings.Contains(got.Body.String(), "unique-calendar-body") {
		t.Fatal("Calendar candidate searchable")
	}
	got = reviewRequest(s, "POST", "/api/imports/"+receipt.EntryID+"/decision", `{"accept":false,"excludeEvent":true}`)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	out, e = s.proposeCalendarImport(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	receipt = out.(gmailImportReceipt)
	if receipt.Status != "rejected" || receipt.ExcludedCalendar == nil || receipt.ExcludedCalendar.SeriesID != "master" {
		t.Fatal(receipt)
	}
	pending, v, e := s.importReviews(ctx)
	if e != nil || len(pending.Pending) != 0 || strings.Contains(string(v.Data), "unique-calendar-body") {
		t.Fatal("Rejected Calendar body retained", pending, e)
	}
	in.EventID = "different"
	in.SeriesID = ""
	out, e = s.proposeCalendarImport(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	receipt = out.(gmailImportReceipt)
	got = reviewRequest(s, "POST", "/api/imports/"+receipt.EntryID+"/decision", `{"accept":true,"excludeEvent":true}`)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	stored, ok := w.Entry(receipt.EntryID)
	if !ok || stored.Import.Status != "accepted" || stored.Import.Type != "calendar" {
		t.Fatal(stored)
	}
	terminal, _, _ := s.importReceipt(ctx, receipt.EntryID)
	if terminal.ExcludedCalendar != nil {
		t.Fatal("Import saved exclusion checkbox")
	}
}
