package server

import (
	"encoding/json"
	"log"
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

// scheduleIndex queues bringing the search index up to date. It runs as a
// reaction of its own, so one indexer works a workspace at a time, in
// whichever process claims it.
func (s *Server) scheduleIndex() {
	s.runner.ScheduleIndex()
}
