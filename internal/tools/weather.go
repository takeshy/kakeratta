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
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

var weatherForecast = Tool{
	Name: "weather_forecast", ReadOnly: true,
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string", "description": "地域名（例: Tokyo, Japan）または緯度,経度。省略すると投稿の位置を使う"},
		"days":  map[string]any{"type": "integer", "minimum": 1, "maximum": 14, "description": "今日から何日分か。既定3。実際に取れる期間はWeatherAPIの契約による"},
		"date":  map[string]any{"type": "string", "description": "特定日の予報を調べる場合は YYYY-MM-DD。予報可能な期間内のみ"},
		"hour":  map[string]any{"type": "integer", "minimum": 0, "maximum": 23, "description": "地域の現地時刻で何時の予報か。指定時だけ、その時間帯の予報も返す"},
		"lang":  map[string]any{"type": "string", "enum": []string{"ja", "en"}, "description": "天気の説明の言語。既定ja"},
	}},
	Usage: `weather_forecast — WeatherAPIで現在の天気と日別予報を調べる。queryは地域名または緯度,経度。省略時は投稿の位置が必要。サーバーの位置を現在地とみなさない。daysは1〜14（既定3）、dateはYYYY-MM-DD、hourは現地時刻の0〜23。hour指定時だけ時間帯の予報も返す。langはjaまたはen（既定ja）。実際に取得できた地域・日付・更新時刻を確認し、取得していない日時の予報を補わない。契約によって取得できる日数が少ない場合がある。予報と現在の観測値を区別し、出典はWeatherAPI.comを示す。`,
	run: func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error) {
		key := strings.TrimSpace(r.config.Weather.APIKey)
		if key == "" {
			return Result{}, errors.New("WeatherAPI のAPIキーが設定されていません")
		}
		query := args.str("query")
		if query == "" {
			if at == nil {
				return Result{}, errors.New("天気を調べる地域名か投稿の位置情報が必要です")
			}
			if math.IsNaN(at.Lat) || math.IsNaN(at.Lon) || math.IsInf(at.Lat, 0) || math.IsInf(at.Lon, 0) || math.Abs(at.Lat) > 90 || math.Abs(at.Lon) > 180 {
				return Result{}, errors.New("投稿の位置情報が正しくありません")
			}
			query = fmt.Sprintf("%f,%f", at.Lat, at.Lon)
		}
		if strings.EqualFold(query, "auto:ip") {
			return Result{}, errors.New("auto:ip はユーザーの現在地ではありません。地域名を指定してください")
		}
		days, err := weatherInteger(args, "days", 3, 1, 14)
		if err != nil {
			return Result{}, err
		}
		date := args.str("date")
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return Result{}, errors.New("date は YYYY-MM-DD で指定してください")
			}
		}
		hour, err := weatherInteger(args, "hour", -1, 0, 23)
		if err != nil {
			return Result{}, err
		}
		lang := args.str("lang")
		if lang == "" {
			lang = "ja"
		}
		if lang != "ja" && lang != "en" {
			return Result{}, errors.New("lang は ja または en を指定してください")
		}
		endpoint := strings.TrimRight(r.config.Weather.URL, "/")
		if endpoint == "" {
			endpoint = "https://api.weatherapi.com/v1"
		}
		address, err := url.Parse(endpoint + "/forecast.json")
		if err != nil || address.Host == "" || (address.Scheme != "https" && address.Scheme != "http") || address.User != nil {
			return Result{}, errors.New("WeatherAPI の接続先が正しくありません")
		}
		params := address.Query()
		params.Set("key", key)
		params.Set("q", query)
		params.Set("days", strconv.Itoa(days))
		params.Set("lang", lang)
		params.Set("aqi", "no")
		params.Set("alerts", "no")
		if date != "" {
			params.Set("dt", date)
		}
		if hour >= 0 {
			params.Set("hour", strconv.Itoa(hour))
		}
		address.RawQuery = params.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
		if err != nil {
			return Result{}, errors.New("WeatherAPI のリクエストを作成できません")
		}
		request.Header.Set("User-Agent", userAgent)
		// The API key is in the query: never follow a redirect or expose a URL
		// from an HTTP error to model prompts or logs.
		client := *httpClient
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return Result{}, errors.New("WeatherAPI に接続できませんでした")
		}
		defer response.Body.Close()
		var data weatherResponse
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&data)
		if response.StatusCode != http.StatusOK || data.Error != nil {
			code := 0
			if data.Error != nil {
				code = data.Error.Code
			}
			return Result{}, weatherAPIError(response.StatusCode, code)
		}
		if decodeErr != nil || data.Location.Name == "" || len(data.Forecast.Days) == 0 {
			return Result{}, errors.New("WeatherAPI の予報データを読み取れませんでした")
		}
		if date != "" {
			filtered := data.Forecast.Days[:0]
			for _, day := range data.Forecast.Days {
				if day.Date == date {
					filtered = append(filtered, day)
				}
			}
			data.Forecast.Days = filtered
			if len(filtered) == 0 {
				return Result{}, errors.New("WeatherAPI から指定した日付の予報を取得できませんでした")
			}
		}
		for i := range data.Forecast.Days {
			day := &data.Forecast.Days[i]
			if day.Date == "" || day.Day.MaxTemp == nil || day.Day.MinTemp == nil {
				return Result{}, errors.New("WeatherAPI の予報データが不完全です")
			}
			if hour < 0 {
				day.Hours = nil
			} else {
				filtered := day.Hours[:0]
				for _, h := range day.Hours {
					if when, err := time.Parse("2006-01-02 15:04", h.Time); err == nil && when.Hour() == hour {
						filtered = append(filtered, h)
					}
				}
				day.Hours = filtered
			}
		}
		warning := ""
		if date == "" && len(data.Forecast.Days) < days {
			warning = fmt.Sprintf("%d日分を指定しましたが取得できたのは%d日分です。未取得の日付の予報は分かりません。", days, len(data.Forecast.Days))
		}
		result := struct {
			Provider string `json:"provider"`
			*weatherResponse
			Warning string `json:"warning,omitempty"`
		}{"WeatherAPI.com", &data, warning}
		raw, err := json.Marshal(result)
		if err != nil {
			return Result{}, errors.New("WeatherAPI の結果を整形できませんでした")
		}
		return Result{Text: string(raw), Sources: []timeline.Source{{Label: "WeatherAPI.com — " + data.Location.Name, URL: "https://www.weatherapi.com/"}}}, nil
	},
}

func weatherInteger(args Args, name string, fallback, low, high int) (int, error) {
	if _, exists := args[name]; !exists {
		return fallback, nil
	}
	var value float64
	switch number := args[name].(type) {
	case int:
		value = float64(number)
	case float64:
		value = number
	default:
		return 0, fmt.Errorf("%s は%d〜%dの整数を指定してください", name, low, high)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < float64(low) || value > float64(high) || math.Trunc(value) != value {
		return 0, fmt.Errorf("%s は%d〜%dの整数を指定してください", name, low, high)
	}
	return int(value), nil
}

func weatherAPIError(status, code int) error {
	message := "予報を取得できませんでした"
	switch code {
	case 1006:
		message = "指定した地域が見つかりませんでした"
	case 1002, 2006, 2008:
		message = "APIキーが未設定・無効・停止されています"
	case 2007:
		message = "APIの利用上限に達しています"
	case 2009:
		message = "現在の契約では指定した予報を取得できません"
	}
	return fmt.Errorf("WeatherAPI: %s（HTTP %d、code %d）", message, status, code)
}

type weatherCondition struct {
	Text string `json:"text"`
	Code int    `json:"code"`
}

// Keep only weather fields useful to an answer, omitting icons and unrequested hours.
type weatherReading struct {
	Time      string           `json:"time,omitempty"`
	Updated   string           `json:"last_updated,omitempty"`
	Temp      *float64         `json:"temp_c,omitempty"`
	FeelsLike *float64         `json:"feelslike_c,omitempty"`
	Wind      *float64         `json:"wind_kph,omitempty"`
	Rain      *float64         `json:"precip_mm,omitempty"`
	Chance    *float64         `json:"chance_of_rain,omitempty"`
	Humidity  *float64         `json:"humidity,omitempty"`
	UV        *float64         `json:"uv,omitempty"`
	Condition weatherCondition `json:"condition"`
}

type weatherResponse struct {
	Location struct {
		Name      string  `json:"name"`
		Region    string  `json:"region"`
		Country   string  `json:"country"`
		Lat       float64 `json:"lat"`
		Lon       float64 `json:"lon"`
		Zone      string  `json:"tz_id"`
		LocalTime string  `json:"localtime"`
	} `json:"location"`
	Current  weatherReading `json:"current"`
	Forecast struct {
		Days []struct {
			Date string `json:"date"`
			Day  struct {
				MaxTemp   *float64         `json:"maxtemp_c"`
				MinTemp   *float64         `json:"mintemp_c"`
				Rain      *float64         `json:"totalprecip_mm,omitempty"`
				Chance    *float64         `json:"daily_chance_of_rain,omitempty"`
				Wind      *float64         `json:"maxwind_kph,omitempty"`
				UV        *float64         `json:"uv,omitempty"`
				Condition weatherCondition `json:"condition"`
			} `json:"day"`
			Hours []weatherReading `json:"hour,omitempty"`
		} `json:"forecastday"`
	} `json:"forecast"`
	Error *struct {
		Code int `json:"code,omitempty"`
	} `json:"error,omitempty"`
}
