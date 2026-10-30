package server

import (
	"net/http"
	"strings"
)

func (s *Server) runCheckIn(w http.ResponseWriter, r *http.Request) {
	period := r.PathValue("period")
	if period != "daily" && period != "monthly" {
		fail(w, http.StatusBadRequest, "チェックインの頻度が不正です")
		return
	}
	client, settings := s.client()
	if client == nil || client.BaseURL == "" || strings.TrimSpace(client.APIKey) == "" ||
		(settings.OpenRouterMode && strings.TrimSpace(settings.OpenRouterAPIKey) == "") ||
		(settings.MiddleModel == "" && settings.LowModel == "") {
		fail(w, http.StatusBadRequest, "AIの設定を確認してください")
		return
	}
	if err := s.runner.RequestCheckIn(period); err != nil {
		fail(w, http.StatusInternalServerError, "チェックインを受け付けられませんでした")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}
