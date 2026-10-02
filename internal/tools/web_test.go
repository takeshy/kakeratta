package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type webSearchTransport func(*http.Request) (*http.Response, error)

func (f webSearchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebSearchForwardsPublicationFiltersAndReturnsDates(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name string
		args Args
		want map[string]string
	}{
		{"ordinary", Args{"query": "AI"}, nil},
		{"latest news", Args{"query": "AI", "topic": "news"}, map[string]string{"topic": "news", "time_range": "day"}},
		{"weekly news", Args{"query": "AI", "topic": "news", "time_range": "week"}, map[string]string{"topic": "news", "time_range": "week"}},
		{"historical news", Args{"query": "AI", "topic": "news", "start_date": "2026-09-01", "end_date": "2026-09-30"}, map[string]string{"topic": "news", "start_date": "2026-09-01", "end_date": "2026-09-30"}},
		{"general date filter", Args{"query": "AI", "start_date": "2026-09-01"}, map[string]string{"start_date": "2026-09-01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			http.DefaultTransport = webSearchTransport(func(req *http.Request) (*http.Response, error) {
				called = true
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if req.URL.String() != "https://api.tavily.com/search" || body["query"] != "AI" || body["api_key"] != "test-key" || body["max_results"] != float64(5) {
					t.Fatalf("unexpected request: %s %+v", req.URL, body)
				}
				for _, name := range []string{"topic", "time_range", "start_date", "end_date"} {
					if want, exists := tc.want[name]; exists {
						if body[name] != want {
							t.Errorf("%s = %v, want %s", name, body[name], want)
						}
					} else if _, exists := body[name]; exists {
						t.Errorf("unexpected %s: %v", name, body[name])
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"AI release","url":"https://example.com/release","content":"Release details","published_date":"2026-10-02T07:00:00Z"}]}`)), Header: http.Header{}}, nil
			})
			set := New(Config{}).WithTavilyKey("test-key").For([]string{"web_search"})
			got, err := set.Run(context.Background(), "web_search", tc.args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !called || !strings.Contains(got.Text, "公開日時: 2026-10-02T07:00:00Z") || len(got.Sources) != 1 || got.Sources[0].URL != "https://example.com/release" {
				t.Fatalf("missing publication date or source: %+v", got)
			}
		})
	}
}

func TestWebSearchRejectsInvalidFiltersBeforeRequest(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = webSearchTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid search made a provider request")
		return nil, nil
	})
	for _, args := range []Args{
		{"topic": "unknown"}, {"time_range": "hour"}, {"start_date": "2026-02-30"},
		{"end_date": "today"}, {"start_date": "2026-10-03", "end_date": "2026-10-02"},
		{"time_range": "day", "start_date": "2026-10-01"}, {"time_range": true}, {"topic": ""},
	} {
		args["query"] = "AI"
		set := New(Config{}).WithTavilyKey("test-key").For([]string{"web_search"})
		if _, err := set.Run(context.Background(), "web_search", args, nil); err == nil {
			t.Errorf("accepted invalid filters: %+v", args)
		}
	}
}
