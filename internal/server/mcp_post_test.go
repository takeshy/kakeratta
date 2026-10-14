package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The agent posts as an active persona that uses it, as written, without waking
// anybody, and only so many times a day.
func TestPostAsPersonaPublishesForExternalPersonasWithinTheDailyLimit(t *testing.T) {
	s, w := app(t)
	ctx := context.Background()
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"今日のおすすめ"}`)); err == nil || !strings.Contains(err.Error(), "外部エージェント") {
		t.Fatalf("a builtin persona's post = %v", err)
	}
	settings := w.ConfiguredSettings()
	settings.Planners = map[string]string{"gourmet": store.PlannerExternal}
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	// Switched off, it keeps its planner but does not post.
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"今日のおすすめ"}`)); err == nil || !strings.Contains(err.Error(), "外部エージェント") {
		t.Fatalf("an inactive persona's post = %v", err)
	}
	settings.Active = append(settings.Active, "gourmet")
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"  "}`)); err == nil {
		t.Fatal("an empty post passed")
	}
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"nobody","text":"x"}`)); err == nil {
		t.Fatal("an unknown persona's post passed")
	}
	for i := range persona.MaxAgentPostsPerDay {
		result, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"今日のおすすめ\\nラーメン"}`))
		if err != nil {
			t.Fatal(err)
		}
		posted := result.(map[string]any)
		if posted["remaining"] != persona.MaxAgentPostsPerDay-i-1 {
			t.Fatalf("post %d = %v", i, posted)
		}
		entry, ok := w.Entry(posted["entryId"].(string))
		if !ok || entry.Kind != timeline.KindPost || entry.Speaker != "gourmet" || entry.Root != entry.ID || entry.Text != "今日のおすすめ\nラーメン" || entry.Model != "agent" {
			t.Fatalf("entry = %+v", entry)
		}
	}
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"もう一件"}`)); err == nil || !strings.Contains(err.Error(), "1日") {
		t.Fatalf("a post past the limit = %v", err)
	}
	reactions, err := w.Reactions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, reaction := range reactions {
		// Only the search index hears of the posts.
		if reaction.ID != "index" {
			t.Fatalf("reactions = %+v", reactions)
		}
	}
	settings.ExternalAgent.Paused = true
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(ctx, "post_as_persona", json.RawMessage(`{"persona":"gourmet","text":"x"}`)); err == nil || !strings.Contains(err.Error(), "外部エージェント") {
		t.Fatalf("a post while paused = %v", err)
	}
}
