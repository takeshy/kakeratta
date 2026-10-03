package server

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// resolvePlace enriches the submitted coordinates without making a map outage
// prevent a post. The request hash remains that of the original submission.
func (s *Server) resolvePlace(ctx context.Context, place *timeline.Place) *timeline.Place {
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
		result, err := s.tools.MCP.Call(ctx, tool.QualifiedName(), map[string]any{"lat": out.Lat, "lng": out.Lon})
		if err != nil || result.IsError {
			return out
		}
		var data struct {
			Found bool `json:"found"`
			Place struct {
				Name        string `json:"name"`
				CountryCode string `json:"countryCode"`
				GeonameID   int64  `json:"geonameId"`
			} `json:"place"`
		}
		if json.Unmarshal(result.StructuredContent, &data) != nil || !data.Found {
			return out
		}
		name := strings.TrimSpace(data.Place.Name)
		if name == "" {
			return out
		}
		out.Label = name
		if country := strings.TrimSpace(data.Place.CountryCode); country != "" {
			out.Label += "（" + country + "）"
		}
		out.Approximate = true
		out.GeonameID = data.Place.GeonameID
		return out
	}
	return out
}
