package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"
)

// Usage contains provider-reported counts. Missing counts remain null, not zero.
type Usage struct {
	Input         *int `json:"prompt_tokens"`
	Output        *int `json:"completion_tokens"`
	Total         *int `json:"total_tokens"`
	OutputDetails *struct {
		Reasoning *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
	InputDetails *struct {
		Cached *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

type Trace struct {
	Persona     string `json:"persona,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	Wake        string `json:"wake,omitempty"`
	Job         string `json:"job,omitempty"`
	JobAttempt  int    `json:"jobAttempt,omitempty"`
	NoteAttempt int    `json:"noteAttempt,omitempty"`
	PlanAttempt int    `json:"planAttempt,omitempty"`
}

type traceKey struct{}

// WithTrace adds identifiers without retaining prompts or credentials.
func WithTrace(ctx context.Context, trace Trace) context.Context {
	current, _ := ctx.Value(traceKey{}).(Trace)
	if trace.Persona != "" {
		current.Persona = trace.Persona
	}
	if trace.Trigger != "" {
		current.Trigger = trace.Trigger
	}
	if trace.Wake != "" {
		current.Wake = trace.Wake
	}
	if trace.Job != "" {
		current.Job = trace.Job
	}
	if trace.JobAttempt > 0 {
		current.JobAttempt = trace.JobAttempt
	}
	if trace.NoteAttempt > 0 {
		current.NoteAttempt = trace.NoteAttempt
	}
	if trace.PlanAttempt > 0 {
		current.PlanAttempt = trace.PlanAttempt
	}
	return context.WithValue(ctx, traceKey{}, current)
}

// CallRecord is one outbound inference request, including unsuccessful requests.
type CallRecord struct {
	Trace
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Path           string `json:"path"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Purpose        string `json:"purpose"`
	Attempt        int    `json:"attempt"`
	RetryCount     int    `json:"retryCount"`
	Round          int    `json:"round"`
	JobRetryCount  int    `json:"jobRetryCount"`
	NoteRetryCount int    `json:"noteRetryCount"`
	PlanRetryCount int    `json:"planRetryCount"`
	Status         int    `json:"status,omitempty"`
	Outcome        string `json:"outcome"`
	FinishReason   string `json:"finishReason,omitempty"`
	Usage          *Usage `json:"usage"`
	DurationMS     int64  `json:"durationMs"`
	started        time.Time
}

func NewCallRecord(ctx context.Context, endpoint, model, purpose string, attempt, round int) *CallRecord {
	trace, _ := ctx.Value(traceKey{}).(Trace)
	provider := "custom"
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Hostname() != "" {
		// Never record URL userinfo, paths, queries, fragments, or custom hostnames.
		switch parsed.Hostname() {
		case "api.tokenfactory.nebius.com":
			provider = "nebius"
		case "openrouter.ai":
			provider = "openrouter"
		}
	}
	if purpose == "" {
		purpose = "unspecified"
	}
	if attempt < 1 {
		attempt = 1
	}
	if round < 1 {
		round = 1
	}
	return &CallRecord{Trace: trace, Severity: "INFO", Message: "model usage", Path: "model-usage",
		Provider: provider, Model: model, Purpose: purpose, Attempt: attempt, RetryCount: attempt - 1,
		Round: round, JobRetryCount: max(0, trace.JobAttempt-1), NoteRetryCount: max(0, trace.NoteAttempt-1), PlanRetryCount: max(0, trace.PlanAttempt-1), Outcome: "request_error", started: time.Now()}
}

func (record *CallRecord) Finish() {
	record.DurationMS = time.Since(record.started).Milliseconds()
	if record.Outcome != "success" {
		record.Severity = "WARNING"
	}
}

func (record *CallRecord) Log() {
	record.Finish()
	if encoded, err := json.Marshal(record); err == nil {
		fmt.Fprintln(os.Stdout, string(encoded))
	}
}
