package websearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type usageTransport func(*http.Request) (*http.Response, error)

func (f usageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUsageCountsOutboundAttemptsOnly(t *testing.T) {
	calls, failures, requests := 0, 0, 0
	client := New("key")
	client.Observe = func(_ context.Context, failed bool) {
		calls++
		if failed {
			failures++
		}
	}
	status := 200
	client.HTTP = &http.Client{Transport: usageTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
	})}
	for _, options := range []SearchOptions{{}, {Topic: "invalid"}} {
		_, _ = client.SearchWithOptions(context.Background(), "test", 1, options)
	}
	_, _ = client.Search(context.Background(), "", 1)
	status = 429
	_, _ = client.Search(context.Background(), "test", 1)
	client.APIKey = ""
	_, _ = client.Search(context.Background(), "test", 1)
	if calls != 2 || failures != 1 || requests != 2 {
		t.Fatalf("calls=%d failures=%d requests=%d", calls, failures, requests)
	}
}
