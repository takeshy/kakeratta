package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

type researchTransport func(*http.Request) (*http.Response, error)

func (f researchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the actual read executor and HTTP request schema, not a display
// counter: the next model request must reflect searches the host really ran.
func TestResearchSearchCapabilitiesFollowExecutedBudget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		deep, always bool
		searches     int
	}{
		{"ordinary", false, false, 2},
		{"deep", true, false, 4},
		{"always-ultra", false, true, 4},
		{"both", true, true, 4},
	} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/batch=%t", tc.name, batch), func(t *testing.T) {
				r, s, _ := setup(t, nil, "support")
				skill, _ := r.catalogue.Find(context.Background(), "support")
				skill.AlwaysUltra = tc.always
				skill.Tools = []string{"web_search", "weather_forecast", "unknown_tool"}
				trigger := post(t, s, "調査対象の投稿本文")
				previous := http.DefaultTransport
				defer func() { http.DefaultTransport = previous }()
				searchCalls := 0
				http.DefaultTransport = researchTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Host != "api.tavily.com" {
						return previous.RoundTrip(req)
					}
					searchCalls++
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"evidence","url":"https://example.com","content":"search result"}]}`))}, nil
				})
				researchCalls, answerCalls := 0, 0
				var seenRemaining []int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					var body struct {
						Model    string        `json:"model"`
						Messages []llm.Message `json:"messages"`
						Tools    []struct {
							Function struct {
								Name        string          `json:"name"`
								Description string          `json:"description"`
								Parameters  json.RawMessage `json:"parameters"`
							} `json:"function"`
						} `json:"tools"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					respond := func(name string, args any) {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": testCall(name, args)}}})
					}
					if len(body.Tools) == 1 && body.Tools[0].Function.Name == submitAnswer {
						answerCalls++
						transcript, _ := json.Marshal(body.Messages)
						for _, want := range []string{"search result", trigger.Text, "entries の結果", "timeline_search の結果"} {
							if !strings.Contains(string(transcript), want) {
								t.Errorf("answer lost %q", want)
							}
						}
						respond(submitAnswer, Answer{Text: "調査結果", Sources: []string{}})
						return
					}
					if len(body.Tools) != 2 || body.Tools[0].Function.Name != "read" || body.Tools[1].Function.Name != "finish_read" {
						t.Errorf("unexpected tools: %+v", body.Tools)
						respond("finish_read", map[string]any{})
						return
					}
					researchCalls++
					if body.Model != "super" {
						t.Errorf("research model = %s", body.Model)
					}
					remaining := tc.searches - searchCalls
					seenRemaining = append(seenRemaining, remaining)
					tool := body.Tools[0].Function
					system := body.Messages[0].Content
					status := fmt.Sprintf("Web検索: 残り %d 回", remaining)
					if !strings.Contains(system, status) || !strings.Contains(tool.Description, status) || strings.Count(system, "Web検索: 残り") != 1 {
						t.Errorf("missing or stale status: %s", system)
					}
					if remaining == 1 && !strings.Contains(system, "必要な場合だけ") {
						t.Error("last search not identified")
					}
					if remaining == 0 && !strings.Contains(system, "これ以上Web検索は利用できません") {
						t.Error("exhaustion not explained")
					}
					for _, forbidden := range []string{"records", "weather_forecast", "unknown_tool"} {
						if strings.Contains(system, forbidden) || strings.Contains(string(tool.Parameters), forbidden) {
							t.Errorf("unavailable read %s advertised", forbidden)
						}
					}
					if hasReadOption(t, tool.Parameters, "tool", "web_search") != (remaining > 0) || strings.Contains(system, "web_search:") != (remaining > 0) {
						t.Errorf("search schema/instruction disagrees with remaining=%d", remaining)
					}
					if !hasReadOption(t, tool.Parameters, "tool", "timeline_search") || !hasReadOption(t, tool.Parameters, "entries", "") {
						t.Error("other reads were removed with web search")
					}
					searchRounds := tc.searches
					if batch {
						searchRounds = 1
					}
					switch {
					case remaining > 0:
						count := 1
						if batch {
							count = remaining + 1 // One batch exceeds the remaining allowance.
						}
						var steps []ReadStep
						for i := 0; i < count; i++ {
							steps = append(steps, ReadStep{Type: "tool", Tool: "web_search", Args: fmt.Sprintf(`{"query":"query-%d-%d"}`, researchCalls, i)})
						}
						if batch {
							// A second call in the same round must see the spent budget too.
							first := testCall("read", map[string]any{"steps": steps})
							second := testCall("read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "web_search", Args: `{"query":"extra-call"}`}}})
							first["tool_calls"] = append(first["tool_calls"].([]any), second["tool_calls"].([]any)...)
							_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": first}}})
						} else {
							respond("read", map[string]any{"steps": steps})
						}
					case researchCalls == searchRounds+1:
						respond("read", map[string]any{"steps": []ReadStep{{Type: "tool", Tool: "timeline_search", Args: `{"query":"調査対象"}`}}})
					case researchCalls == searchRounds+2:
						respond("read", map[string]any{"steps": []ReadStep{{Type: "entries", Args: fmt.Sprintf(`{"ids":[%q]}`, trigger.ID)}}})
					default:
						respond("finish_read", map[string]any{})
					}
				}))
				defer server.Close()
				settings := s.Settings()
				settings.MiddleModel, settings.HighModel = "super", "ultra"
				settings.Search.APIKey = "test-key"
				registry := r.registry(settings).WithSearch(search.New(s))
				entry := r.run(context.Background(), skill, []timeline.Entry{trigger}, trigger, true, false, "", tc.deep, llm.New(server.URL, ""), settings, registry)
				if entry == nil || entry.Text != "調査結果" || searchCalls != tc.searches || answerCalls != 1 {
					t.Fatalf("entry=%+v searches=%d answers=%d", entry, searchCalls, answerCalls)
				}
				var want []int
				if batch {
					want = []int{tc.searches, 0, 0, 0}
				} else {
					for i := tc.searches; i > 0; i-- {
						want = append(want, i)
					}
					want = append(want, 0, 0, 0)
				}
				if !reflect.DeepEqual(seenRemaining, want) {
					t.Fatalf("remaining per round=%v, want %v", seenRemaining, want)
				}
			})
		}
	}
}

func hasReadOption(t *testing.T, raw []byte, kind, tool string) bool {
	t.Helper()
	var schema struct {
		Properties struct {
			Steps struct {
				Items struct {
					AnyOf []struct {
						Properties map[string]struct{ Enum []string }
					} `json:"anyOf"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, branch := range schema.Properties.Steps.Items.AnyOf {
		for _, k := range branch.Properties["type"].Enum {
			for _, name := range branch.Properties["tool"].Enum {
				if k == kind && name == tool {
					return true
				}
			}
		}
	}
	return false
}

func TestReadCapabilitiesKeepMemoryAndEntryReadsAfterLookupExhaustion(t *testing.T) {
	set := tools.New(tools.Config{}).WithTavilyKey("test").For([]string{"web_search", "kakeratta_help"})
	budget := newLookups(1, 4)
	if _, ok := budget.take("kakeratta_help", "{}"); !ok {
		t.Fatal("lookup unexpectedly refused")
	}
	c := currentReadCapabilities(Skill{}, set, budget, nil, 9)
	raw, _ := json.Marshal(c.tool().Parameters)
	if c.searchRemaining != 0 || len(c.tools) != 0 || !hasReadOption(t, raw, "records", "") || !hasReadOption(t, raw, "entries", "") {
		t.Fatalf("capabilities after lookup exhaustion: %s", raw)
	}
	if !strings.Contains(c.instruction(), "records") || strings.Contains(c.instruction(), "kakeratta_help:") {
		t.Fatal("instruction does not match remaining capabilities")
	}
}
