package server

import (
	"context"
	"math"
	"time"

	"github.com/takeshy/kakeratta/internal/geocode"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// resolvePlace enriches the submitted coordinates without making a map outage
// prevent a post. The request hash remains that of the original submission.
func (s *Server) resolvePlace(ctx context.Context, place *timeline.Place, language string) *timeline.Place {
	if place == nil {
		return nil
	}
	if math.IsNaN(place.Lat) || math.IsInf(place.Lat, 0) || math.Abs(place.Lat) > 90 || math.IsNaN(place.Lon) || math.IsInf(place.Lon, 0) || math.Abs(place.Lon) > 180 {
		return nil
	}
	out := &timeline.Place{Lat: place.Lat, Lon: place.Lon, Label: place.Label}
	if out.Label != "" || s.tools.MCP == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, tool := range s.tools.MCP.ToolsContext(ctx) {
		if tool.Name != "reverse_geocode" {
			continue
		}
		result, err := s.tools.MCP.Call(ctx, tool.QualifiedName(), map[string]any{"lat": out.Lat, "lng": out.Lon, "language": geocode.Language(language)})
		if err != nil || result.IsError {
			return out
		}
		resolved, found, err := geocode.Resolve(result.StructuredContent, *out, language)
		if err != nil || !found {
			return out
		}
		return &resolved
	}
	return out
}
