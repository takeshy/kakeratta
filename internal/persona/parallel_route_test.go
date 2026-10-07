package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestRoutingEvaluatesIsolatedConditionsInBoundedParallelCalls(t *testing.T) {
	started := make(chan struct{}, 12)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); n > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, n) {
				break
			}
		}
		var body struct {
			Messages []llm.Message
			Model    string
			Tools    []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "lightning" || len(body.Tools) != 1 || body.Tools[0].Function.Name != matchTool.Name {
			t.Errorf("request = %+v", body)
		}
		// Every request sees exactly its own condition, but keeps the whole thread.
		if strings.Count(body.Messages[1].Content, "unique-condition-") != 1 || !strings.Contains(body.Messages[1].Content, "earlier context") {
			t.Error("condition isolation or thread context lost")
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
			return
		}
		writeResearchReply(w, matchTool.Name, map[string]any{"match": true, "reason": "matches its own condition"})
	}))
	defer server.Close()
	skills := make([]Skill, 12)
	want := make([]string, 12)
	for i := range skills {
		want[i] = fmt.Sprintf("p%d", i)
		skills[i] = Skill{ID: want[i], Domain: fmt.Sprintf("unique-condition-%d", i)}
	}
	client := llm.New(server.URL, "")
	var mu sync.Mutex
	var usage []llm.CallRecord
	client.Observe = func(call llm.CallRecord) { mu.Lock(); defer mu.Unlock(); usage = append(usage, call) }
	result := make(chan Routing, 1)
	go func() {
		result <- Route(context.Background(), client, "lightning", skills, []Entryish{{Text: "earlier context"}}, Entryish{ID: "post", Text: "new post"})
	}()
	for i := 0; i < 8; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("routing did not start eight calls concurrently")
		}
	}
	close(release)
	var got Routing
	select {
	case got = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("routing did not finish")
	}
	if !got.Answered || !reflect.DeepEqual(got.Deliver, want) || maximum.Load() != 8 {
		t.Fatalf("routing=%+v max=%d", got, maximum.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(usage) != 12 {
		t.Fatalf("usage calls=%d", len(usage))
	}
	seen := map[string]bool{}
	for _, call := range usage {
		if call.Purpose != "routing" || call.Trigger != "post" || call.Persona == "" {
			t.Errorf("usage=%+v", call)
		}
		seen[call.Persona] = true
	}
	if len(seen) != 12 {
		t.Fatal("per-persona usage identifiers lost")
	}
}

func TestRoutingDoesNotPublishPartialOrCanceledDecisions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []llm.Message }
		_ = json.NewDecoder(req.Body).Decode(&body)
		if strings.Contains(body.Messages[1].Content, "bad-condition") {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeResearchReply(w, matchTool.Name, map[string]any{"match": true, "reason": "match"})
	}))
	defer server.Close()
	got := Route(context.Background(), llm.New(server.URL, ""), "lightning", []Skill{{ID: "good"}, {ID: "bad", Domain: "bad-condition"}}, nil, Entryish{})
	if got.Answered || len(got.Deliver) > 0 || !strings.Contains(got.Error, "bad") {
		t.Fatalf("partial routing=%+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = Route(ctx, llm.New(server.URL, ""), "lightning", []Skill{{ID: "good"}}, nil, Entryish{})
	if got.Answered || len(got.Deliver) > 0 || got.Error == "" {
		t.Fatalf("canceled routing=%+v", got)
	}
}

func TestAnswerWithoutReadsNeedsOnlyOneCallAndCannotSelectDepth(t *testing.T) {
	for _, text := range []string{"返答です", ""} {
		t.Run(fmt.Sprintf("spoken=%t", text != ""), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				var body struct {
					Tools []struct {
						Function struct {
							Name       string
							Parameters map[string]any
						}
					}
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				for _, tool := range body.Tools {
					if tool.Function.Name == "finish_read" || tool.Function.Name == "finish_silent" {
						t.Error("preliminary research tool still offered")
					}
					props, _ := tool.Function.Parameters["properties"].(map[string]any)
					if props["deep"] != nil || props["research_plan"] != nil {
						t.Error("model still selects depth or a plan")
					}
				}
				writeResearchReply(w, submitAnswer, Answer{Text: text, Sources: []string{}})
			}))
			defer server.Close()
			answer, err := composeAnswer(context.Background(), llm.New(server.URL, ""), "super", nil, turnRules{}, answerReads{
				capabilities: func() readCapabilities { return readCapabilities{readsRemaining: 10} }, lookup: func([]ReadStep) string { t.Error("unneeded read"); return "" },
			})
			if err != nil || answer == nil || answer.Text != text || calls != 1 {
				t.Fatalf("answer=%+v calls=%d err=%v", answer, calls, err)
			}
		})
	}
}
