package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The point of declaring tools is that the model acts instead of describing an
// action, so what goes out and what comes back both have to survive the trip.
func TestToolsGoOutAndCallsComeBack(t *testing.T) {
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"schedule","arguments":"{\"at\":\"2026-09-16T21:30:00+09:00\"}"}}
		]}}]}`)
	}))
	defer server.Close()

	reply, err := New(server.URL, "").Complete(context.Background(), Request{
		Model:      "test",
		ToolChoice: "required",
		Tools: []Tool{{Name: "schedule", Description: "自分を起こす時刻を登録する",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"at": map[string]any{"type": "string"},
			}}}},
		Messages: []Message{{Role: "user", Content: "今日の21:30からドラマを見る"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Calls) != 1 || reply.Calls[0].Name != "schedule" || reply.Calls[0].ID != "c1" {
		t.Fatalf("calls = %+v", reply.Calls)
	}
	if reply.Calls[0].Args != `{"at":"2026-09-16T21:30:00+09:00"}` {
		t.Fatalf("arguments = %q", reply.Calls[0].Args)
	}
	if sent["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %v", sent["tool_choice"])
	}
	tools, _ := sent["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", sent["tools"])
	}
	function, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if function["name"] != "schedule" || function["parameters"] == nil {
		t.Fatalf("function = %v", function)
	}
}

// A tool exchange hands the model its own calls back and the results beside
// them; both have shapes the API insists on.
func TestAToolExchangeIsSentBackInTheRightShape(t *testing.T) {
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"わかりました"}}]}`)
	}))
	defer server.Close()

	reply, err := New(server.URL, "").Complete(context.Background(), Request{
		Model: "test",
		Messages: []Message{
			{Role: "user", Content: "登録して"},
			{Role: "assistant", Calls: []ToolCall{{ID: "c1", Name: "schedule", Args: `{"at":"x"}`}}},
			{Role: "tool", CallID: "c1", Content: "schedule s_1 を置きました"},
		},
	})
	if err != nil || reply.Content != "わかりました" {
		t.Fatalf("reply = %+v (%v)", reply, err)
	}
	messages, _ := sent["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v", messages)
	}
	assistant, _ := messages[1].(map[string]any)
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("assistant = %v", assistant)
	}
	call, _ := calls[0].(map[string]any)
	if call["type"] != "function" || call["id"] != "c1" {
		t.Fatalf("call = %v", call)
	}
	if function, _ := call["function"].(map[string]any); function["name"] != "schedule" || function["arguments"] != `{"at":"x"}` {
		t.Fatalf("function = %v", call["function"])
	}
	result, _ := messages[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "c1" {
		t.Fatalf("result = %v", result)
	}
	// An ordinary message carries neither, or a provider will refuse it.
	first, _ := messages[0].(map[string]any)
	if _, ok := first["tool_calls"]; ok {
		t.Fatalf("a plain message carried tool_calls: %v", first)
	}
	if _, ok := first["tool_call_id"]; ok {
		t.Fatalf("a plain message carried tool_call_id: %v", first)
	}
}
