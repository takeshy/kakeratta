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
	"log"
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
	"github.com/takeshy/kakeratta/internal/usage"
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
	// Alarm wakes the workspace at a time: it makes sure a due reaction is
	// queued then. Without one the server sets a timer in its own process.
	Alarm func(at time.Time) error
	// Passive assembles API/worker dependencies without starting background work.
	Passive bool
}

// EventBus carries one workspace's events to every process serving it. A
// workspace that implements it - Firestore - has its events go through it;
// one that does not keeps them in this process.
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

	mux       *http.ServeMux
	alarm     *localAlarm
	closeOnce sync.Once
	events    *hub

	// bus, feed: with an event bus, the hub is fed from it while a page is
	// listening.
	bus      EventBus
	feedMu   sync.Mutex
	feedRefs int
	feedStop context.CancelFunc
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
	}
	if bus, ok := options.Store.(EventBus); ok {
		s.bus = bus
	}
	s.tools.ObserveWebSearch = func(ctx context.Context, failed bool) {
		if err := usage.RecordSearch(s.store, llm.TraceFrom(ctx), failed); err != nil {
			log.Print("usage search persistence failed")
		}
	}
	s.runner = persona.New(s.store, s.catalogue, s.client)
	if options.Passive {
		s.runner.SetQueuedOnly()
	}
	s.search = search.New(s.store)
	s.runner.SetSearch(s.search)
	s.runner.SetTools(s.tools)
	s.runner.SetPlaceLookup(s.enrichPlace)
	s.runner.SetConnectionWaits(s.activeMCPWaits)
	s.runner.OnEvent(s.publishEvent)
	s.routes()
	if options.Alarm != nil {
		s.runner.SetAlarm(options.Alarm)
	} else {
		s.alarm = newLocalAlarm(func() {
			if err := s.runner.Due(); err != nil {
				// The wake was not kept: try again in a minute.
				_ = s.alarm.Set(time.Now().Add(time.Minute))
			}
		})
		s.runner.SetAlarm(s.alarm.Set)
	}
	// Whatever was queued and not finished - here before a restart, or by
	// another process - is taken up now, and the workspace looks at its times.
	if !options.Passive {
		s.runner.Start()
		s.scheduleIndex()
		s.runner.Rearm()
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Close works everything queued that can be taken up now, then stops. Tests
// use it to wait for the reactions they caused.
func (s *Server) Close() { s.close(false) }

// Halt finishes the reactions in hand and stops. Reactions write as they go,
// so exiting in the middle of one leaves a persona's notes half written; the
// rest of the queue stays kept for the next process to take up.
func (s *Server) Halt() { s.close(true) }

func (s *Server) close(halt bool) {
	s.closeOnce.Do(func() {
		if s.alarm != nil {
			s.alarm.Stop()
		}
		if halt {
			s.runner.Halt()
		} else {
			s.runner.Stop()
		}
		s.feedMu.Lock()
		if s.feedStop != nil {
			s.feedStop()
			s.feedStop, s.feedRefs = nil, 0
		}
		s.feedMu.Unlock()
	})
}

// feedJoin starts feeding the hub from the event bus for one more page;
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
// listening, no backup is being written, and no reaction is in hand.
func (s *Server) Idle() bool {
	s.events.mu.Lock()
	watching := len(s.events.watchers) > 0
	s.events.mu.Unlock()
	return !watching && !s.exporting.Load() && s.runner.Idle()
}

func (s *Server) client() (*llm.Client, store.Settings) {
	settings := s.store.Settings()
	if settings.OpenRouterMode && strings.TrimSpace(settings.OpenRouterAPIKey) != "" {
		settings.RouterModel = settings.OpenRouterRouterModel
		settings.HighModel = settings.OpenRouterHighModel
		settings.MiddleModel = settings.OpenRouterMiddleModel
		settings.LowModel = settings.OpenRouterLowModel
		settings.VisionModel = store.OpenRouterVision
		client := llm.NewOpenRouter(store.OpenRouterBaseURL, settings.OpenRouterAPIKey)
		client.Observe = s.observeUsage
		return client, settings
	}
	client := llm.New(settings.BaseURL, settings.APIKey)
	client.Observe = s.observeUsage
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

func (s *Server) observeUsage(record llm.CallRecord) {
	record.Log()
	if err := usage.Record(s.store, record); err != nil {
		log.Print("usage model persistence failed")
	}
}
