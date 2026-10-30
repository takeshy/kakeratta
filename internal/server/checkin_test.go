package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestManualCheckInEndpoint(t *testing.T) {
	ws, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: ws, Catalogue: persona.NewCatalogue(ws), Passive: true})
	t.Cleanup(s.Halt)
	// Missing model configuration must not consume any execution claims.
	settings := ws.Settings()
	settings.BaseURL = ""
	if err := ws.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	request := func(period string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/check-ins/"+period, nil))
		if w.Code != want {
			t.Fatalf("%s: status %d: %s", period, w.Code, w.Body.String())
		}
	}
	request("daily", http.StatusBadRequest)
	request("bad", http.StatusBadRequest)
	settings.BaseURL = "http://example.invalid"
	settings.APIKey = "test"
	if err := ws.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		request("daily", http.StatusAccepted)
		request("monthly", http.StatusAccepted)
	}
	items, err := ws.Reactions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manual := 0
	for _, item := range items {
		if item.CheckIn != "" {
			manual++
		}
	}
	if manual != 2 {
		t.Fatalf("manual reactions = %d; want 2", manual)
	}
}
