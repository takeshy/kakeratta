// Package server is the HTTP surface: the API and the live channel the
// reactions come back on. The page is served elsewhere and only ever reaches
// this through /api.
//
// Nothing here decides anything about a post. It takes what came in, stores it,
// hands it to the runner and gets out of the way.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

// Options is everything the server is handed.
type Options struct {
	Store     store.Workspace
	Catalogue *persona.Catalogue
	Version   string
	// Tools is the operator's configuration, shared by every workspace.
	Tools tools.Config
	// Exports is where a finished backup is put for the user to download.
	// Without one the backup is streamed down the response instead.
	Exports store.ExportTarget
	// Follower builds a server on an instance that does not own the
	// workspace: it answers requests and keeps reactions for the owner, but
	// runs no persona work until Lead is called.
	Follower bool
	// Events carries the workspace's events between instances. Without it
	// they stay in this process.
	Events EventBus
}

// EventBus carries one workspace's events to every instance serving it.
type EventBus interface {
	Publish(name string, data []byte) error
	Subscribe(ctx context.Context, deliver func(name string, data []byte))
}

// Server is the app.
type Server struct {
	workspaceKey string
	socialClient func(context.Context) (*social.Client, error)
	oauthClient  func(*oauth.ClientApp)
	store        store.Workspace
	catalogue    *persona.Catalogue
	runner       *persona.Runner
	tools        tools.Config
	version      string
	search       *search.Service
	exports      store.ExportTarget
	exporting    atomic.Bool

	mux         *http.ServeMux
	indexWake   chan struct{}
	indexDone   chan struct{}
	indexCancel context.CancelFunc
	dailyStop   chan struct{}
	dailyDone   chan struct{}
	closeOnce   sync.Once
	events      *hub

	// bus, feed: with an event bus, the hub is fed from it while anyone -
	// an open page, or the persona work of an owner - is listening.
	bus      EventBus
	feedMu   sync.Mutex
	feedRefs int
	feedStop context.CancelFunc

	leadOnce  sync.Once
	leading   atomic.Bool
	watchStop context.CancelFunc
}

func New(options Options) *Server {
	s := &Server{
		store:     options.Store,
		catalogue: options.Catalogue,
		version:   options.Version,
		tools:     options.Tools,
		exports:   options.Exports,
		mux:       http.NewServeMux(),
		events:    newHub(),
		bus:       options.Events,
	}
	s.runner = persona.New(s.store, s.catalogue, s.client)
	s.runner.SetFollower(options.Follower)
	s.search = search.New(s.store)
	s.runner.SetSearch(s.search)
	s.runner.SetTools(s.tools)
	s.runner.SetConnectionWaits(s.activeMCPWaits)
	s.runner.OnEvent(s.publishEvent)
	s.routes()
	if !options.Follower {
		s.Lead()
	}
	return s
}

// Lead starts the persona work of the workspace on this instance: the
// reaction queue, the daily and schedule checks, search indexing. It is
// called once, when this instance comes to own the workspace; the server
// stops leading only by being closed.
func (s *Server) Lead() {
	s.leadOnce.Do(s.lead)
}

func (s *Server) lead() {
	s.leading.Store(true)
	s.runner.SetFollower(false)
	s.startIndexing()
	if s.bus != nil {
		// Posts saved on other instances are indexed here, where the
		// indexing runs.
		s.feedJoin()
	}
	if watcher, ok := s.store.(interface {
		WatchReactions(context.Context, func())
	}); ok {
		ctx, cancel := context.WithCancel(context.Background())
		s.watchStop = cancel
		watcher.WatchReactions(ctx, s.runner.Poke)
	}
	s.runner.Start()
	s.dailyStop = make(chan struct{})
	s.dailyDone = make(chan struct{})
	go func() {
		defer close(s.dailyDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		s.runner.CheckDaily()
		s.runner.CheckSchedules()
		for {
			select {
			case <-s.dailyStop:
				return
			case <-ticker.C:
				s.runner.CheckDaily()
				s.runner.CheckSchedules()
			}
		}
	}()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Close lets whatever is reacting finish. Reactions write as they go, so
// exiting in the middle of one leaves a persona's notes half written.
func (s *Server) Close() { s.close(false) }

// Halt finishes the reaction in hand and stops, leaving the rest of the queue
// kept for the next process or instance to take up.
func (s *Server) Halt() { s.close(true) }

func (s *Server) close(halt bool) {
	s.closeOnce.Do(func() {
		s.leadOnce.Do(func() {}) // A follower never leads after closing.
		if s.leading.Load() {
			close(s.dailyStop)
			<-s.dailyDone
			if s.watchStop != nil {
				s.watchStop()
			}
			s.indexCancel()
		}
		if halt {
			s.runner.Halt()
		} else {
			s.runner.Stop()
		}
		if s.leading.Load() {
			<-s.indexDone
		}
		s.feedMu.Lock()
		if s.feedStop != nil {
			s.feedStop()
			s.feedStop, s.feedRefs = nil, 0
		}
		s.feedMu.Unlock()
	})
}

// feedJoin starts feeding the hub from the event bus for one more listener;
// feedLeave ends that once the last one is gone.
func (s *Server) feedJoin() {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	s.feedRefs++
	if s.feedStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.feedStop = cancel
	s.bus.Subscribe(ctx, func(name string, data []byte) {
		s.events.send(name, json.RawMessage(data))
		if name == "entry" {
			s.scheduleIndex()
		}
	})
}

func (s *Server) feedLeave() {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	if s.feedRefs--; s.feedRefs <= 0 && s.feedStop != nil {
		s.feedStop()
		s.feedStop, s.feedRefs = nil, 0
	}
}

// Idle reports whether nothing on this server needs it kept open: no page is
// listening, no backup is being written and it does not lead the workspace.
func (s *Server) Idle() bool {
	s.feedMu.Lock()
	listening := s.feedRefs > 0
	s.feedMu.Unlock()
	s.events.mu.Lock()
	watching := len(s.events.watchers) > 0
	s.events.mu.Unlock()
	return !listening && !watching && !s.exporting.Load() && !s.leading.Load()
}

func (s *Server) client() (*llm.Client, store.Settings) {
	settings := s.store.Settings()
	if settings.OpenRouterMode && strings.TrimSpace(settings.OpenRouterAPIKey) != "" {
		settings.RouterModel = settings.OpenRouterRouterModel
		settings.HighModel = settings.OpenRouterHighModel
		settings.MiddleModel = settings.OpenRouterMiddleModel
		settings.LowModel = settings.OpenRouterLowModel
		settings.VisionModel = store.OpenRouterVision
		return llm.NewOpenRouter(store.OpenRouterBaseURL, settings.OpenRouterAPIKey), settings
	}
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
	if s.bus != nil {
		s.feedJoin()
		defer s.feedLeave()
	}

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
