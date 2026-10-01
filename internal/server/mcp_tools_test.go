package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func TestMCPNoteListingIncludesWrittenTime(t *testing.T) {
	s, w := app(t)
	if err := w.WriteNote("gourmet", "topics/go.md", "Deployment notes"); err != nil {
		t.Fatal(err)
	}
	result, err := s.callMCPTool(context.Background(), "list_notes", json.RawMessage(`{"persona":"gourmet"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	text := string(raw)
	for _, want := range []string{`"path":"topics/go.md"`, `"written":"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("note listing missing %s: %s", want, text)
		}
	}
}

// The tools that wrote on Hermes's behalf are gone: an external agent plans,
// and only the app writes.
func TestMCPOffersPlanningToolsAndNoWrites(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range kakerattaMCPTools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"list_asks", "claim_ask", "read_for_ask", "submit_answer", "release_ask", "read_note", "get_thread"} {
		if !names[want] {
			t.Fatalf("missing %s", want)
		}
	}
	for _, gone := range []string{"write_note", "post_report", "claim_task", "complete_task", "cancel_task"} {
		if names[gone] {
			t.Fatalf("%s is still offered", gone)
		}
	}
}

func TestMCPAskToolsCheckArguments(t *testing.T) {
	s, _ := app(t)
	ctx := context.Background()
	if _, err := s.callMCPTool(ctx, "claim_ask", json.RawMessage(`{"askId":"x"}`)); err == nil || !strings.Contains(err.Error(), "claimId") {
		t.Fatalf("missing claimId = %v", err)
	}
	if _, err := s.callMCPTool(ctx, "claim_ask", json.RawMessage(`{"askId":"x","claimId":"c","text":"t"}`)); err == nil {
		t.Fatal("an unknown argument passed")
	}
	if _, err := s.callMCPTool(ctx, "claim_ask", json.RawMessage(`{"askId":"missing","claimId":"c"}`)); err == nil {
		t.Fatal("a missing ask was claimed")
	}
	result, err := s.callMCPTool(ctx, "list_asks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if string(raw) != `{"asks":[]}` {
		t.Fatalf("asks = %s", raw)
	}
}

func TestPlannerIsChosenPerPersona(t *testing.T) {
	s, w := app(t)
	put := func(id, planner string) int {
		t.Helper()
		out := httptest.NewRecorder()
		s.ServeHTTP(out, httptest.NewRequest(http.MethodPut, "/api/personas/"+id+"/planner", strings.NewReader(`{"planner":"`+planner+`"}`)))
		return out.Code
	}
	if code := put("gourmet", store.PlannerExternal); code != http.StatusOK {
		t.Fatalf("put = %d", code)
	}
	if !w.Settings().PlansExternally("gourmet") || w.Settings().PlansExternally("move") {
		t.Fatalf("planners = %v", w.Settings().Planners)
	}
	// A stale settings form does not undo it.
	out := httptest.NewRecorder()
	s.ServeHTTP(out, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"planners":{}}`)))
	if out.Code != http.StatusOK || !w.Settings().PlansExternally("gourmet") {
		t.Fatalf("settings save undid the planner: %d %v", out.Code, w.Settings().Planners)
	}
	result, err := s.callMCPTool(context.Background(), "list_personas", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), `"id":"gourmet","name":"tabe","planner":"external"`) {
		t.Fatalf("personas = %s", raw)
	}
	// Pausing sets the agent aside; the card still shows the saved choice.
	paused := httptest.NewRecorder()
	s.ServeHTTP(paused, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"externalAgent":{"claimMinutes":3,"planMinutes":15,"paused":true}}`)))
	if paused.Code != http.StatusOK || w.Settings().PlansExternally("gourmet") {
		t.Fatalf("pause = %d, still planning externally", paused.Code)
	}
	listed := httptest.NewRecorder()
	s.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/personas", nil))
	if !strings.Contains(listed.Body.String(), `"planner":"external"`) || !strings.Contains(listed.Body.String(), `"externalAgentPaused":true`) {
		t.Fatalf("personas while paused = %s", listed.Body.String())
	}
	if code := put("gourmet", "builtin"); code != http.StatusOK || w.ConfiguredSettings().Planners["gourmet"] != "" {
		t.Fatalf("back to builtin = %d", code)
	}
	if code := put("gourmet", "hermes"); code != http.StatusBadRequest {
		t.Fatalf("unknown planner = %d", code)
	}
	if code := put("nobody", "builtin"); code != http.StatusNotFound {
		t.Fatalf("unknown persona = %d", code)
	}
}

// list_asks carries the versions of the distributed skill and Wake Gate
// script, read from the files, and get_integration hands both over, so an
// agent allowed only this endpoint keeps them current.
func TestIntegrationIsServedOverMCP(t *testing.T) {
	skill, err := os.ReadFile("../../frontend/public/integrations/hermes/kakeratta/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	gate, err := os.ReadFile("../../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py")
	if err != nil {
		t.Fatal(err)
	}
	saved := integration
	t.Cleanup(func() { integration = saved })
	SetIntegration(string(skill), string(gate))
	if integration.skill.Version == "" || integration.gate.Version == "" {
		t.Fatalf("versions = %q, %q", integration.skill.Version, integration.gate.Version)
	}

	s, _ := app(t)
	ctx := context.Background()
	result, err := s.callMCPTool(ctx, "list_asks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(map[string]any)["integration"].(map[string]string)
	if listed["skill"] != integration.skill.Version || listed["gate"] != integration.gate.Version {
		t.Fatalf("integration = %v", listed)
	}
	bundle, err := s.callMCPTool(ctx, "get_integration", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bundle)
	var got struct{ Skill, Gate integrationFile }
	if json.Unmarshal(raw, &got) != nil || got.Skill.Text != string(skill) || got.Gate.Text != string(gate) {
		t.Fatalf("bundle = %.200s", raw)
	}
}
