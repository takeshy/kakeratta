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
	"mime/multipart"
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
	BaseURL string
	APIKey  string
	HTTP    *http.Client
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
}

type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	JSON        bool      `json:"-"`
	// Files travel with the last user message. A technical book is passed as
	// the file itself so its code listings, tables and figures survive; whether
	// the provider takes a PDF or needs page images is this package's problem,
	// and the caller only ever says "put this book through".
	Files []Attachment `json:"-"`
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
func (c *Client) Chat(ctx context.Context, request Request) (string, error) {
	if c == nil || c.BaseURL == "" || request.Model == "" {
		return "", ErrNotConfigured
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
		messages = append(messages, message)
	}
	body := map[string]any{
		"model":       request.Model,
		"messages":    messages,
		"temperature": request.Temperature,
	}
	if request.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	response, err := c.HTTP.Do(httpRequest)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", response.Status, firstLine(payload))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("応答を解釈できません: %w", err)
	}
	if parsed.Error.Message != "" {
		return "", errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("応答が空です")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Transcribe turns a recorded memo into text. Only the words are wanted here;
// the recording itself is never stored by the app.
func (c *Client) Transcribe(ctx context.Context, model, filename string, audio []byte) (string, error) {
	if c == nil || c.BaseURL == "" || model == "" {
		return "", ErrNotConfigured
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	_ = writer.WriteField("model", model)
	if err := writer.Close(); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", response.Status, firstLine(payload))
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("応答を解釈できません: %w", err)
	}
	return strings.TrimSpace(parsed.Text), nil
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
