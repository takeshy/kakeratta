package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageRecordedForEveryRequestOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, body, outcome string
		status              int
		reported            bool
	}{
		{"success", `{"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"completion_tokens_details":{"reasoning_tokens":4},"prompt_tokens_details":{"cached_tokens":3}},"choices":[{"message":{"content":"private answer"}}]}`, "success", 200, true},
		{"missing", `{"choices":[{"message":{"content":"ok"}}]}`, "success", 200, false},
		{"truncated", `{"choices":[{"finish_reason":"length","message":{"content":"private incomplete answer"}}]}`, "success", 200, false},
		{"zero", `{"usage":{"prompt_tokens":0,"completion_tokens":0},"choices":[{"message":{"content":"ok"}}]}`, "success", 200, true},
		{"empty", `{"usage":{"prompt_tokens":12},"choices":[]}`, "empty_choices", 200, true},
		{"provider", `{"usage":{"prompt_tokens":12},"error":{"message":"private provider error"}}`, "provider_error", 200, true},
		{"http", `{"usage":{"prompt_tokens":12},"error":{"message":"private key"}}`, "http_error", 429, true},
		{"malformed", `private malformed payload`, "decode_error", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New(server.URL, "private-key")
			var records []CallRecord
			client.Observe = func(r CallRecord) { records = append(records, r) }
			ctx := WithTrace(context.Background(), Trace{Job: "job-1", JobAttempt: 3})
			ctx = WithTrace(ctx, Trace{Persona: "persona-1", Trigger: "post-1", Wake: "daily", NoteAttempt: 2, PlanAttempt: 2})
			reply, err := client.Complete(ctx, Request{Model: "test-model", Purpose: "answer", Attempt: 2, Round: 4, Messages: []Message{{Role: "user", Content: "private prompt"}}})
			if (err == nil) != (tc.outcome == "success") {
				t.Fatalf("error = %v", err)
			}
			if len(records) != 1 {
				t.Fatalf("records = %d", len(records))
			}
			r := records[0]
			if r.Path != "model-usage" || r.Outcome != tc.outcome || r.Status != tc.status || (r.Usage != nil) != tc.reported {
				t.Fatalf("record = %+v", r)
			}
			if r.Purpose != "answer" || r.Attempt != 2 || r.RetryCount != 1 || r.Round != 4 || r.JobRetryCount != 2 || r.NoteRetryCount != 1 || r.PlanRetryCount != 1 || r.Persona != "persona-1" || r.Trigger != "post-1" || r.Job != "job-1" || r.Wake != "daily" {
				t.Fatalf("trace = %+v", r)
			}
			if tc.name == "success" && (*r.Usage.Input != 12 || *r.Usage.Output != 7 || *r.Usage.Total != 19 || *r.Usage.OutputDetails.Reasoning != 4 || *r.Usage.InputDetails.Cached != 3 || reply.Usage != r.Usage) {
				t.Fatal("usage was lost")
			}
			if tc.name == "zero" && (r.Usage.Input == nil || *r.Usage.Input != 0 || r.Usage.Total != nil) {
				t.Fatal("zero and unknown were conflated")
			}
			if tc.name == "truncated" && (reply.FinishReason != "length" || r.FinishReason != "length") {
				t.Fatal("completion finish reason was lost")
			}
			if _, ok := sent["purpose"]; ok {
				t.Fatal("logging metadata was sent to provider")
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), "private") {
				t.Fatalf("private content leaked: %s", encoded)
			}
		})
	}
}

type failedTransport struct{}

func (failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private transport error")
}

func TestUsageTransportErrorAndDefaults(t *testing.T) {
	client := New("https://api.tokenfactory.nebius.com/v1", "private-key")
	client.HTTP = &http.Client{Transport: failedTransport{}}
	var records []CallRecord
	client.Observe = func(r CallRecord) { records = append(records, r) }
	_, _ = client.Chat(context.Background(), Request{Model: "test", Purpose: "dictation"})
	if len(records) != 1 || records[0].Outcome != "transport_error" || records[0].Provider != "nebius" || records[0].Usage != nil || records[0].Attempt != 1 || records[0].RetryCount != 0 || records[0].Round != 1 || records[0].Severity != "WARNING" {
		t.Fatalf("records = %+v", records)
	}
	_, _ = client.Complete(context.Background(), Request{})
	if len(records) != 1 {
		t.Fatal("a request never sent was recorded as an API call")
	}
}

func TestUsageDoesNotLogEndpointSecrets(t *testing.T) {
	r := NewCallRecord(context.Background(), "https://private-user:private-password@private-host/path/private-key?key=private-secret", "test", "answer", 1, 1)
	b, _ := json.Marshal(r)
	if r.Provider != "custom" || strings.Contains(string(b), "private") {
		t.Fatalf("endpoint leaked: %s", b)
	}
}
