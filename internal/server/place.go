package server

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/takeshy/kakeratta/internal/geocode"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func submittedPlace(place *timeline.Place) *timeline.Place {
	if place == nil {
		return nil
	}
	if math.IsNaN(place.Lat) || math.IsInf(place.Lat, 0) || math.Abs(place.Lat) > 90 || math.IsNaN(place.Lon) || math.IsInf(place.Lon, 0) || math.Abs(place.Lon) > 180 {
		return nil
	}
	return &timeline.Place{Lat: place.Lat, Lon: place.Lon, Label: place.Label}
}

// enrichPlace runs before routing, independent of the HTTP request. Failures
// retry in the durable location lane after the catalogue cooldown.
func (s *Server) enrichPlace(ctx context.Context, id string) error {
	entry, exists := s.store.Entry(id)
	if !exists || entry.Place == nil || entry.Place.Label != "" || s.tools.MCP == nil {
		return nil
	}
	before := *entry.Place
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for _, tool := range s.tools.MCP.ToolsContext(ctx) {
		if tool.Name != "reverse_geocode" {
			continue
		}
		result, err := s.tools.MCP.Call(ctx, tool.QualifiedName(), map[string]any{"lat": before.Lat, "lng": before.Lon, "language": geocode.Language(entry.Language)})
		if err != nil {
			return err
		}
		if result.IsError {
			return errors.New("reverse_geocode failed")
		}
		resolved, found, err := geocode.Resolve(result.StructuredContent, before, entry.Language)
		if err != nil || !found {
			return err
		}
		current, changed, err := s.store.UpdatePlace(ctx, id, before, resolved)
		if err == nil && changed {
			s.publishEvent("entry", current)
		}
		return err
	}
	return errors.New("reverse_geocode unavailable")
}
