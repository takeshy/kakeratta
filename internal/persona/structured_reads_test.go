package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

func readArgsSchema(t *testing.T, raw json.RawMessage, kind, name string) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	steps := props["steps"].(map[string]any)
	items := steps["items"].(map[string]any)
	for _, option := range items["anyOf"].([]any) {
		branch := option.(map[string]any)["properties"].(map[string]any)
		kinds := branch["type"].(map[string]any)["enum"].([]any)
		names := branch["tool"].(map[string]any)["enum"].([]any)
		if len(kinds) == 1 && kinds[0] == kind && len(names) == 1 && names[0] == name {
			return branch["args"].(map[string]any)
		}
	}
	t.Fatalf("missing branch %s/%s", kind, name)
	return nil
}

func TestStructuredReadArgumentsReachTimeline(t *testing.T) {
	r, s, _ := setup(t, nil, "book")
	skill, _ := r.catalogue.Find(context.Background(), "book")
	skill.MemoryEnabled = memoryDisabled()
	skill.Tools = []string{"web_search"}
	trigger := post(t, s, "期間検索で確かめる投稿")
	researchCalls, answerCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []struct {
				Function struct {
					Name       string
					Parameters json.RawMessage
				}
			}
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if researchCalls == 2 {
			answerCalls++
			writeResearchReply(w, submitAnswer, map[string]any{"text": "投稿を確認しました。", "sources": []string{trigger.ID}, "unrelated": false})
			return
		}
		researchCalls++
		raw := body.Tools[1].Function.Parameters
		entries := readArgsSchema(t, raw, "entries", "")
		if entries["type"] != "object" || entries["additionalProperties"] != false || !reflect.DeepEqual(entries["required"], []any{"ids"}) {
			t.Errorf("entries does not require structured IDs: %v", entries)
		}
		entryProps := entries["properties"].(map[string]any)
		if entryProps["from"] != nil || entryProps["query"] != nil {
			t.Error("ID reader advertises search arguments")
		}
		period := readArgsSchema(t, raw, "tool", "timeline_search")
		if period["type"] != "object" || len(period["anyOf"].([]any)) != 3 {
			t.Error("period search does not require a query or date")
		}
		web := readArgsSchema(t, raw, "tool", "web_search")
		if !reflect.DeepEqual(web["required"], []any{"query"}) || web["properties"].(map[string]any)["from"] != nil {
			t.Error("different tools share argument schemas")
		}
		switch researchCalls {
		case 1:
			if !strings.Contains(body.Messages[0].Content, timelinePeriodReadSpec) || strings.Contains(body.Messages[0].Content, `"args":"`) {
				t.Error("period example missing or legacy string example still offered")
			}
			writeResearchReply(w, "read", map[string]any{"deep": false, "research_plan": []string{"期間内の投稿を確認"}, "steps": []any{
				map[string]any{"type": "tool", "tool": "timeline_search", "args": map[string]any{"from": trigger.At.Add(-time.Hour).Format(time.RFC3339), "to": trigger.At.Add(time.Hour).Format(time.RFC3339)}},
			}})
		case 2:
			result := body.Messages[len(body.Messages)-1].Content
			if !strings.Contains(result, "timeline_search の結果") || !strings.Contains(result, trigger.Text) {
				t.Errorf("structured period search did not retrieve the post: %s", result)
			}
			writeResearchReply(w, "read", map[string]any{"steps": []any{
				map[string]any{"type": "entries", "tool": "", "args": map[string]any{"ids": []string{trigger.ID}}},
			}})
		default:
			result := body.Messages[len(body.Messages)-1].Content
			if !strings.Contains(result, "entries の結果") || !strings.Contains(result, trigger.Text) {
				t.Errorf("structured ID read did not retrieve the post: %s", result)
			}
			writeResearchReply(w, "finish_read", map[string]any{})
		}
	}))
	defer server.Close()
	settings := s.Settings()
	settings.Search.APIKey = "test-key"
	registry := r.registry(settings).WithSearch(search.New(s))
	entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", false, llm.New(server.URL, ""), settings, registry)
	if entry == nil || entry.Text != "投稿を確認しました。" || researchCalls != 2 || answerCalls != 1 {
		t.Fatalf("entry=%+v research=%d answers=%d", entry, researchCalls, answerCalls)
	}
}

func TestReadStepDecodesStructuredAndSavedArguments(t *testing.T) {
	for _, raw := range []string{
		`{"type":"entries","paths":["legacy.md"],"tool":"","args":{"ids":["e_a"]}}`,
		`{"type":"entries","paths":["legacy.md"],"tool":"","args":"{\"ids\":[\"e_a\"]}"}`,
	} {
		var step ReadStep
		if err := json.Unmarshal([]byte(raw), &step); err != nil {
			t.Fatal(err)
		}
		if step.Type != "entries" || step.Args != `{"ids":["e_a"]}` || !reflect.DeepEqual(step.Paths, []string{"legacy.md"}) {
			t.Fatalf("lost structured or saved fields: %+v", step)
		}
		stored, _ := json.Marshal(step)
		var resumed ReadStep
		if err := json.Unmarshal(stored, &resumed); err != nil || !reflect.DeepEqual(step, resumed) {
			t.Fatalf("saved step did not resume: %+v err=%v", resumed, err)
		}
	}
	for _, args := range []string{`[]`, `42`, `true`} {
		var step ReadStep
		if err := json.Unmarshal([]byte(`{"type":"entries","args":`+args+`}`), &step); err == nil {
			t.Errorf("accepted non-object args %s", args)
		}
	}
}

func TestIDReadsRejectSearchFieldsAndInvalidIDs(t *testing.T) {
	c := readCapabilities{tools: []tools.Declaration{{Name: "timeline_search"}}}
	for _, args := range []string{
		`{"ids":["e_a"],"from":"2026-10-07"}`,
		`{"ids":["e_a"],"query":"gift"}`,
		`{"ids":["e_a",42]}`,
		`{"ids":["e_a"," "]}`,
		`{"ids":"e_a"}`,
		`{"ids":[]}`,
		`{"id":42}`,
		`{"ids":["a","b","c","d","e","f","g","h","i","j","k"]}`,
	} {
		if err := validateEntryReads([]ReadStep{{Type: "entries", Args: args}}, c); err == nil {
			t.Errorf("accepted invalid ID read %s", args)
		}
	}
	for _, args := range []string{`{"ids":["e_a"]}`, `{"id":"e_a"}`} {
		if err := validateEntryReads([]ReadStep{{Type: "entries", Args: args}}, c); err != nil {
			t.Errorf("rejected current or legacy ID read %s: %v", args, err)
		}
	}
}

func TestNestedMCPReadSchemaPreservesLocalReferences(t *testing.T) {
	params := map[string]any{"type": "object", "$defs": map[string]any{"Place": map[string]any{"type": "string"}}, "properties": map[string]any{
		"place": map[string]any{"$ref": "#/$defs/Place"},
	}, "required": []string{"place"}}
	before, _ := json.Marshal(params)
	c := readCapabilities{tools: []tools.Declaration{{Name: "geo.lookup", Parameters: params}}}
	raw, _ := json.Marshal(c.tool().Parameters)
	args := readArgsSchema(t, raw, "tool", "geo.lookup")
	ref := args["properties"].(map[string]any)["place"].(map[string]any)["$ref"]
	if ref != "#/properties/steps/items/anyOf/1/properties/args/$defs/Place" || args["$defs"].(map[string]any)["Place"] == nil {
		t.Fatalf("MCP schema reference no longer resolves: %s", raw)
	}
	after, _ := json.Marshal(params)
	if string(before) != string(after) {
		t.Fatal("shared tool schema was modified")
	}
}
