package server

import (
	"context"
	"net/http"
	"time"
)

// previewReply runs a persona over a post again, with its tone and
// instructions as they are now saved, and returns what it would say. Nothing
// is delivered or written. A persona's turn can take a while - it reads, it
// may look things up, it may be reviewed - so this waits longer than most.
func (s *Server) previewReply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Persona string `json:"persona"`
	}
	if err := readJSON(r, &body); err != nil || body.Persona == "" {
		fail(w, http.StatusBadRequest, "担当を指定してください")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	preview, err := s.runner.PreviewReply(ctx, r.PathValue("id"), body.Persona)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) applyReply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil || body.Token == "" {
		fail(w, http.StatusBadRequest, "試し書きを指定してください")
		return
	}
	if err := s.runner.ApplyReply(body.Token, r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"applied": true})
}
