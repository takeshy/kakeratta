package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
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
	for _, want := range []string{"list_asks", "claim_ask", "read_for_ask", "submit_answer", "release_ask", "read_note", "get_thread", "claim_request", "reply_request", "comment_on_entry", "search_timeline", "list_schedules"} {
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
	if string(raw) != `{"asks":[],"requests":[]}` {
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

// list_asks carries the versions of the distributed skill, Wake Gate script,
// and direct-request worker, read from the files, and get_integration hands
// all three over, so an agent allowed only this endpoint keeps them current.
func TestIntegrationIsServedOverMCP(t *testing.T) {
	skill, err := os.ReadFile("../../frontend/public/integrations/hermes/kakeratta/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	gate, err := os.ReadFile("../../frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile("../../frontend/public/integrations/hermes/kakeratta/scripts/direct_request_worker.py")
	if err != nil {
		t.Fatal(err)
	}
	saved := integration
	t.Cleanup(func() { integration = saved })
	SetIntegration(string(skill), string(gate), string(worker))
	if integration.skill.Version == "" || integration.gate.Version == "" || integration.worker.Version == "" {
		t.Fatalf("versions = %q, %q, %q", integration.skill.Version, integration.gate.Version, integration.worker.Version)
	}

	s, _ := app(t)
	ctx := context.Background()
	result, err := s.callMCPTool(ctx, "list_asks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(map[string]any)["integration"].(map[string]string)
	if listed["skill"] != integration.skill.Version || listed["gate"] != integration.gate.Version || listed["worker"] != integration.worker.Version {
		t.Fatalf("integration = %v", listed)
	}
	bundle, err := s.callMCPTool(ctx, "get_integration", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bundle)
	var got struct{ Skill, Gate, Worker integrationFile }
	if json.Unmarshal(raw, &got) != nil || got.Skill.Text != string(skill) || got.Gate.Text != string(gate) || got.Worker.Text != string(worker) {
		t.Fatalf("bundle = %.200s", raw)
	}
}

// A post naming the agent is listed for it, claimed
// and answered over MCP, and the reply lands in the thread as the agent's.
func TestDirectRequestsAreAnsweredOverMCP(t *testing.T) {
	s, w := app(t)
	ctx := context.Background()
	if err := s.changeMCPConnection(ctx, "default", "Hermes", []byte("digest"), true); err != nil {
		t.Fatal(err)
	}
	entry := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User, Text: "@agent 請求書をメールで送って", At: time.Now()}
	entry.Root = entry.ID
	if err := w.Append(entry); err != nil {
		t.Fatal(err)
	}
	s.runner.React(entry.ID, nil)
	var requests []persona.DirectRequest
	deadline := time.Now().Add(5 * time.Second)
	for requests = s.runner.Requests(); len(requests) == 0; requests = s.runner.Requests() {
		if time.Now().After(deadline) {
			t.Fatal("no request was opened")
		}
		s.runner.Poke()
		time.Sleep(10 * time.Millisecond)
	}
	listed, err := s.callMCPTool(ctx, "list_asks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(listed)
	if !strings.Contains(string(raw), `"entryId":"`+entry.ID+`"`) {
		t.Fatalf("list_asks = %s", raw)
	}
	id := requests[0].ID
	open := httptest.NewRecorder()
	s.ServeHTTP(open, httptest.NewRequest(http.MethodGet, "/api/requests", nil))
	if open.Code != http.StatusOK || !strings.Contains(open.Body.String(), `"entryId":"`+entry.ID+`","id":"`+id+`","notify":true,"status":"pending"`) || !strings.Contains(open.Body.String(), `"agentName":"Hermes"`) {
		t.Fatalf("open requests = %d %s", open.Code, open.Body.String())
	}
	muted := httptest.NewRecorder()
	s.ServeHTTP(muted, httptest.NewRequest(http.MethodPut, "/api/requests/"+id+"/notify", strings.NewReader(`{"notify":false}`)))
	if muted.Code != http.StatusOK || !strings.Contains(muted.Body.String(), `"notify":false`) || !s.runner.Requests()[0].Mute {
		t.Fatalf("mute = %d %s", muted.Code, muted.Body.String())
	}
	missing := httptest.NewRecorder()
	s.ServeHTTP(missing, httptest.NewRequest(http.MethodPut, "/api/requests/q_missing/notify", strings.NewReader(`{"notify":false}`)))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing request = %d", missing.Code)
	}
	claimed, err := s.callMCPTool(ctx, "claim_request", json.RawMessage(`{"requestId":"`+id+`","claimId":"c1"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(claimed)
	if !strings.Contains(string(raw), "請求書をメールで送って") {
		t.Fatalf("claim = %s", raw)
	}
	if _, err := s.callMCPTool(ctx, "reply_request", json.RawMessage(`{"requestId":"`+id+`","claimId":"c1"}`)); err == nil {
		t.Fatal("a reply without text passed")
	}
	searched, err := s.callMCPTool(ctx, "search_timeline", json.RawMessage(`{"from":"`+time.Now().In(w.Settings().Location()).Format("2006-01-02")+`"}`))
	if raw, _ := json.Marshal(searched); err != nil || !strings.Contains(string(raw), "請求書") {
		t.Fatalf("search = %s, %v", raw, err)
	}
	if _, err := s.callMCPTool(ctx, "list_schedules", json.RawMessage(`{"limit":5}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(ctx, "comment_on_entry", json.RawMessage(`{"requestId":"`+id+`","claimId":"c1","entryId":"`+entry.ID+`","text":"補足です"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(ctx, "reply_request", json.RawMessage(`{"requestId":"`+id+`","claimId":"c1","text":"送信しました"}`)); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.Timeline()
	found := false
	for _, e := range entries {
		if e.Speaker == timeline.Agent && e.ReplyTo == entry.ID && e.Text == "送信しました" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the agent's reply is not in the timeline: %+v", entries)
	}
}
