package server

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

func (s *Server) publishEvent(name string, data any) {
	if name == "entry" {
		s.scheduleIndex()
	}
	if s.bus == nil {
		s.events.send(name, data)
		return
	}
	// Every instance with a page open on the workspace hears it, this one
	// through the bus like the rest.
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("event %s: %v", name, err)
		return
	}
	if err := s.bus.Publish(name, raw); err != nil {
		log.Printf("event %s: %v", name, err)
	}
}
func (s *Server) scheduleIndex() {
	select {
	case s.indexWake <- struct{}{}:
	default:
	}
}
func (s *Server) startIndexing() {
	ctx, cancel := context.WithCancel(context.Background())
	s.indexCancel = cancel
	s.indexWake = make(chan struct{}, 1)
	s.indexDone = make(chan struct{})
	go func() {
		defer close(s.indexDone)
		retry := time.NewTimer(time.Hour)
		defer retry.Stop()
		retry.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.indexWake:
			case <-retry.C:
			}
			// Coalesce adjacent saves and fast image updates, without delaying the post.
			debounce := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				debounce.Stop()
				return
			case <-debounce.C:
			}
			select {
			case <-s.indexWake:
			default:
			}
			work, finish := context.WithTimeout(ctx, 5*time.Minute)
			err := s.search.IndexPending(work)
			finish()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("search indexing: %v", err)
				retry.Reset(30 * time.Second)
			} else {
				retry.Stop()
			}
		}
	}()
	s.scheduleIndex()
}
