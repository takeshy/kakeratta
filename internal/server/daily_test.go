package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestDailySettingsEndpoint(t *testing.T) {
	app, s := app(t, "")
	id := app.catalogue.All()[0].ID
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"slots":["morning","evening"],"timeZone":"Asia/Tokyo"}`, 200},
		{`{"slots":["morning"]}`, 400},
		{`{"slots":["midnight"],"timeZone":"Asia/Tokyo"}`, 400},
		{`{"slots":["morning"],"timeZone":"invalid"}`, 400},
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
	if stale.Code != 200 || !reflect.DeepEqual(s.Settings().DailyCheckIns[id], got) {
		t.Fatal("stale settings erased schedule")
	}
	if !reflect.DeepEqual(got.Slots, []string{"morning", "evening"}) || got.TimeZone != "Asia/Tokyo" {
		t.Fatalf("bad persistence: %+v", got)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/personas", strings.NewReader(`{"active":[]}`)))
	if !reflect.DeepEqual(s.Settings().DailyCheckIns[id], got) {
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
			if !reflect.DeepEqual(row.Daily, got) {
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
