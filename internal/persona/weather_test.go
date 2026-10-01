package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/tools"
)

func TestExternalAgentCanReadWeatherWithoutReceivingTheAPIKey(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "private-weather-key" {
			t.Error("provider did not receive configured key")
		}
		fmt.Fprint(w, `{"location":{"name":"Tokyo","tz_id":"Asia/Tokyo"},"forecast":{"forecastday":[{"date":"2026-10-01","day":{"maxtemp_c":25,"mintemp_c":18}}]}}`)
	}))
	defer backend.Close()
	runner, workspace, _ := externalSetup(t, time.Minute, time.Minute)
	runner.SetTools(tools.Config{Weather: tools.WeatherAPIConfig{APIKey: "private-weather-key", URL: backend.URL}})
	skill, _ := runner.catalogue.Find(context.Background(), "gourmet")
	skill.Tools = append(skill.Tools, "weather_forecast")
	raw, err := json.Marshal(skill)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.catalogue.Update(context.Background(), skill.ID, raw, nil); err != nil {
		t.Fatal(err)
	}
	entry := post(t, workspace, "東京で食べたい")
	runner.react(entry.ID)
	ask := openAskOf(t, runner)
	_, brief, err := runner.ClaimAsk(ask.ID, "weather-reader")
	if err != nil {
		t.Fatal(err)
	}
	offered := false
	for _, tool := range brief.Tools {
		if tool.Name == "weather_forecast" && tool.Use == "read" {
			offered = true
		}
	}
	raw, _ = json.Marshal(brief)
	if !offered || strings.Contains(string(raw), "private-weather-key") {
		t.Fatal("weather tool or key isolation is incorrect in external context")
	}
	result, err := runner.ReadForAsk(context.Background(), ask.ID, "weather-reader", []ReadStep{{Type: "tool", Tool: "weather_forecast", Args: `{"query":"Tokyo","days":1}`}})
	if err != nil || !strings.Contains(result, "Tokyo") || !strings.Contains(result, "maxtemp_c") || strings.Contains(result, "private-weather-key") {
		t.Fatalf("external weather read failed: %v", err)
	}
}
