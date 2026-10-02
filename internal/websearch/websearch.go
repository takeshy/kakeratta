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
	Title         string `json:"title"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet"`
	PublishedDate string `json:"published_date,omitempty"`
}

// SearchOptions restricts the source category and publication period.
type SearchOptions struct {
	Topic     string `json:"topic,omitempty"`
	TimeRange string `json:"time_range,omitempty"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

func (o SearchOptions) Validate() error {
	if o.Topic != "" && o.Topic != "general" && o.Topic != "news" {
		return errors.New("topic は general / news で指定してください")
	}
	switch o.TimeRange {
	case "", "day", "week", "month", "year":
	default:
		return errors.New("time_range は day / week / month / year で指定してください")
	}
	for name, date := range map[string]string{"start_date": o.StartDate, "end_date": o.EndDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("%s は YYYY-MM-DD で指定してください", name)
			}
		}
	}
	if o.StartDate != "" && o.EndDate != "" && o.StartDate > o.EndDate {
		return errors.New("start_date は end_date 以前の日付を指定してください")
	}
	if o.TimeRange != "" && (o.StartDate != "" || o.EndDate != "") {
		return errors.New("time_range と start_date / end_date はどちらか一方を指定してください")
	}
	return nil
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
	return c.SearchWithOptions(ctx, query, limit, SearchOptions{})
}

// SearchWithOptions keeps ordinary searches unchanged. News searches default
// to the last day unless the caller supplies a publication period.
func (c *Client) SearchWithOptions(ctx context.Context, query string, limit int, options SearchOptions) ([]Result, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled() {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	if options.Topic == "news" && options.TimeRange == "" && options.StartDate == "" && options.EndDate == "" {
		options.TimeRange = "day"
	}
	body, err := json.Marshal(struct {
		APIKey     string `json:"api_key"`
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		SearchOptions
	}{c.APIKey, query, limit, options})
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
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			Snippet       string `json:"snippet"`
			PublishedDate string `json:"published_date"`
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
		out = append(out, Result{Title: hit.Title, URL: hit.URL, Snippet: snippet, PublishedDate: hit.PublishedDate})
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
		line := fmt.Sprintf("- %s\n  %s\n  %s", result.Title, result.URL, snippet)
		if result.PublishedDate != "" {
			line += "\n  公開日時: " + result.PublishedDate
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
