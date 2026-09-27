// Package mcp is the app's client for Model Context Protocol servers.
//
// Only remote servers over Streamable HTTP are supported: the API runs on
// Cloud Run, where there is no process to hand a stdio server to. The
// operator lists the servers; a persona sees their tools next to the built-in
// ones and cannot tell the difference.
//
// The protocol surface used here is small - initialize, tools/list and
// tools/call - so this is written against the specification directly rather
// than pulling in an SDK and everything under it.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the revision of the specification this client speaks.
const ProtocolVersion = "2025-06-18"

// Server is one operator-configured MCP endpoint.
type Server struct {
	// Name prefixes every tool from this server (name.tool), so it has to be
	// something a persona can write in JSON without escaping.
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ParseServers reads the operator's list. Names must be unique and usable as
// a prefix; a mistake here should stop the server from starting rather than
// silently drop a tool.
func ParseServers(raw string) ([]Server, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var servers []Server
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, fmt.Errorf("MCPサーバーの一覧を読めませんでした: %w", err)
	}
	seen := map[string]bool{}
	for i := range servers {
		servers[i].Name = strings.TrimSpace(servers[i].Name)
		servers[i].URL = strings.TrimSpace(servers[i].URL)
		if !validName.MatchString(servers[i].Name) {
			return nil, fmt.Errorf("MCPサーバー名が不正です（英小文字・数字・_・- のみ）: %q", servers[i].Name)
		}
		if seen[servers[i].Name] {
			return nil, fmt.Errorf("MCPサーバー名が重複しています: %s", servers[i].Name)
		}
		seen[servers[i].Name] = true
		if !strings.HasPrefix(servers[i].URL, "https://") && !strings.HasPrefix(servers[i].URL, "http://") {
			return nil, fmt.Errorf("MCPサーバー %s のURLが不正です: %q", servers[i].Name, servers[i].URL)
		}
	}
	return servers, nil
}

// Tool is what a server says it can do, as listed by tools/list.
type Tool struct {
	Annotations struct {
		ReadOnlyHint bool `json:"readOnlyHint"`
	} `json:"annotations,omitempty"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one item of a tool result.
type Content struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	URI         string `json:"uri,omitempty"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Resource    *struct {
		URI      string `json:"uri"`
		Text     string `json:"text,omitempty"`
		MIMEType string `json:"mimeType,omitempty"`
	} `json:"resource,omitempty"`
}

// CallResult is what tools/call returns. IsError is the tool's own failure
// (bad arguments, nothing found), as opposed to a protocol error.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Client talks to one server. It is safe for concurrent use; the session, if
// the server issues one, is shared.
type Client struct {
	server  Server
	http    *http.Client
	version string

	mu      sync.Mutex
	session string
	ready   bool
	nextID  atomic.Int64
}

// MaxBody caps what is read from a server: a tool result goes into a prompt,
// and a prompt has no use for megabytes.
const MaxBody = 4 << 20

func NewClient(server Server, version string) *Client {
	return &Client{server: server, http: &http.Client{Timeout: 60 * time.Second}, version: version}
}

func (c *Client) Server() Server { return c.server }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Method string `json:"method,omitempty"`
}

// ListTools fetches every page of tools/list.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("tools/list の結果を読めませんでした: %w", err)
		}
		tools = append(tools, payload.Tools...)
		if payload.NextCursor == "" {
			return tools, nil
		}
		cursor = payload.NextCursor
	}
	return tools, nil
}

// CallTool runs one tool. Arguments are passed through as the persona wrote
// them; the server validates them against its own schema.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CallResult{}, fmt.Errorf("tools/call の結果を読めませんでした: %w", err)
	}
	return result, nil
}

// call sends one request, opening a session first if there is none. A session
// the server has forgotten (404) is reopened once.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	raw, status, err := c.request(ctx, method, params, true)
	if status == http.StatusNotFound {
		c.mu.Lock()
		c.ready, c.session = false, ""
		c.mu.Unlock()
		if err := c.ensureSession(ctx); err != nil {
			return nil, err
		}
		raw, _, err = c.request(ctx, method, params, true)
	}
	return raw, err
}

func (c *Client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	raw, _, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kakeratta", "version": c.version},
	}, false)
	if err != nil {
		return fmt.Errorf("MCPサーバー %s に接続できませんでした: %w", c.server.Name, err)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &init); err != nil || init.ProtocolVersion == "" {
		return fmt.Errorf("MCPサーバー %s の initialize 応答を読めませんでした", c.server.Name)
	}
	// The server picks the version; an unknown one is still worth trying,
	// because tools/list and tools/call have not changed shape between revisions.
	if _, _, err := c.request(ctx, "notifications/initialized", nil, false); err != nil {
		return fmt.Errorf("MCPサーバー %s との初期化を終えられませんでした: %w", c.server.Name, err)
	}
	c.ready = true
	return nil
}

// request does one HTTP round trip. Notifications (no id) expect no result.
// The caller holds c.mu when locked is false, so the session is read directly.
func (c *Client) request(ctx context.Context, method string, params any, locked bool) (json.RawMessage, int, error) {
	notification := strings.HasPrefix(method, "notifications/")
	message := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if !notification {
		message.ID = c.nextID.Add(1)
	}
	body, err := json.Marshal(message)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("User-Agent", "kakeratta/"+c.version)
	for key, value := range c.server.Headers {
		request.Header.Set(key, value)
	}
	if locked {
		c.mu.Lock()
	}
	session := c.session
	if locked {
		c.mu.Unlock()
	}
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
	}
	if method != "initialize" {
		request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if id := response.Header.Get("Mcp-Session-Id"); id != "" {
		if locked {
			c.mu.Lock()
		}
		c.session = id
		if locked {
			c.mu.Unlock()
		}
	}
	if response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, response.StatusCode, fmt.Errorf("MCPサーバー %s が %d を返しました: %s", c.server.Name, response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if notification {
		return nil, response.StatusCode, nil
	}
	reply, err := readResponse(response, message.ID)
	if err != nil {
		return nil, response.StatusCode, err
	}
	if reply.Error != nil {
		return nil, response.StatusCode, fmt.Errorf("MCPサーバー %s がエラーを返しました (%d): %s", c.server.Name, reply.Error.Code, reply.Error.Message)
	}
	return reply.Result, response.StatusCode, nil
}

// readResponse finds the reply to id in either a JSON body or an SSE stream.
// A stream may carry the server's own notifications first; those are skipped.
func readResponse(response *http.Response, id int64) (*rpcResponse, error) {
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	body := io.LimitReader(response.Body, MaxBody)
	want := fmt.Sprint(id)
	if mediaType == "text/event-stream" {
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64<<10), MaxBody)
		var data []string
		for {
			more := scanner.Scan()
			line := scanner.Text()
			if more && line != "" {
				if strings.HasPrefix(line, "data:") {
					data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
				}
				continue
			}
			if len(data) > 0 {
				if reply := matchReply([]byte(strings.Join(data, "\n")), want); reply != nil {
					return reply, nil
				}
				data = nil
			}
			if !more {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("MCPサーバーの応答に結果がありませんでした")
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if reply := matchReply(raw, want); reply != nil {
		return reply, nil
	}
	return nil, errors.New("MCPサーバーの応答を読めませんでした")
}

// matchReply parses one message or a batch and returns the reply to id.
func matchReply(raw []byte, want string) *rpcResponse {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var batch []rpcResponse
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil
		}
	} else {
		var one rpcResponse
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil
		}
		batch = []rpcResponse{one}
	}
	for i := range batch {
		if batch[i].Method != "" || len(batch[i].ID) == 0 {
			continue
		}
		if strings.Trim(string(batch[i].ID), `"`) == want {
			return &batch[i]
		}
	}
	return nil
}
