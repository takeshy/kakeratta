package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/takeshy/kakeratta/internal/timeline"
)

const weatherFixture = `{"location":{"name":"Tokyo","country":"Japan","tz_id":"Asia/Tokyo","localtime":"2026-10-01 12:00"},"current":{"last_updated":"2026-10-01 12:00","temp_c":25,"condition":{"text":"晴れ","code":1000}},"forecast":{"forecastday":[{"date":"2026-10-02","day":{"maxtemp_c":28,"mintemp_c":20,"daily_chance_of_rain":40,"condition":{"text":"曇り","code":1003}},"hour":[{"time":"2026-10-02 12:00","temp_c":27,"chance_of_rain":10},{"time":"2026-10-02 18:00","temp_c":24,"chance_of_rain":60}]}]}}`

func TestWeatherForecastRequestsAndProjectsWeather(t *testing.T) {
	var query string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		if r.URL.Path != "/v1/forecast.json" || params.Get("key") != "test-weather-key" || params.Get("days") != "3" || params.Get("lang") != "ja" || params.Get("aqi") != "no" {
			t.Error("unexpected WeatherAPI request")
		}
		query = params.Get("q")
		fmt.Fprint(w, weatherFixture)
	}))
	defer backend.Close()
	set := New(Config{Weather: WeatherAPIConfig{APIKey: "test-weather-key", URL: backend.URL + "/v1"}}).For([]string{"weather_forecast"})
	if !set.IsReadOnly("weather_forecast") {
		t.Fatal("weather forecast is not a research tool")
	}
	result, err := set.Run(context.Background(), "weather_forecast", Args{}, &timeline.Place{Lat: 35.68, Lon: 139.76})
	if err != nil {
		t.Fatal(err)
	}
	if query != "35.680000,139.760000" || strings.Contains(result.Text, "test-weather-key") || strings.Contains(result.Text, `"hour"`) {
		t.Fatal("location fallback or projected output is incorrect")
	}
	for _, want := range []string{"Tokyo", "Asia/Tokyo", "2026-10-02", `"maxtemp_c":28`, `"daily_chance_of_rain":40`, "取得できたのは1日分"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("missing %s from forecast", want)
		}
	}
	if len(result.Sources) != 1 || result.Sources[0].URL != "https://www.weatherapi.com/" {
		t.Fatal("missing safe source attribution")
	}
	result, err = set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo, Japan", "date": "2026-10-02", "hour": 18}, nil)
	if err != nil || query != "Tokyo, Japan" || !strings.Contains(result.Text, "18:00") || strings.Contains(result.Text, `"time":"2026-10-02 12:00"`) {
		t.Fatalf("date/hour selection failed: %v", err)
	}
	if _, err := set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo", "date": "2026-10-03"}, nil); err == nil {
		t.Fatal("a missing date was treated as a forecast")
	}
}

func TestWeatherForecastValidatesBeforeCallingProvider(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1) }))
	defer backend.Close()
	set := New(Config{Weather: WeatherAPIConfig{APIKey: "test-key", URL: backend.URL}}).For([]string{"weather_forecast"})
	for _, args := range []Args{{}, {"query": "auto:ip"}, {"query": "Tokyo", "days": 15}, {"query": "Tokyo", "days": 1.5}, {"query": "Tokyo", "days": "3"}, {"query": "Tokyo", "hour": 24}, {"query": "Tokyo", "date": "2026-02-30"}, {"query": "Tokyo", "lang": "invalid"}} {
		if _, err := set.Run(context.Background(), "weather_forecast", args, nil); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	if _, err := set.Run(context.Background(), "weather_forecast", Args{}, &timeline.Place{Lat: 91}); err == nil {
		t.Fatal("invalid coordinates accepted")
	}
	if called.Load() != 0 {
		t.Fatal("invalid arguments reached provider")
	}
	if _, err := New(Config{}).For([]string{"weather_forecast"}).Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := New(Config{}).For(nil).Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil); err == nil {
		t.Fatal("undeclared tool accepted")
	}
}

func TestWeatherForecastProviderFailuresDoNotExposeCredentials(t *testing.T) {
	for _, test := range []struct {
		status, code int
		message      string
	}{
		{400, 1006, "地域が見つかりません"}, {401, 2006, "APIキー"}, {403, 2007, "利用上限"}, {403, 2009, "契約"}, {503, 0, "取得できません"},
	} {
		t.Run(fmt.Sprint(test.code), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprintf(w, `{"error":{"code":%d,"message":"test-secret-in-error"}}`, test.code)
			}))
			defer backend.Close()
			set := New(Config{Weather: WeatherAPIConfig{APIKey: "test-secret-in-error", URL: backend.URL}}).For([]string{"weather_forecast"})
			_, err := set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil)
			if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "test-secret-in-error") {
				t.Fatal("incorrect or unsafe provider error")
			}
		})
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not-json test-secret") }))
	set := New(Config{Weather: WeatherAPIConfig{APIKey: "test-secret", URL: backend.URL}}).For([]string{"weather_forecast"})
	_, err := set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil)
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("unsafe malformed response")
	}
	backend.Close()
	_, err = set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil)
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("transport error exposed URL credentials")
	}
}

func TestWeatherForecastDoesNotFollowRedirects(t *testing.T) {
	var called atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1) }))
	defer target.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer backend.Close()
	set := New(Config{Weather: WeatherAPIConfig{APIKey: "test-key", URL: backend.URL}}).For([]string{"weather_forecast"})
	if _, err := set.Run(context.Background(), "weather_forecast", Args{"query": "Tokyo"}, nil); err == nil || called.Load() != 0 {
		t.Fatal("weather request followed redirect")
	}
}

func TestWeatherAPIConfigUsesFileAndEnvironmentPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weather.key")
	if err := os.WriteFile(path, []byte(" test-file-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KAKERATTA_MCP_SERVERS", "")
	t.Setenv("KAKERATTA_WEATHERAPI_KEY", "")
	t.Setenv("KAKERATTA_WEATHERAPI_KEY_FILE", path)
	t.Setenv("KAKERATTA_WEATHERAPI_URL", "")
	config, err := ConfigFromEnv("test")
	if err != nil || config.Weather.APIKey != "test-file-key" || config.Weather.URL != "https://api.weatherapi.com/v1" {
		t.Fatal("file configuration failed")
	}
	t.Setenv("KAKERATTA_WEATHERAPI_KEY", "test-env-key")
	t.Setenv("KAKERATTA_WEATHERAPI_KEY_FILE", path+".missing")
	config, err = ConfigFromEnv("test")
	if err != nil || config.Weather.APIKey != "test-env-key" {
		t.Fatal("environment key did not take precedence")
	}
	t.Setenv("KAKERATTA_WEATHERAPI_KEY", "")
	if _, err := ConfigFromEnv("test"); err == nil {
		t.Fatal("explicit missing key file accepted")
	}
	t.Setenv("KAKERATTA_WEATHERAPI_KEY_FILE", "")
	t.Chdir(t.TempDir())
	config, err = ConfigFromEnv("test")
	if err != nil || config.Weather.APIKey != "" {
		t.Fatal("absent optional local key file prevented startup")
	}
	if err := os.Mkdir("tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tmp/weatherapi.key", []byte("local-test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = ConfigFromEnv("test")
	if err != nil || config.Weather.APIKey != "local-test-key" {
		t.Fatal("local default key file was not loaded")
	}
}

func TestWeatherForecastLive(t *testing.T) {
	if os.Getenv("KAKERATTA_WEATHERAPI_LIVE_TEST") != "1" {
		t.Skip("opt-in WeatherAPI smoke test")
	}
	config, err := ConfigFromEnv("test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(config).For([]string{"weather_forecast"}).Run(context.Background(), "weather_forecast", Args{"query": "Tokyo, Japan", "days": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got weatherResponse
	if err := json.Unmarshal([]byte(result.Text), &got); err != nil || got.Location.Name == "" || len(got.Forecast.Days) != 1 {
		t.Fatal("live response did not contain one forecast day")
	}
	t.Logf("WeatherAPI returned %s: %s (%s)", got.Location.Name, got.Forecast.Days[0].Date, got.Location.Zone)
}
