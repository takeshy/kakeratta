// Package server is the HTTP surface: the API and the live channel the
// reactions come back on. The page is served elsewhere and only ever reaches
// this through /api.
//
// Nothing here decides anything about a post. It takes what came in, stores it,
// hands it to the runner and gets out of the way.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Options is everything the server is handed.
type Options struct {
	Store     store.Workspace
	Catalogue *persona.Catalogue
	Version   string
	Token     string
	// Tools is the operator's configuration, shared by every workspace.
	Tools tools.Config
}

// Server is the app.
type Server struct {
	store     store.Workspace
	catalogue *persona.Catalogue
	runner    *persona.Runner
	tools     tools.Config
	version   string
	token     string
	search    *search.Service

	mux       *http.ServeMux
	dailyStop chan struct{}
	dailyDone chan struct{}
	closeOnce sync.Once
	events    *hub
}

func New(options Options) *Server {
	s := &Server{
		store:     options.Store,
		catalogue: options.Catalogue,
		version:   options.Version,
		token:     options.Token,
		tools:     options.Tools,
		mux:       http.NewServeMux(),
		events:    newHub(),
	}
	s.runner = persona.New(s.store, s.catalogue, s.client)
	s.search = search.New(s.store)
	s.runner.SetSearch(s.search)
	s.runner.SetTools(s.tools)
	s.runner.OnEvent(s.events.send)
	s.runner.Start()
	s.routes()
	s.dailyStop = make(chan struct{})
	s.dailyDone = make(chan struct{})
	go func() {
		defer close(s.dailyDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		s.runner.CheckDaily()
		for {
			select {
			case <-s.dailyStop:
				return
			case <-ticker.C:
				s.runner.CheckDaily()
			}
		}
	}()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Close lets whatever is reacting finish. Reactions write as they go, so
// exiting in the middle of one leaves a persona's notes half written.
func (s *Server) Close() {
	s.closeOnce.Do(func() { close(s.dailyStop); <-s.dailyDone; s.runner.Stop() })
}

func (s *Server) client() (*llm.Client, store.Settings) {
	settings := s.store.Settings()
	client := llm.New(settings.BaseURL, settings.APIKey)
	return client, settings
}

func (s *Server) knownTool(name string) bool {
	for _, known := range tools.New(s.tools).Names() {
		if known == name {
			return true
		}
	}
	return false
}

// ---- plumbing --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(r *http.Request, target any) error {
	return readJSONLimit(r, target, 8<<20)
}

func readJSONLimit(r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit)).Decode(target)
}

// hub fans one event out to every open page.
//
// Reactions arrive late by design, so the page cannot ask for them: it has to
// be told. A dropped send is fine - the page reloads the timeline when it
// reconnects.
type hub struct {
	mu       sync.Mutex
	watchers map[chan message]bool
}

type message struct {
	Name string `json:"name"`
	Data any    `json:"data"`
}

func newHub() *hub { return &hub{watchers: map[chan message]bool{}} }

func (h *hub) join() chan message {
	channel := make(chan message, 16)
	h.mu.Lock()
	h.watchers[channel] = true
	h.mu.Unlock()
	return channel
}

func (h *hub) leave(channel chan message) {
	h.mu.Lock()
	delete(h.watchers, channel)
	h.mu.Unlock()
	close(channel)
}

func (h *hub) send(name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for channel := range h.watchers {
		select {
		case channel <- message{Name: name, Data: data}:
		default:
		}
	}
}

// events streams to one page.
func (s *Server) events2(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "この接続では受け取れません")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	channel := s.events.join()
	defer s.events.leave(channel)

	// A heartbeat keeps proxies from closing an idle stream, which matters here
	// because idle is the normal state: nobody is meant to be reacting most of
	// the time.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case event := <-channel:
			raw, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("data: " + string(raw) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func trimmed(value string) string { return strings.TrimSpace(value) }
