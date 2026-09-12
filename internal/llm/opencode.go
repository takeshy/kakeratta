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
	"net/url"
	"strings"
)

// OpenCode runs locally and does not speak the chat-completions shape: a
// conversation is a session, and a message is a list of parts. Ported from
// gemihub-desktop's chat.go so the same local server serves both apps.
//
// The model is written "<providerID>/<modelID>", which is how OpenCode names
// what it will run.
type openCodeClient struct {
	base string
	user string
	pass string
	http *http.Client
}

func (c *Client) openCode() *openCodeClient {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "http://127.0.0.1:4096"
	}
	client := &openCodeClient{base: base, http: c.HTTP}
	// A key of "user:password" is basic auth; anything else is the password.
	if key := strings.TrimSpace(c.APIKey); key != "" {
		if index := strings.Index(key, ":"); index > 0 {
			client.user, client.pass = key[:index], key[index+1:]
		} else {
			client.user, client.pass = "opencode", key
		}
	}
	return client
}

func (o *openCodeClient) headers() map[string]string {
	headers := map[string]string{"Content-Type": "application/json"}
	if o.pass != "" {
		token := base64.StdEncoding.EncodeToString([]byte(o.user + ":" + o.pass))
		headers["Authorization"] = "Basic " + token
	}
	return headers
}

func (o *openCodeClient) post(ctx context.Context, path string, body any, into any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for key, value := range o.headers() {
		request.Header.Set(key, value)
	}
	response, err := o.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", response.Status, firstLine(payload))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(payload, into)
}

func (c *Client) chatOpenCode(ctx context.Context, request Request) (string, error) {
	slash := strings.Index(request.Model, "/")
	if slash <= 0 || slash == len(request.Model)-1 {
		return "", errors.New("OpenCodeのモデルは <providerID>/<modelID> の形式で指定してください")
	}
	client := c.openCode()
	var session struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Session   struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := client.post(ctx, "/session", map[string]any{}, &session); err != nil {
		return "", fmt.Errorf("OpenCodeのセッションを作成できません: %w", err)
	}
	id := session.ID
	if id == "" {
		id = session.SessionID
	}
	if id == "" {
		id = session.Session.ID
	}
	if id == "" {
		return "", errors.New("OpenCodeがセッションIDを返しませんでした")
	}
	var reply any
	body := map[string]any{
		"model": map[string]any{
			"providerID": request.Model[:slash],
			"modelID":    request.Model[slash+1:],
		},
		"parts": []map[string]any{{"type": "text", "text": flatten(request.Messages)}},
	}
	if err := client.post(ctx, "/session/"+url.PathEscape(id)+"/message", body, &reply); err != nil {
		return "", fmt.Errorf("OpenCodeへの送信に失敗しました: %w", err)
	}
	text := openCodeText(reply)
	if text == "" {
		return "", errors.New("OpenCodeの応答が空です")
	}
	return text, nil
}

// flatten turns the conversation into the single prompt OpenCode expects.
func flatten(messages []Message) string {
	var parts []string
	for _, message := range messages {
		switch message.Role {
		case "system":
			parts = append(parts, message.Content)
		case "assistant":
			parts = append(parts, "アシスタント: "+message.Content)
		default:
			parts = append(parts, "読者: "+message.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// openCodeText digs the assistant's words out of whatever shape the server
// answered with; the schema has moved more than once.
func openCodeText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var parts []string
		for _, item := range typed {
			if text := openCodeText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	case map[string]any:
		if kind, ok := typed["type"].(string); ok && kind != "text" && typed["parts"] == nil {
			return ""
		}
		for _, key := range []string{"parts", "message", "content", "info"} {
			if text := openCodeText(typed[key]); text != "" {
				return text
			}
		}
		for _, key := range []string{"text", "output"} {
			if text, ok := typed[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}
