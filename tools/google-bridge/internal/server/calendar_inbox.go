package googlebridge

import (
	"context"
	googletools "github.com/takeshy/kakeratta/tools/google-bridge/internal/google"
	"net/http"
	"time"
)

func (s *Server) calendarInbox(w http.ResponseWriter, r *http.Request) {
	out, e := s.google.CalendarInbox(r.Context())
	if e != nil {
		googleFailure(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) calendarInboxSettings(w http.ResponseWriter, r *http.Request) {
	var in googletools.CalendarInboxSettings
	if readJSONLimit(r, &in, 128<<10) != nil {
		googleFailure(w, googletools.ErrInput)
		return
	}
	out, e := s.google.SetCalendarInboxSettings(r.Context(), in)
	if e != nil {
		googleFailure(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) calendarInboxSync(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if readJSONLimit(r, &in, 1024) != nil {
		googleFailure(w, googletools.ErrInput)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, e := s.google.SyncCalendar(ctx, true)
	if e != nil {
		googleFailure(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) calendarInboxClear(w http.ResponseWriter, r *http.Request) {
	if e := s.google.ClearCalendarInbox(r.Context()); e != nil {
		googleFailure(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
