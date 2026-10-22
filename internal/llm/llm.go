// Package llm talks to OpenAI-compatible inference services.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ErrNotConfigured is returned when no endpoint or model is set. Every AI
// feature must degrade to "not available yet" rather than fail loudly: reading,
// listening and taking memos work without any of this.
var ErrNotConfigured = errors.New("AIの接続先が設定されていません")

type Client struct {
	BaseURL    string
	APIKey     string
	HTTP       *http.Client
	OpenRouter bool
	Observe    func(CallRecord)
}

func NewOpenRouter(baseURL, apiKey string) *Client {
	client := New(baseURL, apiKey)
	client.OpenRouter = true
	return client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 4 * time.Minute},
	}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Calls belong to an assistant message being handed back, and CallID to the
	// result of one. Both are empty in an ordinary exchange.
	Calls  []ToolCall `json:"-"`
	CallID string     `json:"-"`
}

type Request struct {
	Purpose     string    `json:"-"`
	Attempt     int       `json:"-"`
	Round       int       `json:"-"`
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"-"`
	Thinking    *bool     `json:"-"`
	Exacto      bool      `json:"-"`
	JSON        bool      `json:"-"`
	// Tools are the functions the model may call. With ToolChoice "required" it
	// must call one, which is the whole reason for using them here: a model that
	// answers in prose when it was meant to act is the failure this removes.
	Tools      []Tool `json:"-"`
	ToolChoice string `json:"-"`
	// Files travel with the last user message. A technical book is passed as
	// the file itself so its code listings, tables and figures survive; whether
	// the provider takes a PDF or needs page images is this package's problem,
	// and the caller only ever says "put this book through".
	Files []Attachment `json:"-"`
}

// Tool is one function the model may call, with its arguments as JSON Schema.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is the model asking for one of them. Args is the raw JSON object the
// model wrote, parsed by whoever runs the tool.
type ToolCall struct {
	ID   string
	Name string
	Args string
}

// Reply is what came back: prose, tool calls, or both.
type Reply struct {
	Content      string
	Calls        []ToolCall
	Usage        *Usage
	FinishReason string
}

// Attachment is a file handed to the model.
type Attachment struct {
	Name string
	MIME string
	Data []byte
}

// contentParts turns a message and its attachments into the OpenAI multipart
// content shape. A provider that does not take files will refuse the request,
// and the caller falls back to text.
func contentParts(text string, files []Attachment) []map[string]any {
	parts := []map[string]any{{"type": "text", "text": text}}
	for _, file := range files {
		encoded := "data:" + file.MIME + ";base64," + base64.StdEncoding.EncodeToString(file.Data)
		if strings.HasPrefix(file.MIME, "image/") {
			parts = append(parts, map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": encoded},
			})
			continue
		}
		parts = append(parts, map[string]any{
			"type": "file",
			"file": map[string]any{"filename": file.Name, "file_data": encoded},
		})
	}
	return parts
}

// Chat sends one completion request and returns the assistant's text.
// transportAttempts is how many times a chat completion is sent when the
// connection fails before a response; transportBackoff is the wait before the
// second try, and twice it before the third. Variables so tests can shorten them.
var (
	transportAttempts = 3
	transportBackoff  = time.Second
)

func (c *Client) Chat(ctx context.Context, request Request) (string, error) {
	reply, err := c.Complete(ctx, request)
	return reply.Content, err
}

// wire turns one message into the shape the API takes, including the two that
// only appear in a tool exchange: an assistant handing its calls back, and the
// result of one going the other way.
func wire(message Message) map[string]any {
	out := map[string]any{"role": message.Role, "content": message.Content}
	if message.CallID != "" {
		out["tool_call_id"] = message.CallID
	}
	if len(message.Calls) > 0 {
		calls := make([]map[string]any, 0, len(message.Calls))
		for _, call := range message.Calls {
			calls = append(calls, map[string]any{
				"id": call.ID, "type": "function",
				"function": map[string]any{"name": call.Name, "arguments": call.Args},
			})
		}
		out["tool_calls"] = calls
	}
	return out
}

// Complete sends one completion request and returns everything that came back.
func (c *Client) Complete(ctx context.Context, request Request) (Reply, error) {
	if c == nil || c.BaseURL == "" || request.Model == "" {
		return Reply{}, ErrNotConfigured
	}
	messages := make([]any, 0, len(request.Messages))
	for i, message := range request.Messages {
		last := i == len(request.Messages)-1
		if last && len(request.Files) > 0 && message.Role == "user" {
			messages = append(messages, map[string]any{
				"role": message.Role, "content": contentParts(message.Content, request.Files),
			})
			continue
		}
		messages = append(messages, wire(message))
	}
	body := map[string]any{
		"model":       request.Model,
		"messages":    messages,
		"temperature": request.Temperature,
	}
	if request.MaxTokens > 0 {
		body["max_tokens"] = request.MaxTokens
	}
	if c.OpenRouter {
		// OpenRouter's text slots reason by default; low explicitly overrides
		// this to false. This avoids relying on each model alias's own default.
		enabled := true
		if request.Thinking != nil {
			enabled = *request.Thinking
		}
		body["reasoning"] = map[string]any{"enabled": enabled}
	} else if request.Thinking != nil {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": *request.Thinking}
	}
	if (request.Exacto || len(request.Tools) > 0) && c.OpenRouter {
		body["provider"] = map[string]any{"sort": "exacto"}
	}
	if request.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(request.Tools) > 0 {
		listed := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			listed = append(listed, map[string]any{"type": "function", "function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": parameters,
			}})
		}
		body["tools"] = listed
		if request.ToolChoice != "" {
			body["tool_choice"] = request.ToolChoice
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Reply{}, err
	}
	record := NewCallRecord(ctx, c.BaseURL, request.Model, request.Purpose, request.Attempt, request.Round)
	defer func() {
		if c.Observe != nil {
			record.Finish()
			c.Observe(*record)
		} else {
			record.Log()
		}
	}()
	var response *http.Response
	for attempt := 1; ; attempt++ {
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return Reply{}, err
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		response, err = c.HTTP.Do(httpRequest)
		if err == nil {
			break
		}
		// A connection that was reset or refused before any answer came back
		// is tried again a couple of times; the provider has not seen a
		// finished request, so nothing is answered twice. A context that
		// ended is not retried.
		if attempt >= transportAttempts || ctx.Err() != nil {
			record.Outcome = "transport_error"
			return Reply{}, err
		}
		select {
		case <-ctx.Done():
			record.Outcome = "transport_error"
			return Reply{}, err
		case <-time.After(transportBackoff * time.Duration(attempt)):
		}
	}
	record.Status = response.StatusCode
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		record.Outcome = "read_error"
		return Reply{}, err
	}
	var reported struct {
		Usage *Usage `json:"usage"`
	}
	if json.Unmarshal(payload, &reported) == nil {
		record.Usage = reported.Usage
	}
	if response.StatusCode >= 300 {
		record.Outcome = "http_error"
		return Reply{}, fmt.Errorf("%s: %s", response.Status, firstLine(payload))
	}
	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		record.Outcome = "decode_error"
		return Reply{}, fmt.Errorf("応答を解釈できません: %w", err)
	}
	if parsed.Error.Message != "" {
		record.Outcome = "provider_error"
		return Reply{}, errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		record.Outcome = "empty_choices"
		return Reply{}, errors.New("応答が空です")
	}
	message := parsed.Choices[0].Message
	record.FinishReason = parsed.Choices[0].FinishReason
	reply := Reply{Content: message.Content, Usage: record.Usage, FinishReason: record.FinishReason}
	for _, call := range message.ToolCalls {
		reply.Calls = append(reply.Calls, ToolCall{
			ID: call.ID, Name: call.Function.Name, Args: call.Function.Arguments,
		})
	}
	record.Outcome = "success"
	return reply, nil
}

// Models lists what the endpoint will serve, so the settings can offer the
// models an account actually has rather than a list written into the app.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if c == nil || c.BaseURL == "" {
		return nil, ErrNotConfigured
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", response.Status, firstLine(payload))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// DecodeJSON reads a JSON object out of a model reply, tolerating a fenced
// block around it.
func DecodeJSON(reply string, target any) error {
	trimmed := strings.TrimSpace(reply)
	if fence := strings.Index(trimmed, "```"); fence >= 0 {
		rest := trimmed[fence+3:]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		trimmed = strings.TrimSpace(rest)
	}
	start := strings.IndexAny(trimmed, "{[")
	end := strings.LastIndexAny(trimmed, "}]")
	if start < 0 || end <= start {
		return fmt.Errorf("JSONが含まれていません")
	}
	return json.Unmarshal([]byte(trimmed[start:end+1]), target)
}

func firstLine(payload []byte) string {
	text := strings.TrimSpace(string(payload))
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	if len(text) > 400 {
		text = text[:400]
	}
	return text
}
