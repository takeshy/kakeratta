package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) previewOrganization(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Request string `json:"request"`
		Planner string `json:"planner"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, "整理の希望を入力してください")
		return
	}
	if body.Planner == "external" {
		connections, err := s.mcpConnections(r.Context())
		if err != nil || len(connections) == 0 {
			fail(w, 400, "外部エージェントを接続してください")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	preview, err := s.runner.PreviewOrganization(ctx, r.PathValue("id"), body.Request, body.Planner)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, preview)
}

func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request) {
	preview, err := s.runner.Organization(r.Context(), r.PathValue("id"), r.URL.Query().Get("token"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, preview)
}

func (s *Server) applyOrganization(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil || body.Token == "" {
		fail(w, 400, "整理案を指定してください")
		return
	}
	if err := s.runner.ApplyOrganization(r.Context(), r.PathValue("id"), body.Token); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"applied": true})
}
