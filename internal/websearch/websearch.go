// Package websearch is the app's one way out to the open web.
//
// A persona never makes the call itself. It declares in its manifest that it
// needs search, and the app hands it results. That keeps three things in the
// app's hands rather than each plugin's: which provider is used, what the
// credentials are, and the rule that every claim comes back with a link.
//
// The provider is Tavily. The key is the user's own, entered in Settings.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Endpoint is where Tavily listens. It can be overridden for tests.
const Endpoint = "https://api.tavily.com/search"

// Result is one hit, and the link is not optional: a persona saying "this may
// be out of date" is only useful if the reader can check.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ErrNotConfigured is what every path gets when no key is set. A technical
// persona that cannot search says so rather than answering from memory.
var ErrNotConfigured = errors.New("Web検索の APIキーが設定されていません（設定 → Web検索）")

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		BaseURL: Endpoint,
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.BaseURL != "" && c.APIKey != ""
}

// Search returns at most limit hits.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if !c.Enabled() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	body, err := json.Marshal(map[string]any{"api_key": c.APIKey, "query": query, "max_results": limit})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("Web検索が失敗しました (%d)", response.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	var out []Result
	for _, hit := range payload.Results {
		snippet := hit.Content
		if snippet == "" {
			snippet = hit.Snippet
		}
		out = append(out, Result{Title: hit.Title, URL: hit.URL, Snippet: snippet})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Lines renders results for a prompt. The URL travels with every line so the
// model has no way to state a fact without the link that backs it.
func Lines(results []Result) string {
	var lines []string
	for _, result := range results {
		snippet := result.Snippet
		if runes := []rune(snippet); len(runes) > 300 {
			snippet = string(runes[:300])
		}
		lines = append(lines, fmt.Sprintf("- %s\n  %s\n  %s", result.Title, result.URL, snippet))
	}
	return strings.Join(lines, "\n")
}
