package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
	"github.com/takeshy/kakeratta/internal/websearch"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := s.store.ConfiguredSettings()
	// Keys go out to the page only as "set or not". The page never needs them,
	// and a page that does not have them cannot leak them.
	settings.APIKey = masked(settings.APIKey)
	settings.OpenRouterAPIKey = masked(settings.OpenRouterAPIKey)
	settings.Search.APIKey = masked(settings.Search.APIKey)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	current := s.store.ConfiguredSettings()
	next := current
	if err := readJSON(r, &next); err != nil {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	// Persona schedules and planners have their own endpoints; a stale
	// settings form must not undo them.
	next.DailyCheckIns = current.DailyCheckIns
	next.Planners = current.Planners
	// A masked key coming back means "unchanged", not "set it to asterisks".
	reusedKey := next.APIKey == "********" && current.APIKey != ""
	next.APIKey = unmask(next.APIKey, current.APIKey)
	// A saved key belongs to the endpoint it was entered for. Moving it to
	// another address takes the key typed again, so a changed URL can never
	// carry a key it was not given for.
	if reusedKey && next.Endpoint() != current.Endpoint() {
		fail(w, http.StatusBadRequest, "接続先を変えるときはAPIキーを入力し直してください")
		return
	}
	next.OpenRouterAPIKey = unmask(next.OpenRouterAPIKey, current.OpenRouterAPIKey)
	next.Search.APIKey = unmask(next.Search.APIKey, current.Search.APIKey)
	if err := s.store.SaveSettings(next); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Switching the agent off hands its waiting turns back at once, since it
	// can no longer call in to answer them.
	if next.ExternalAgent.Paused && !current.ExternalAgent.Paused && s.runner != nil {
		s.runner.ResetExternalAgent()
	}
	s.scheduleIndex()
	s.getSettings(w, r)
}

func (s *Server) getModels(w http.ResponseWriter, r *http.Request) {
	client, _ := s.client()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := client.Models(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) getOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Settings()
	if strings.TrimSpace(settings.OpenRouterAPIKey) == "" {
		fail(w, http.StatusBadRequest, "OpenRouter APIキーを入力してください")
		return
	}
	client := llm.NewOpenRouter(store.OpenRouterBaseURL, settings.OpenRouterAPIKey)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	check, err := http.NewRequestWithContext(ctx, http.MethodGet, store.OpenRouterBaseURL+"/key", nil)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	check.Header.Set("Authorization", "Bearer "+settings.OpenRouterAPIKey)
	response, err := client.HTTP.Do(check)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		fail(w, http.StatusBadGateway, "OpenRouter APIキーを確認できませんでした")
		return
	}
	models, err := client.Models(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) checkSearch(w http.ResponseWriter, r *http.Request) {
	client := websearch.New(s.store.Settings().Search.APIKey)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := client.Search(ctx, "kakeratta", 1); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) getAbout(w http.ResponseWriter, r *http.Request) {
	about := map[string]any{
		"version": s.version,
		"providers": []map[string]string{
			{"id": string(store.ProviderNebius), "label": "Nebius Token Factory", "endpoint": store.NebiusBaseURL},
		},
		"nemotron": map[string]string{"low": store.NemotronNano, "middle": store.NemotronSuper, "high": store.NemotronUltra},
		// What the operator wired up, so the page can say what personas can reach.
		"tools":      tools.New(s.tools).Names(),
		"mcpServers": s.tools.MCP.Servers(),
	}
	// Where the files are is worth saying only on the machine that holds them.
	// A hosted workspace lives in Firestore and object storage, and the
	// directory left behind is a scratch one nobody can open.
	if s.workspaceKey == "" {
		about["workspace"] = s.store.Root()
	}
	writeJSON(w, http.StatusOK, about)
}

func masked(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	return "********"
}

func unmask(next, current string) string {
	if next == "********" {
		return current
	}
	return next
}
