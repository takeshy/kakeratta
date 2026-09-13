// Package websearch is the app's one way out to the open web.
//
// A persona never makes the call itself. It declares in its manifest that it
// needs search, and the app hands it results. That keeps three things in the
// app's hands rather than each plugin's: which provider is used, what the
// credentials are, and the rule that every claim comes back with a link.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Result is one hit, and the link is not optional: a persona saying "this may
// be out of date" is only useful if the reader can check.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ErrNotConfigured is what every path gets when no provider is set. A technical
// persona that cannot search says so rather than answering from memory.
var ErrNotConfigured = errors.New("Web検索の接続先が設定されていません")

// Providers are the search APIs the app knows how to speak to, with the
// endpoint each one usually listens on.
var Providers = map[string]struct {
	Label   string `json:"label"`
	BaseURL string `json:"baseUrl"`
	NeedKey bool   `json:"needKey"`
}{
	"brave":  {"Brave Search API", "https://api.search.brave.com/res/v1/web/search", true},
	"tavily": {"Tavily", "https://api.tavily.com/search", true},
}

type Client struct {
	Provider string
	BaseURL  string
	APIKey   string
	HTTP     *http.Client
}

func New(provider, baseURL, apiKey string) *Client {
	provider = strings.TrimSpace(provider)
	if baseURL = strings.TrimSpace(baseURL); baseURL == "" {
		if entry, ok := Providers[provider]; ok {
			baseURL = entry.BaseURL
		}
	}
	return &Client{
		Provider: provider,
		BaseURL:  baseURL,
		APIKey:   strings.TrimSpace(apiKey),
		HTTP:     &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	if c == nil || c.BaseURL == "" {
		return false
	}
	entry, known := Providers[c.Provider]
	return known && (!entry.NeedKey || c.APIKey != "")
}

// Search returns at most limit hits. Every provider is reduced to the same
// shape here, so nothing above this line knows which one answered.
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
	request, err := c.request(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("Web検索が失敗しました (%d)", response.StatusCode)
	}
	results, err := c.decode(response)
	if err != nil {
		return nil, err
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (c *Client) request(ctx context.Context, query string, limit int) (*http.Request, error) {
	switch c.Provider {
	case "tavily":
		body, err := json.Marshal(map[string]any{
			"api_key": c.APIKey, "query": query, "max_results": limit,
		})
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		return request, nil
	case "brave":
		target := fmt.Sprintf("%s?q=%s&count=%d", c.BaseURL, url.QueryEscape(query), limit)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Subscription-Token", c.APIKey)
		return request, nil
	default:
		return nil, ErrNotConfigured
	}
}

func (c *Client) decode(response *http.Response) ([]Result, error) {
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
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
	for _, hit := range payload.Web.Results {
		out = append(out, Result{Title: hit.Title, URL: hit.URL, Snippet: hit.Description})
	}
	for _, hit := range payload.Results {
		snippet := hit.Content
		if snippet == "" {
			snippet = hit.Snippet
		}
		out = append(out, Result{Title: hit.Title, URL: hit.URL, Snippet: snippet})
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
