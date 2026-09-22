package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestDailySettingsEndpoint(t *testing.T) {
	app, s := app(t)
	id := app.catalogue.All()[0].ID
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"enabled":true,"time":"08:00","timeZone":"Asia/Tokyo"}`, 200},
		{`{"enabled":true,"time":"24:00","timeZone":"Asia/Tokyo"}`, 400},
		{`{"enabled":true,"time":"08:00","timeZone":"invalid"}`, 400},
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas/"+id+"/daily", strings.NewReader(tc.body)))
		if response.Code != tc.code {
			t.Fatalf("%d: %s", response.Code, response.Body.String())
		}
	}
	got := s.Settings().DailyCheckIns[id]
	stale := httptest.NewRecorder()
	app.ServeHTTP(stale, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"dailyCheckIns":{},"readAloudRate":1.5}`)))
	if stale.Code != 200 || s.Settings().DailyCheckIns[id] != got {
		t.Fatal("stale settings erased schedule")
	}
	if !got.Enabled || got.Time != "08:00" {
		t.Fatalf("bad persistence: %+v", got)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas", strings.NewReader(`{"active":[]}`)))
	if s.Settings().DailyCheckIns[id] != got {
		t.Fatal("activation erased schedule")
	}
	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/personas", nil))
	var payload struct {
		Personas []struct {
			ID    string             `json:"id"`
			Daily store.DailyCheckIn `json:"dailyCheckIn"`
		} `json:"personas"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, row := range payload.Personas {
		if row.ID == id {
			if row.Daily != got {
				t.Fatal("schedule not returned")
			}
			return
		}
	}
	t.Fatal("persona missing")
}

func TestGatewayRestoresWorkspacesWithoutLogin(t *testing.T) {
	options := AuthOptions{Root: t.TempDir()}
	first := newGateway(options, fakeVerifier{})
	tenant, err := first.tenant("alice")
	if err != nil {
		t.Fatal(err)
	}
	root := tenant.store.Root()
	first.Close()
	second := newGateway(options, fakeVerifier{})
	defer second.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		second.mu.Lock()
		found := false
		for _, tenant := range second.tenants {
			if tenant.store.Root() == root {
				found = true
			}
		}
		second.mu.Unlock()
		if found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("workspace did not restore without a login")
}

// Forgetting a persona's whole memory takes the app's own bookkeeping with it:
// there is nothing left for the persona to reconcile, so it is not woken.
func TestForgettingMemoryDropsSchedules(t *testing.T) {
	app, s := app(t)
	id := app.catalogue.All()[0].ID
	if _, err := s.SaveSchedule(store.Schedule{Skill: id, At: time.Now().Add(time.Hour).UTC(), Note: "朝の薬"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/personas/"+id+"/memory", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if left, err := s.Schedules(""); err != nil || len(left) != 0 {
		t.Fatalf("left = %+v (%v)", left, err)
	}
}

func TestPausingPersonaDropsFutureSchedulesImmediately(t *testing.T) {
	app, s := app(t)
	id := app.catalogue.All()[0].ID
	settings := s.ConfiguredSettings()
	settings.Active = []string{id}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSchedule(store.Schedule{Skill: id, At: time.Now().Add(24 * time.Hour).UTC(), Note: "明日の用事"}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas", strings.NewReader(`{"active":[]}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if left, err := s.Schedules(id); err != nil || len(left) != 0 {
		t.Fatalf("future schedules survived pause: %+v (%v)", left, err)
	}
}

func TestRemovingPersonaDropsFutureSchedulesImmediately(t *testing.T) {
	app, s := app(t)
	skill := persona.Skill{
		ID: "removable", Name: "removable", Icon: "🧹", Domain: "test",
		Tools: []string{"schedule"},
	}
	raw, err := json.Marshal(skill)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.catalogue.Install(raw, app.knownTool); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSchedule(store.Schedule{Skill: skill.ID, At: time.Now().Add(24 * time.Hour).UTC(), Note: "明日の用事"}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/personas/"+skill.ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if left, err := s.Schedules(skill.ID); err != nil || len(left) != 0 {
		t.Fatalf("future schedules survived removal: %+v (%v)", left, err)
	}
}
