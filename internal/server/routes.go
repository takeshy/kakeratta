package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
)

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/push", s.guard(s.pushConfig))
	s.mux.HandleFunc("PUT /api/push", s.guard(s.savePush))
	s.mux.HandleFunc("DELETE /api/push", s.guard(s.deletePush))
	s.mux.HandleFunc("GET /api/mcp/connection", s.guard(s.mcpConnection))
	s.mux.HandleFunc("PUT /api/mcp/connection", s.guard(s.selectMCPConnection))
	s.mux.HandleFunc("PATCH /api/mcp/connection", s.guard(s.setMCPConnectionWaits))
	s.mux.HandleFunc("POST /api/mcp/connection", s.guard(s.issueMCPKey))
	s.mux.HandleFunc("DELETE /api/mcp/connection", s.guard(s.revokeMCPKey))
	s.mux.HandleFunc("/api/mcp/users/{workspace}", s.serveMCP)
	s.mux.HandleFunc("GET /api/auth/identity", s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(s.store.Root()+"\x00")))
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "enabled": false})
	}))
	s.mux.HandleFunc("POST /api/social/preview", s.guard(s.previewSocial))
	s.mux.HandleFunc("POST /api/social/publish", s.guard(s.publishSocial))
	s.mux.HandleFunc("GET /api/social/bluesky/client-metadata.json", blueskyMetadata)
	s.mux.HandleFunc("GET /api/social/bluesky/connection", s.guard(s.blueskyConnection))
	s.mux.HandleFunc("POST /api/social/bluesky/connect", s.guard(s.connectBluesky))
	s.mux.HandleFunc("GET /api/social/bluesky/callback", s.guard(s.blueskyCallback))
	s.mux.HandleFunc("POST /api/social/bluesky/finish", s.guard(s.finishBluesky))
	s.mux.HandleFunc("DELETE /api/social/bluesky/connection", s.guard(s.disconnectBluesky))
	s.mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
	})
	s.mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	s.mux.HandleFunc("GET /api/favorites", s.guard(s.getFavorites))
	s.mux.HandleFunc("PUT /api/entry/{id}/favorite", s.guard(s.saveFavorite))
	s.mux.HandleFunc("DELETE /api/entry/{id}/favorite", s.guard(s.removeFavorite))
	s.mux.HandleFunc("GET /api/timeline", s.guard(s.getTimeline))
	s.mux.HandleFunc("POST /api/timeline/search", s.guard(s.searchTimeline))
	s.mux.HandleFunc("GET /api/speaker/{id}", s.guard(s.speakerEntries))
	s.mux.HandleFunc("GET /api/thread/{id}", s.guard(s.getThread))
	s.mux.HandleFunc("POST /api/post", s.guard(s.postEntry))
	s.mux.HandleFunc("POST /api/reply", s.guard(s.postReply))
	s.mux.HandleFunc("DELETE /api/entry/{id}", s.guard(s.deleteEntry))
	s.mux.HandleFunc("PATCH /api/entry/{id}", s.guard(s.editEntry))
	s.mux.HandleFunc("GET /api/entry/{id}/routing", s.guard(s.getRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/routing/check", s.guard(s.checkRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/routing/apply", s.guard(s.applyRouting))
	s.mux.HandleFunc("POST /api/entry/{id}/reply/preview", s.guard(s.previewReply))
	s.mux.HandleFunc("POST /api/entry/{id}/reply/apply", s.guard(s.applyReply))
	s.mux.HandleFunc("GET /api/blob/{id}", s.guard(s.getBlob))
	s.mux.HandleFunc("GET /api/events", s.guard(s.events2))
	s.mux.HandleFunc("POST /api/fetch", s.guard(s.postFetch))

	s.mux.HandleFunc("GET /api/personas", s.guard(s.getPersonas))
	s.mux.HandleFunc("PUT /api/personas/{id}/daily", s.guard(s.putDailyCheckIn))
	s.mux.HandleFunc("PUT /api/personas/{id}/planner", s.guard(s.putPlanner))
	s.mux.HandleFunc("PUT /api/personas/{id}/schedules/{schedule}", s.guard(s.putSchedule))
	s.mux.HandleFunc("DELETE /api/personas/{id}/schedules/{schedule}", s.guard(s.deleteSchedule))
	s.mux.HandleFunc("PUT /api/personas", s.guard(s.putPersonas))
	s.mux.HandleFunc("POST /api/personas/build", s.guard(s.buildPersona))
	s.mux.HandleFunc("POST /api/personas/draft", s.guard(s.savePersonaDraft))
	s.mux.HandleFunc("PUT /api/personas/{id}", s.guard(s.savePersonaDraft))
	s.mux.HandleFunc("POST /api/personas/install", s.guard(s.installPersona))
	s.mux.HandleFunc("DELETE /api/personas/{id}", s.guard(s.uninstallPersona))
	s.mux.HandleFunc("GET /api/personas/{id}/memory", s.guard(s.getMemory))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory", s.guard(s.forgetMemory))
	s.mux.HandleFunc("GET /api/personas/{id}/memory/note", s.guard(s.getNote))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory/note", s.guard(s.forgetNote))
	s.mux.HandleFunc("PUT /api/personas/{id}/memory/note", s.guard(s.editNote))
	s.mux.HandleFunc("PUT /api/personas/{id}/memory/fact", s.guard(s.editFact))
	s.mux.HandleFunc("DELETE /api/personas/{id}/memory/fact", s.guard(s.forgetFact))

	s.mux.HandleFunc("GET /api/settings", s.guard(s.getSettings))
	s.mux.HandleFunc("PUT /api/settings", s.guard(s.putSettings))
	s.mux.HandleFunc("GET /api/export", s.guard(s.exportWorkspace))
	s.mux.HandleFunc("POST /api/export", s.guard(s.startExport))
	s.mux.HandleFunc("DELETE /api/workspace", s.guard(s.destroyWorkspace))
	s.mux.HandleFunc("POST /api/search/check", s.guard(s.checkSearch))
	s.mux.HandleFunc("GET /api/models", s.guard(s.getModels))
	s.mux.HandleFunc("GET /api/openrouter/models", s.guard(s.getOpenRouterModels))
	s.mux.HandleFunc("GET /api/about", s.guard(s.getAbout))

	// The dictation stack is reached through the relay so credentials stay out
	// of the browser.

	s.mux.HandleFunc("/", notFound)
}

// notFound is every path that is not the API. The page lives on its own origin
// or dev server; a request for it landing here is a misconfiguration, and a
// JSON error says so faster than a blank file listing would.
func notFound(w http.ResponseWriter, r *http.Request) {
	fail(w, http.StatusNotFound, "not found: "+r.URL.Path)
}
