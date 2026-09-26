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
	"github.com/takeshy/kakeratta/internal/timeline"
)

func TestDailySettingsEndpoint(t *testing.T) {
	app, s := app(t)
	id := app.catalogue.All()[0].ID
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"talk":"minimal"}`, 200},
		{`{"talk":"loud"}`, 400},
		// A time from an older page is not the user's to set any more.
		{`{"time":"08:00","timeZone":"Asia/Tokyo","talk":"minimal"}`, 200},
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
	if !got.Enabled || got.Time != "" || got.Talk != store.CheckInMinimal {
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
			if row.Daily.Talk != store.CheckInMinimal {
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
	if err := s.WriteNote(skill.ID, "errands/tomorrow.md", "明日の用事"); err != nil {
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
	// Nothing is left to show or forget the notes from, so they go too.
	if paths, err := s.NotePaths(skill.ID); err != nil || len(paths) != 0 {
		t.Fatalf("notes survived removal: %v (%v)", paths, err)
	}
}

// The person a schedule would wake can take it off the card; another persona's
// schedule is not reachable through this one's path.
func TestRemovingOneScheduleFromTheCard(t *testing.T) {
	app, s := app(t)
	skills := app.catalogue.All()
	id, other := skills[0].ID, skills[1].ID
	kept, err := s.SaveSchedule(store.Schedule{Skill: id, At: time.Now().Add(2 * time.Hour).UTC(), Note: "夜の薬"})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := s.SaveSchedule(store.Schedule{Skill: id, At: time.Now().Add(time.Hour).UTC(), Note: "朝の薬"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.SaveSchedule(store.Schedule{Skill: other, At: time.Now().Add(time.Hour).UTC(), Note: "洗濯"})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/personas/"+id+"/schedules/"+gone.ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	left, err := s.Schedules(id)
	if err != nil || len(left) != 1 || left[0].ID != kept.ID {
		t.Fatalf("left = %+v (%v)", left, err)
	}

	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/personas/"+id+"/schedules/"+theirs.ID, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("another persona's schedule: status = %d", response.Code)
	}
	if left, _ := s.Schedules(other); len(left) != 1 {
		t.Fatalf("another persona's schedule was removed: %+v", left)
	}
}

// Only a person can be heard from monthly; the card is told the day.
func TestMonthlyCheckInIsForPeople(t *testing.T) {
	app, s := app(t)
	subject := app.catalogue.All()[0].ID
	raw, _ := json.Marshal(persona.Skill{ID: "sato", Name: "佐藤さん", Domain: "友人。", Kind: persona.KindPerson})
	if _, err := app.catalogue.Install(raw, nil); err != nil {
		t.Fatal(err)
	}
	put := func(id, body string) int {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas/"+id+"/daily", strings.NewReader(body)))
		return response.Code
	}
	if code := put(subject, `{"talk":"share","every":"monthly"}`); code != 400 {
		t.Fatalf("a subject went monthly: %d", code)
	}
	if code := put("sato", `{"talk":"share","every":"weekly"}`); code != 400 {
		t.Fatalf("weekly was taken: %d", code)
	}
	if code := put("sato", `{"talk":"share","every":"monthly"}`); code != 200 {
		t.Fatalf("a person could not go monthly: %d", code)
	}
	if s.Settings().DailyCheckIns["sato"].Every != store.CheckInMonthly {
		t.Fatalf("not saved: %+v", s.Settings().DailyCheckIns["sato"])
	}
	response := httptest.NewRecorder()
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
		if row.ID == "sato" && (row.Daily.Every != store.CheckInMonthly || row.Daily.On != store.SummaryDay(s.Root())) {
			t.Fatalf("card = %+v", row.Daily)
		}
	}
}

// The user can set the month's summary right; editing it routes nothing.
func TestSummaryCanBeEdited(t *testing.T) {
	app, s := app(t)
	summary := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.App, Text: "### 人\n- 佐藤さんと会った", At: time.Now(), Summary: timeline.SummaryMonth}
	summary.Root = summary.ID
	remark := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: "gourmet", Text: "check-in", At: time.Now()}
	remark.Root = remark.ID
	for _, entry := range []timeline.Entry{summary, remark} {
		if err := s.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(id string) int {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/entry/"+id, strings.NewReader(`{"text":"### 人\n- 佐藤さんと釣りに行った"}`)))
		return response.Code
	}
	if code := edit(summary.ID); code != 200 {
		t.Fatalf("summary edit = %d", code)
	}
	if got, _ := s.Entry(summary.ID); got.Text != "### 人\n- 佐藤さんと釣りに行った" || got.Summary != timeline.SummaryMonth {
		t.Fatalf("summary = %+v", got)
	}
	if code := edit(remark.ID); code == 200 {
		t.Fatal("a persona's post was edited")
	}
}
