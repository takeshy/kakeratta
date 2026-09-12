package capture

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestTextExcludesNonContent(t *testing.T) {
	got := text(`<p>本文</p><script>if (x < 2) { secret() }</script><style>.hidden { color: red }</style><noscript>代替</noscript><svg><text>図</text></svg><p>続き</p>`)
	if got != "本文\n続き" {
		t.Fatalf("text = %q", got)
	}
}

func TestTransportBlocksActualPrivateDestination(t *testing.T) {
	transport := publicTransport()
	defer transport.CloseIdleConnections()
	if transport.Proxy != nil {
		t.Fatal("a proxy would bypass destination validation")
	}
	for _, target := range []string{"http://127.0.0.1:1", "http://[::1]:1", "http://169.254.169.254:1"} {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", target, nil)
		if response, err := transport.RoundTrip(req); err == nil {
			response.Body.Close()
			t.Fatalf("accepted %s", target)
		} else if !strings.Contains(err.Error(), "そのURLは取得できません") {
			t.Fatalf("destination was not rejected by the guard: %v", err)
		}
	}
}
