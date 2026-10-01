package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrganizationRoutesRejectMissingApprovalAndUnconnectedAgent(t *testing.T) {
	s, workspace := app(t)
	_ = workspace.WriteNote("gourmet", "a.md", "keep")
	for _, item := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/api/personas/gourmet/memory/organize", `{"request":"整理","planner":"external"}`, 400},
		{http.MethodPost, "/api/personas/gourmet/memory/organize", `{"request":"","planner":"builtin"}`, 400},
		{http.MethodPost, "/api/personas/gourmet/memory/organize/apply", `{}`, 400},
		{http.MethodPost, "/api/personas/gourmet/memory/organize/apply", `{"token":"unknown"}`, 409},
		{http.MethodGet, "/api/personas/gourmet/memory/organize?token=unknown", "", 400},
	} {
		response := httptest.NewRecorder()
		s.ServeHTTP(response, httptest.NewRequest(item.method, item.path, strings.NewReader(item.body)))
		if response.Code != item.status {
			t.Errorf("%s %s = %d %s", item.method, item.path, response.Code, response.Body.String())
		}
	}
	if note, ok := workspace.Note("gourmet", "a.md"); !ok || note.Text != "keep" {
		t.Fatal("invalid request changed notes")
	}
}
