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
	"strconv"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
)

var routeTo = Tool{
	Name: "route",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"to":   map[string]any{"type": "string", "description": "行き先の名前や住所"},
		"mode": map[string]any{"type": "string", "description": "car / foot / bike のいずれか", "enum": []any{"car", "foot", "bike"}},
	}, "required": []any{"to"}},
	Usage: `route — 投稿に付いた位置から目的地までの道路の所要時間。位置がなければ使えません。公共交通・リアルタイムの渋滞は非対応。`,
	run: func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error) {
		destination := args.str("to")
		if destination == "" {
			return Result{}, errors.New("目的地がありません")
		}
		if at == nil {
			return Result{}, ErrNoPlace
		}
		mode := args.str("mode")
		base := r.config.Places.Route
		switch mode {
		case "walking", "foot":
			mode, base = "foot", r.config.Places.WalkingRoute
		case "cycling", "bike":
			mode, base = "bike", r.config.Places.CyclingRoute
		case "", "driving", "car":
			mode = "driving"
		default:
			return Result{}, errors.New("対応する移動手段は車・徒歩・自転車だけです。公共交通の所要時間は取得できません")
		}
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			return Result{}, errors.New("指定した移動手段の経路検索の接続先が設定されていません")
		}
		// OSRM profiles belong to the prepared data, not the URL profile token.
		// Refuse the known car-only public endpoint for non-car modes.
		endpoint, err := url.Parse(base)
		if err != nil {
			return Result{}, err
		}
		if mode != "driving" && strings.EqualFold(endpoint.Hostname(), "router.project-osrm.org") {
			return Result{}, errors.New("この接続先は車用です。徒歩・自転車に対応した接続先を設定してください")
		}
		found, err := lookup(ctx, r, destination)
		if err != nil {
			return Result{}, err
		}
		if len(found) == 0 {
			return Result{Text: "目的地が見つかりませんでした。"}, nil
		}
		lat, latErr := strconv.ParseFloat(found[0].Lat, 64)
		lon, lonErr := strconv.ParseFloat(found[0].Lon, 64)
		if latErr != nil || lonErr != nil {
			return Result{}, errors.New("目的地の位置を読めませんでした")
		}

		target := fmt.Sprintf("%s/route/v1/%s/%f,%f;%f,%f?overview=false",
			base, "driving", at.Lon, at.Lat, lon, lat)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return Result{}, err
		}
		request.Header.Set("User-Agent", userAgent)
		response, err := doMapRequest(request)
		if err != nil {
			return Result{}, err
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			return Result{}, fmt.Errorf("経路検索が失敗しました (%d)", response.StatusCode)
		}
		var payload struct {
			Code   string `json:"code"`
			Routes []struct {
				Distance float64 `json:"distance"`
				Duration float64 `json:"duration"`
			} `json:"routes"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
			return Result{}, err
		}
		if payload.Code != "Ok" || len(payload.Routes) == 0 {
			return Result{Text: "経路が見つかりませんでした。"}, nil
		}
		route := payload.Routes[0]
		minutes := int(math.Ceil(route.Duration / 60))
		// The public routing service knows roads, not timetables. Saying which
		// question was answered is the difference between a useful number and a
		// wrong one.
		label := map[string]string{"driving": "車", "foot": "徒歩", "bike": "自転車"}[mode]
		return Result{
			Text: fmt.Sprintf("%s まで %s で約%d分（%.1fkm）。公共交通・リアルタイムの渋滞は含みません。",
				found[0].DisplayName, label, minutes, route.Distance/1000),
			Sources: []timeline.Source{{
				Label: "OSRM",
				URL:   fmt.Sprintf("https://www.openstreetmap.org/directions?engine=fossgis_osrm_%s&from=%f,%f&to=%f,%f", map[string]string{"driving": "car", "foot": "foot", "bike": "bike"}[mode], at.Lat, at.Lon, lat, lon),
			}},
		}, nil
	},
}
