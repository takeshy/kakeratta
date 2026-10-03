package server

import (
	"bytes"
	"encoding/json"
	"github.com/takeshy/kakeratta/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScheduleEditingAPI(t *testing.T) {
	app, s := app(t)
	saved, err := s.SaveSchedule(store.Schedule{Skill: "routine", At: time.Now().Add(time.Hour), Note: "digest", Daily: true, TimeZone: "Asia/Tokyo", UserRequested: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	for _, tc := range []struct {
		owner, body string
		status      int
	}{
		{"gourmet", `{"notify":false}`, 404},
		{"routine", `{"at":"2000-01-01T00:00:00Z","notify":false}`, 400},
		{"routine", `{"notify":"false"}`, 400},
		{"routine", `{"daily":false}`, 400},
		{"routine", `{}`, 400},
		{"routine", `{"notify":false} {}`, 400},
		{"routine", `{"at":"` + at.Format(time.RFC3339) + `","notify":false}`, 200},
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas/"+tc.owner+"/schedules/"+saved.ID, bytes.NewBufferString(tc.body)))
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, response.Code, response.Body.String())
		}
		if tc.status == 200 {
			var payload struct {
				Personas []json.RawMessage `json:"personas"`
			}
			if json.Unmarshal(response.Body.Bytes(), &payload) != nil || len(payload.Personas) == 0 {
				t.Fatal("missing updated personas")
			}
		}
	}
	schedules, _ := s.Schedules("routine")
	if len(schedules) != 1 || !schedules[0].At.Equal(at) || schedules[0].NotificationsEnabled() || !schedules[0].Daily || !schedules[0].UserRequested {
		t.Fatalf("saved: %+v", schedules)
	}
}
