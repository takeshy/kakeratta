package persona

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// The scheduled preparation turn can combine existing belongings with a fresh
// forecast. A failed forecast still permits the grounded belongings check.
func TestSonaScheduledPreparationReadsFreshWeather(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("weather unavailable=%t", unavailable), func(t *testing.T) {
			r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
			settings := s.Settings()
			settings.TimeZone = "Asia/Tokyo"
			if err := s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			date := now.In(settings.Location()).Format("2006-01-02")
			requests := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				if req.URL.Path != "/forecast.json" || req.URL.Query().Get("q") != "Tokyo, Japan" || req.URL.Query().Get("dt") != date || req.URL.Query().Get("hour") != "18" {
					t.Error("forecast did not use the outing's region, day and hour")
				}
				if unavailable {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprintf(w, `{"location":{"name":"Tokyo","tz_id":"Asia/Tokyo"},"current":{"condition":{"text":"晴れ"}},"forecast":{"forecastday":[{"date":%q,"day":{"maxtemp_c":25,"mintemp_c":18,"daily_chance_of_rain":80},"hour":[{"time":%q,"chance_of_rain":80,"condition":{"text":"雨"}}]}]}}`, date, date+" 18:00")
			}))
			defer backend.Close()
			r.tools.Weather = tools.WeatherAPIConfig{APIKey: "test-weather", URL: backend.URL}
			if err := s.WriteNote("routine", "Routine.md", "いつもの持ち物は鍵と財布。鍵を忘れたことがある。外出する地域は東京。"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: now.Add(-time.Second).UTC(), Note: "出発前の準備。東京へ外出、18時ごろ帰る。鍵と財布を点検し、帰りの雨を調べる。"}); err != nil {
				t.Fatal(err)
			}
			read := false
			f.phaseReply = func(system, user string, offered map[string]bool) string {
				switch {
				case offered["finish_read"]:
					if !strings.Contains(system, "出発前の準備") || !strings.Contains(system, "この起床時に確認し直して") || !strings.Contains(user, "鍵を忘れたことがある") {
						t.Error("preparation wake lost its purpose or belongings memory")
					}
					if read {
						return "read_done"
					}
					read = true
					if !strings.Contains(system, "weather_forecast:") {
						t.Error("sona cannot research the forecast")
					}
					return fmt.Sprintf(`read:{"steps":[{"type":"tool","tool":"weather_forecast","args":"{\"query\":\"Tokyo, Japan\",\"date\":\"%s\",\"hour\":18}"}]}`, date)
				case offered["submit_answer"]:
					if unavailable {
						if !strings.Contains(user, "WeatherAPI") {
							t.Error("forecast failure did not reach the answer")
						}
						return `{"text":"鍵と財布、大丈夫？","sources":[]}`
					}
					if !strings.Contains(user, `"chance_of_rain":80`) || !strings.Contains(user, "晴れ") {
						t.Error("the answer did not receive current sunshine and later rain")
					}
					return `{"text":"鍵と財布、大丈夫？ 今は晴れているけど、帰りは雨予報だから傘もあるとよさそう。","sources":[]}`
				}
				return `{"operations":[],"consistent":true,"reason":"追加の約束なし"}`
			}
			reminders := 0
			r.OnEvent(func(name string, _ any) {
				if name == "reminder" {
					reminders++
				}
			})
			r.schedulesAt(context.Background(), now)
			r.schedulesAt(context.Background(), now.Add(time.Minute))
			if requests != 1 || reminders != 1 {
				t.Fatalf("forecast requests=%d reminders=%d, want one each", requests, reminders)
			}
			said, err := s.Timeline()
			if err != nil {
				t.Fatal(err)
			}
			if len(said) != 1 || said[0].Speaker != "routine" || !strings.Contains(said[0].Text, "鍵と財布") || (strings.Contains(said[0].Text, "傘") == unavailable) {
				t.Fatalf("preparation answer: %+v", said)
			}
		})
	}
}

// A sharing check-in can pick up an outing addressed to another persona and
// prepare a future wake without publishing a premature reminder.
func TestSonaCheckInSchedulesPreparationWithoutSpeakingEarly(t *testing.T) {
	r, s, f := setup(t, func(string, string) string { return `{"action":"silent"}` }, "routine")
	now := store.CheckInAt(s.Root(), "routine", checkInDay(s, time.Now().UTC().AddDate(0, 0, 1), "routine"))
	// This test covers sona's check-in, independently of monthly summaries.
	if now.Day() == store.SummaryDay(s.Root()) {
		now = store.CheckInAt(s.Root(), "routine", checkInDay(s, now.AddDate(0, 0, 1), "routine"))
	}
	departure := now.Add(75 * time.Minute)
	outing := timeline.Entry{ID: "outing", Kind: timeline.KindPost, Speaker: timeline.User, Root: "outing", At: now.Add(-time.Hour), Text: fmt.Sprintf("@michi %s に東京へ出かける。鍵を忘れたことがある。", departure.Format("2006-01-02 15:04"))}
	if err := s.Append(outing); err != nil {
		t.Fatal(err)
	}
	wakeAt := now.Add(time.Hour)
	read := false
	f.phaseReply = func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			if !strings.Contains(system, "これからの予定や準備が必要な場面") || !strings.Contains(system, "今は黙ります") {
				t.Error("check-in did not receive prospective preparation instructions")
			}
			if read {
				return "read_done"
			}
			read = true
			return fmt.Sprintf(`read:{"steps":[{"type":"tool","tool":"timeline_search","args":"{\"from\":\"%s\",\"to\":\"%s\"}"}]}`, now.Add(-24*time.Hour).Format("2006-01-02 15:04"), now.Format("2006-01-02 15:04"))
		case offered["submit_answer"]:
			if !strings.Contains(user, outing.Text) {
				t.Error("check-in did not see the outing addressed to michi")
			}
			return `{"text":"","sources":[]}`
		case offered["submit_schedule"]:
			return fmt.Sprintf(`{"operations":[{"type":"create","at":%q,"daily":false,"note":"出発前の準備。東京への外出で鍵を点検する。","id":""}],"consistent":true,"reason":"まだ声をかけず、出発前に起きる"}`, wakeAt.Format(time.RFC3339))
		}
		return `{"operations":[],"consistent":true,"reason":"追加なし"}`
	}
	r.dailyAt(context.Background(), now)
	schedules, err := s.Schedules("routine")
	if err != nil || len(schedules) != 1 || !schedules[0].At.Equal(wakeAt) || !strings.Contains(schedules[0].Note, "出発前") {
		t.Fatalf("preparation schedules=%+v err=%v", schedules, err)
	}
	entries, err := s.Timeline()
	if err != nil || len(entries) != 1 || entries[0].ID != outing.ID {
		t.Fatalf("check-in spoke before preparation was due: %+v, %v", entries, err)
	}
}
