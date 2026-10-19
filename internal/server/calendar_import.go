package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type calendarImportExclusion struct {
	CalendarID string `json:"calendarId"`
	EventID    string `json:"eventId"`
	SeriesID   string `json:"seriesId,omitempty"`
	Label      string `json:"label"`
}
type calendarImportInput struct {
	ConnectionID string    `json:"connectionId"`
	CalendarID   string    `json:"calendarId"`
	EventID      string    `json:"eventId"`
	SeriesID     string    `json:"seriesId,omitempty"`
	Summary      string    `json:"summary"`
	Body         string    `json:"body"`
	ImportedAt   time.Time `json:"importedAt"`
	SourceURL    string    `json:"sourceUrl"`
}

func (s *Server) proposeCalendarImport(ctx context.Context, in *calendarImportInput) (any, error) {
	if in == nil || len(in.CalendarID) == 0 || len(in.CalendarID) > 320 || strings.ContainsAny(in.CalendarID, "/\\\r\n\x00") || in.CalendarID == "." || in.CalendarID == ".." || !importID.MatchString(in.EventID) || in.SeriesID != "" && !importID.MatchString(in.SeriesID) {
		return nil, fmt.Errorf("Invalid Calendar import")
	}
	// Same authenticated principal, review lock, durable receipts, owner choice,
	// normal post pipeline and search isolation as mail. No Calendar mutation.
	return s.proposeGmailImport(ctx, &gmailImportInput{SourceType: "calendar", CalendarID: in.CalendarID, EventID: in.EventID, SeriesID: in.SeriesID, ConnectionID: in.ConnectionID, MessageID: in.EventID, ThreadID: in.EventID, Subject: in.Summary, Body: in.Body, ReceivedAt: in.ImportedAt, ImportedAt: in.ImportedAt, SourceURL: in.SourceURL})
}
