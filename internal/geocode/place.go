// Package geocode interprets the geo MCP reverse_geocode response.
package geocode

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// Language defaults older posts without a saved display language to Japanese.
func Language(value string) string {
	if value == "en" {
		return "en"
	}
	return "ja"
}

// Resolve builds an approximate label from the settlement's administrative
// hierarchy. These names describe the nearest settlement, not boundary containment.
func Resolve(raw json.RawMessage, before timeline.Place, language string) (timeline.Place, bool, error) {
	var result struct {
		Found *bool `json:"found"`
		Place struct {
			Name                string `json:"name"`
			GeonameID           int64  `json:"geonameId"`
			AdministrativeAreas []struct {
				Level int    `json:"level"`
				Name  string `json:"name"`
			} `json:"administrativeAreas"`
		} `json:"place"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return before, false, err
	}
	if result.Found == nil {
		return before, false, errors.New("missing found in reverse_geocode response")
	}
	if !*result.Found {
		return before, false, nil
	}
	name := strings.TrimSpace(result.Place.Name)
	if name == "" {
		return before, false, errors.New("empty place name")
	}
	areas := result.Place.AdministrativeAreas
	sort.SliceStable(areas, func(i, j int) bool { return areas[i].Level < areas[j].Level })
	separator := ""
	if Language(language) == "en" {
		separator = ", "
	}
	label := ""
	appendName := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if label == "" || strings.HasPrefix(value, label) {
			label = value
			return
		}
		if label == value || strings.HasSuffix(label, separator+value) {
			return
		}
		label += separator + value
	}
	for _, area := range areas {
		if area.Level >= 2 && area.Level <= 4 {
			appendName(area.Name)
		}
	}
	appendName(name)
	after := before
	after.Label, after.Approximate, after.GeonameID = label, true, result.Place.GeonameID
	return after, true, nil
}
