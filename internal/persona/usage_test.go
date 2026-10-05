package persona

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/takeshy/kakeratta/internal/llm"
)

func TestResearchRoundsDoNotCountAsRetries(t *testing.T) {
	for _, mode := range []string{"valid", "no-tool", "malformed-read"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				switch calls {
				case 1:
					if mode == "no-tool" {
						fmt.Fprint(w, `{"choices":[{"message":{"content":"no tool"}}]}`)
					} else if mode == "malformed-read" {
						fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"r1","function":{"name":"read","arguments":"bad json"}}]}}]}`)
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"r1","function":{"name":"read","arguments":"{\"steps\":[]}"}}]}}]}`)
					}
				case 2:
					fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"r2","function":{"name":"finish_read","arguments":"{}"}}]}}]}`)
				}
			}))
			defer server.Close()
			client := llm.New(server.URL, "")
			var records []llm.CallRecord
			client.Observe = func(r llm.CallRecord) { records = append(records, r) }
			lookups := 0
			_, err := research(context.Background(), client, "test", nil, func() readCapabilities { return readCapabilities{records: true, readsRemaining: maxReads} }, func([]ReadStep) string {
				lookups++
				return "read result"
			})
			if err != nil || len(records) != 2 {
				t.Fatalf("records=%+v error=%v", records, err)
			}
			wantRetry := 0
			if mode != "valid" {
				wantRetry = 1
			}
			if lookups != 1-wantRetry {
				t.Fatalf("invalid research consumed a read: %d", lookups)
			}
			if records[0].Round != 1 || records[0].RetryCount != 0 || records[1].Round != 2 || records[1].RetryCount != wantRetry || records[1].Purpose != "research" {
				t.Fatalf("records=%+v", records)
			}
		})
	}
}

func TestRejectedAnswerUsageRemainsRecorded(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		args := `bad json`
		if calls == 2 {
			args = `{"text":"answer","sources":[]}`
		}
		fmt.Fprintf(w, `{"usage":{"prompt_tokens":10,"completion_tokens":2},"choices":[{"message":{"tool_calls":[{"id":"a","function":{"name":"submit_answer","arguments":%q}}]}}]}`, args)
	}))
	defer server.Close()
	client := llm.New(server.URL, "")
	var records []llm.CallRecord
	client.Observe = func(r llm.CallRecord) { records = append(records, r) }
	answer, err := composeAnswer(context.Background(), client, "test", nil, turnRules{})
	if err != nil || answer == nil || len(records) != 2 {
		t.Fatalf("answer=%+v records=%+v error=%v", answer, records, err)
	}
	if records[0].Usage == nil || records[1].Usage == nil || records[0].RetryCount != 0 || records[1].RetryCount != 1 || records[1].Purpose != "answer" {
		t.Fatalf("records=%+v", records)
	}
}
