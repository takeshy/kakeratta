package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

var routeTo = Tool{
	Name: "route",
	Usage: `route — いまいる場所から目的地までの所要時間。` +
		`{"tool":"route","args":{"to":"渋谷駅","mode":"driving|walking|cycling"}}`,
	run: func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error) {
		destination := args.str("to")
		if destination == "" {
			return Result{}, errors.New("目的地がありません")
		}
		if at == nil {
			return Result{}, ErrNoPlace
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

		mode := args.str("mode")
		switch mode {
		case "walking", "foot":
			mode = "foot"
		case "cycling", "bike":
			mode = "bike"
		default:
			mode = "driving"
		}
		base := strings.TrimRight(strings.TrimSpace(r.settings.Places.RouteURL), "/")
		if base == "" {
			return Result{}, errors.New("経路検索の接続先が設定されていません")
		}
		target := fmt.Sprintf("%s/route/v1/%s/%f,%f;%f,%f?overview=false",
			base, mode, at.Lon, at.Lat, lon, lat)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return Result{}, err
		}
		request.Header.Set("User-Agent", userAgent)
		response, err := httpClient.Do(request)
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
		minutes := time.Duration(route.Duration) * time.Second
		// The public routing service knows roads, not timetables. Saying which
		// question was answered is the difference between a useful number and a
		// wrong one.
		label := map[string]string{"driving": "車", "foot": "徒歩", "bike": "自転車"}[mode]
		return Result{
			Text: fmt.Sprintf("%s まで %s で約%d分（%.1fkm）。公共交通は含みません。",
				found[0].DisplayName, label, int(minutes.Minutes()), route.Distance/1000),
			Sources: []timeline.Source{{
				Label: "OSRM",
				URL:   fmt.Sprintf("https://www.openstreetmap.org/directions?from=%f,%f&to=%f,%f", at.Lat, at.Lon, lat, lon),
			}},
		}, nil
	},
}
