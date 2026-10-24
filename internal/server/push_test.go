package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/golang-jwt/jwt/v5"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func configurePush(t *testing.T) webpush.Subscription {
	t.Helper()
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KAKERATTA_PUSH_PRIVATE_KEY", private)
	t.Setenv("KAKERATTA_PUSH_PUBLIC_KEY", public)
	t.Setenv("KAKERATTA_PUSH_SUBJECT", "mailto:operator@example.com")
	return webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/example", Keys: webpush.Keys{P256dh: public, Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
}

func TestPushRegistrationAndAccountGuard(t *testing.T) {
	sub := configurePush(t)
	server, workspace := app(t)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	raw, _ := json.Marshal(sub)
	if w := request("PUT", "/api/push", string(raw)); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/push", string(raw)); w.Code != 204 {
		t.Fatal("registration must be idempotent")
	}
	records, err := workspace.Records(context.Background(), store.RecordPush)
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	w := request("GET", "/api/push?endpoint="+sub.Endpoint, "")
	var config map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &config)
	if config["available"] != true || config["subscribed"] != true {
		t.Fatalf("config: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), pushOptions().VAPIDPrivateKey) {
		t.Fatal("private key exposed")
	}
	r := httptest.NewRequest("PUT", "/api/push", strings.NewReader(string(raw)))
	r.Header.Set("X-Kakeratta-Workspace", "wrong")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("account guard: %d", w.Code)
	}
	r = httptest.NewRequest("PUT", "/api/push", strings.NewReader(string(raw)))
	r.Header.Set("Origin", "https://attacker.example")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("origin guard: %d", w.Code)
	}
	if w := request("DELETE", "/api/push", string(raw)); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	records, _ = workspace.Records(context.Background(), store.RecordPush)
	if len(records) != 0 {
		t.Fatal("subscription retained")
	}
}

func TestPushRejectsUnsafeEndpointsAndMalformedKeys(t *testing.T) {
	sub := configurePush(t)
	server, _ := app(t)
	for _, endpoint := range []string{"http://fcm.googleapis.com/x", "https://127.0.0.1/x", "https://fcm.googleapis.com.attacker.example/x", "https://user@fcm.googleapis.com/x", "https://fcm.googleapis.com:8443/x", "https://web.push.apple.com/x#bad"} {
		sub.Endpoint = endpoint
		raw, _ := json.Marshal(sub)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("PUT", "/api/push", strings.NewReader(string(raw))))
		if w.Code != 400 {
			t.Fatalf("endpoint %q accepted: %d", endpoint, w.Code)
		}
	}
	sub.Endpoint = "https://web.push.apple.com/x"
	sub.Keys.Auth = "bad"
	raw, _ := json.Marshal(sub)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("PUT", "/api/push", strings.NewReader(string(raw))))
	if w.Code != 400 {
		t.Fatal("invalid keys accepted")
	}
	t.Setenv("KAKERATTA_PUSH_PRIVATE_KEY", "broken")
	if pushReady(pushOptions()) {
		t.Fatal("invalid configuration accepted")
	}
}

type pushTransport func(*http.Request) (*http.Response, error)

func (f pushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPushOnlyReminderEventDeliversAndExpiredSubscriptionsAreRemoved(t *testing.T) {
	testPushReminderDelivery(t, "mailto:operator@example.com")
}

func testPushReminderDelivery(t *testing.T, subject string) {
	t.Helper()
	sub := configurePush(t)
	t.Setenv("KAKERATTA_PUSH_SUBJECT", subject)
	server, workspace := app(t)
	raw, _ := json.Marshal(sub)
	_ = workspace.UpdateRecord(context.Background(), store.RecordPush, pushID(sub.Endpoint), time.Time{}, func([]byte, bool) ([]byte, error) { return raw, nil })
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	// The default transport is shared with background work that outlives
	// other tests (model calls from persona wakes), so only the push endpoint
	// is intercepted and counted; anything else goes where it was going.
	// Problems are recorded rather than failed here, since this may run off
	// the test goroutine.
	var mu sync.Mutex
	sends := 0
	var problems []string
	count := func() (int, []string) { mu.Lock(); defer mu.Unlock(); return sends, append([]string(nil), problems...) }
	http.DefaultTransport = pushTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != sub.Endpoint {
			return previous.RoundTrip(r)
		}
		var problem string
		token := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "vapid t="), ",")[0]
		claims := jwt.MapClaims{}
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Authorization") == "" {
			problem = fmt.Sprintf("invalid delivery: %v", r)
		} else if _, _, err := new(jwt.Parser).ParseUnverified(token, claims); err != nil {
			problem = fmt.Sprintf("invalid VAPID JWT: %v", err)
		} else if claims["sub"] != subject {
			problem = fmt.Sprintf("VAPID subject: %v", claims["sub"])
		} else if strings.Contains(string(body), "private birthday") {
			problem = "unencrypted text"
		}
		mu.Lock()
		sends++
		if problem != "" {
			problems = append(problems, problem)
		}
		mu.Unlock()
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader("expired")), Header: make(http.Header)}, nil
	})
	entry := timeline.Entry{ID: "e_reminder", Speaker: "routine", Kind: timeline.KindPost, Text: "private birthday"}
	server.publishEvent("entry", entry)
	if sent, _ := count(); sent != 0 {
		t.Fatal("ordinary entry sent push")
	}
	server.publishEvent("reminder", entry)
	if sent, problems := count(); sent != 1 || len(problems) != 0 {
		t.Fatalf("sends: %d %v", sent, problems)
	}
	records, _ := workspace.Records(context.Background(), store.RecordPush)
	if len(records) != 0 {
		t.Fatal("expired subscription retained")
	}
}

func TestPushHTTPSSubject(t *testing.T) {
	testPushReminderDelivery(t, "https://example.com/contact")
}

// How a direct request ended is pushed; a muted request never emits it.
func TestAgentRepliesArePushed(t *testing.T) {
	sub := configurePush(t)
	t.Setenv("KAKERATTA_PUSH_SUBJECT", "mailto:operator@example.com")
	server, workspace := app(t)
	raw, _ := json.Marshal(sub)
	_ = workspace.UpdateRecord(context.Background(), store.RecordPush, pushID(sub.Endpoint), time.Time{}, func([]byte, bool) ([]byte, error) { return raw, nil })
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	var mu sync.Mutex
	sends := 0
	http.DefaultTransport = pushTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != sub.Endpoint {
			return previous.RoundTrip(r)
		}
		mu.Lock()
		sends++
		mu.Unlock()
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	count := func() int { mu.Lock(); defer mu.Unlock(); return sends }
	reply := timeline.Entry{ID: "e_agent", Speaker: timeline.Agent, Kind: timeline.KindReply, Text: "送信しました"}
	server.publishEvent(persona.AgentReplyEvent, reply)
	if count() != 1 {
		t.Fatalf("sends = %d", count())
	}
}

// A push names who spoke in its title and carries the words in its body.
func TestPushPayload(t *testing.T) {
	entry := timeline.Entry{ID: "e_1", Text: "餌やりの\n時間です。  " + strings.Repeat("あ", 310)}
	got := pushPayload(entry, "めい", "alice")
	if got["title"] != "めい" || got["entry"] != "e_1" || got["workspace"] != "alice" {
		t.Fatalf("payload: %v", got)
	}
	if body := []rune(got["body"]); len(body) != 301 || !strings.HasPrefix(got["body"], "餌やりの 時間です。 あ") || body[300] != '…' {
		t.Fatalf("body: %q", got["body"])
	}
	if got := pushPayload(entry, " ", "alice"); got["title"] != "kakeratta" {
		t.Fatalf("unnamed speaker: %v", got)
	}
}

// The built-in AI's answer is titled with its own name, not the agent's.
func TestAuthorOfTellsTheBuiltinAIFromTheAgent(t *testing.T) {
	names := map[string]string{timeline.Agent: "Hermes"}
	if got := authorOf(timeline.Entry{Speaker: timeline.Agent, Model: "super", Language: "en"}, names); got != "Built-in AI" {
		t.Fatalf("built-in = %q", got)
	}
	if got := authorOf(timeline.Entry{Speaker: timeline.Agent}, names); got != "Hermes" {
		t.Fatalf("agent = %q", got)
	}
	if got := authorOf(timeline.Entry{Speaker: "gone"}, names); got != "gone" {
		t.Fatalf("unknown = %q", got)
	}
}
