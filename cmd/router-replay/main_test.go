package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestReplayEscalationProxyAndCost(t *testing.T) {
	for _, mode := range []string{"lightning", "switchyard"} {
		t.Run(mode, func(t *testing.T) {
			var models []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string
					Tools []struct{ Function struct{ Name string } }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				models = append(models, body.Model)
				name, args := "submit_answer", `{"text":"4","sources":[]}`
				if body.Tools[0].Function.Name == "escalate" {
					name, args = "escalate", `{"escalate":true}`
				}
				json.NewEncoder(w).Encode(map[string]any{
					"model": body.Model, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20},
					"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "c", "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}}},
				})
			}))
			defer server.Close()
			provider := llm.New(server.URL+"/v1", "provider-key")
			router := persona.AnswerRouter{}
			want := []string{"low", "high"}
			if mode == "switchyard" {
				router = persona.AnswerRouter{Mode: mode, Client: llm.New(server.URL+"/v1", "proxy-key"), Model: "auto", UltraModel: "fixed"}
				want = []string{"auto"}
			}
			tc := replayCase{ID: "real-reply", Messages: []llm.Message{{Role: "user", Content: "2+2?"}}, PreviousAnswer: "5", Reply: "Reconsider", ExpectedContains: []string{"4", "five"}}
			prices := map[string]price{"low": {1, 2}, "high": {1, 2}, "auto": {1, 2}}
			out := evaluate(context.Background(), tc, mode, router, provider, store.Settings{LowModel: "low", MiddleModel: "middle", HighModel: "high"}, prices)
			if out.Error != "" || out.Answer == nil || out.Answer.Text != "4" {
				t.Fatalf("result=%+v", out)
			}
			if !reflect.DeepEqual(models, want) {
				t.Fatalf("models=%v want=%v", models, want)
			}
			if !reflect.DeepEqual(out.Missing, []string{"five"}) {
				t.Fatalf("checks=%v", out.Missing)
			}
			expectedCost := float64(len(want)) * 140 / 1e6
			if out.EstimatedUSD == nil || *out.EstimatedUSD != expectedCost {
				t.Fatalf("cost=%v want=%g", out.EstimatedUSD, expectedCost)
			}
			// Unknown pricing must never be presented as a zero-cost request.
			out = evaluate(context.Background(), tc, mode, router, provider, store.Settings{LowModel: "low", HighModel: "high"}, nil)
			if out.EstimatedUSD != nil {
				t.Fatal("unknown cost was reported")
			}
		})
	}
}

func TestReplayMissingUsageAndFailedCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail {
				http.Error(w, "unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"model":"middle","choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"submit_answer","arguments":"{\"text\":\"ok\",\"sources\":[]}"}}]}}]}`))
		}))
		out := evaluate(context.Background(), replayCase{ID: "test", Messages: []llm.Message{{Role: "user", Content: "hello"}}}, "lightning", persona.AnswerRouter{}, llm.New(server.URL, ""), store.Settings{MiddleModel: "middle"}, map[string]price{"middle": {1, 1}})
		server.Close()
		if out.EstimatedUSD != nil {
			t.Fatal("missing usage or failed response was priced")
		}
		if (out.Error != "") != fail {
			t.Fatalf("unexpected error %s", out.Error)
		}
	}
}
