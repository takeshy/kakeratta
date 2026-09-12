package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/dokking/internal/timeline"
)

var errNoQuery = errors.New("検索語がありません")

// The public OpenStreetMap services ask that a client identify itself.
const userAgent = "dokking/capture-timeline (https://github.com/takeshy/dokking)"

var httpClient = &http.Client{Timeout: 25 * time.Second}

var placeSearch = Tool{
	Name: "place_search",
	Usage: `place_search — いまいる場所の周りを探す。営業時間も返る。` +
		`{"tool":"place_search","args":{"query":"ラーメン","radius":1200}}`,
	run: func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error) {
		query := args.str("query")
		if query == "" {
			return Result{}, errNoQuery
		}
		if at == nil {
			// Without a position this is a plain lookup, and saying so is a
			// better answer than pretending to know where the user is.
			return geocode(ctx, r, query)
		}
		radius := args.num("radius", 1200)
		if radius <= 0 || radius > 5000 {
			radius = 1200
		}
		return around(ctx, r, query, *at, radius)
	},
}

type overpassElement struct {
	Type   string                      `json:"type"`
	ID     int64                       `json:"id"`
	Lat    float64                     `json:"lat"`
	Lon    float64                     `json:"lon"`
	Center *struct{ Lat, Lon float64 } `json:"center"`
	Tags   map[string]string           `json:"tags"`
}

func around(ctx context.Context, r *Registry, query string, at timeline.Place, radius float64) (Result, error) {
	endpoint := strings.TrimSpace(r.settings.Places.OverpassURL)
	if endpoint == "" {
		return Result{}, errors.New("周辺検索の接続先が設定されていません")
	}
	// Name, cuisine and amenity all get the same words: a query is whatever the
	// persona thought to ask for, not a tag the user has to know.
	escaped := strings.ReplaceAll(query, `"`, "")
	ql := fmt.Sprintf(`[out:json][timeout:20];
(
  nwr(around:%.0f,%f,%f)["name"~"%s",i];
  nwr(around:%.0f,%f,%f)["cuisine"~"%s",i];
  nwr(around:%.0f,%f,%f)["amenity"~"%s",i];
);
out center 30;`,
		radius, at.Lat, at.Lon, escaped,
		radius, at.Lat, at.Lon, escaped,
		radius, at.Lat, at.Lon, escaped)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("data="+url.QueryEscape(ql)))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)
	response, err := httpClient.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("周辺検索が失敗しました (%d)", response.StatusCode)
	}
	var payload struct {
		Elements []overpassElement `json:"elements"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return Result{}, err
	}

	type hit struct {
		line     string
		distance float64
	}
	var hits []hit
	seen := map[string]bool{}
	for _, element := range payload.Elements {
		name := element.Tags["name"]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		lat, lon := element.Lat, element.Lon
		if element.Center != nil {
			lat, lon = element.Center.Lat, element.Center.Lon
		}
		distance := metres(at.Lat, at.Lon, lat, lon)
		parts := []string{fmt.Sprintf("- %s（約%.0fm）", name, distance)}
		if kind := describeKind(element.Tags); kind != "" {
			parts = append(parts, "  "+kind)
		}
		if hours := element.Tags["opening_hours"]; hours != "" {
			parts = append(parts, "  営業時間: "+hours)
		}
		if address := describeAddress(element.Tags); address != "" {
			parts = append(parts, "  "+address)
		}
		hits = append(hits, hit{line: strings.Join(parts, "\n"), distance: distance})
	}
	if len(hits) == 0 {
		return Result{Text: fmt.Sprintf("%.0fm以内に「%s」らしきものは見つかりませんでした。", radius, query)}, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].distance < hits[j].distance })
	if len(hits) > 12 {
		hits = hits[:12]
	}
	var lines []string
	for _, found := range hits {
		lines = append(lines, found.line)
	}
	return Result{
		Text: strings.Join(lines, "\n"),
		Sources: []timeline.Source{{
			Label: "OpenStreetMap",
			URL:   fmt.Sprintf("https://www.openstreetmap.org/#map=17/%f/%f", at.Lat, at.Lon),
		}},
	}, nil
}

func describeKind(tags map[string]string) string {
	var parts []string
	for _, key := range []string{"amenity", "shop", "cuisine"} {
		if value := tags[key]; value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " / ")
}

func describeAddress(tags map[string]string) string {
	var parts []string
	for _, key := range []string{"addr:city", "addr:suburb", "addr:neighbourhood", "addr:block_number", "addr:housenumber"} {
		if value := tags[key]; value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "")
}

type geocoded struct {
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
}

// geocode turns a name into a place, and is also the answer to a place_search
// made from somewhere with no position.
func geocode(ctx context.Context, r *Registry, query string) (Result, error) {
	found, err := lookup(ctx, r, query)
	if err != nil {
		return Result{}, err
	}
	if len(found) == 0 {
		return Result{Text: "見つかりませんでした。"}, nil
	}
	var lines []string
	for _, place := range found {
		lines = append(lines, "- "+place.DisplayName)
	}
	return Result{
		Text:    "位置が分からないので、名前で引いた結果です。\n" + strings.Join(lines, "\n"),
		Sources: []timeline.Source{{Label: "Nominatim (OpenStreetMap)", URL: "https://nominatim.openstreetmap.org/"}},
	}, nil
}

func lookup(ctx context.Context, r *Registry, query string) ([]geocoded, error) {
	base := strings.TrimRight(strings.TrimSpace(r.settings.Places.NominatimURL), "/")
	if base == "" {
		return nil, errors.New("地名検索の接続先が設定されていません")
	}
	target := fmt.Sprintf("%s/search?q=%s&format=json&limit=5&accept-language=ja", base, url.QueryEscape(query))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("地名検索が失敗しました (%d)", response.StatusCode)
	}
	var found []geocoded
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&found); err != nil {
		return nil, err
	}
	return found, nil
}

// metres is the great-circle distance, which is close enough at city scale.
func metres(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000
	toRad := func(degrees float64) float64 { return degrees * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earth * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
