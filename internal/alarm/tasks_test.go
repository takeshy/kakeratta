package alarm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/option"
)

// fakeAPI is Cloud Tasks: it keeps the tasks it was given and refuses a name
// it already has.
type fakeAPI struct {
	mu    sync.Mutex
	tasks map[string]map[string]any
	paths []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Task map[string]any `json:"task"`
	}
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	name, _ := body.Task["name"].(string)
	w.Header().Set("Content-Type", "application/json")
	if _, exists := f.tasks[name]; exists {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":409,"message":"exists","status":"ALREADY_EXISTS"}}`))
		return
	}
	f.tasks[name] = body.Task
	_ = json.NewEncoder(w).Encode(body.Task)
}

func newTasks(t *testing.T) (*Tasks, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{tasks: map[string]map[string]any{}}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	tasks, err := NewTasks(context.Background(), "projects/p/locations/l/queues/q", "https://service.example/internal/due", "tasks@p.iam.gserviceaccount.com",
		option.WithEndpoint(server.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	tasks.now = func() time.Time { return now }
	return tasks, api
}

// A wake is a task scheduled for the time, named for the workspace and the
// time, that calls back with an identity token.
func TestAWakeIsATaskForThatTime(t *testing.T) {
	tasks, api := newTasks(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if err := tasks.Set(ctx, "abc123", at); err != nil {
		t.Fatal(err)
	}
	if len(api.tasks) != 1 {
		t.Fatalf("tasks = %v", api.tasks)
	}
	for name, task := range api.tasks {
		if !strings.HasPrefix(name, "projects/p/locations/l/queues/q/tasks/due-abc123-") {
			t.Fatalf("name = %s", name)
		}
		if task["scheduleTime"] != "2026-10-02T09:00:00Z" {
			t.Fatalf("scheduleTime = %v", task["scheduleTime"])
		}
		request := task["httpRequest"].(map[string]any)
		if request["url"] != "https://service.example/internal/due" || request["httpMethod"] != "POST" {
			t.Fatalf("request = %v", request)
		}
		oidc := request["oidcToken"].(map[string]any)
		if oidc["serviceAccountEmail"] != "tasks@p.iam.gserviceaccount.com" || oidc["audience"] != "https://service.example/internal/due" {
			t.Fatalf("oidc = %v", oidc)
		}
		raw, _ := base64.StdEncoding.DecodeString(request["body"].(string))
		if string(raw) != `{"workspace":"abc123"}` {
			t.Fatalf("body = %s", raw)
		}
	}
	// The same time again is the same wake.
	if err := tasks.Set(ctx, "abc123", at.Add(5*time.Second)); err != nil {
		t.Fatalf("setting a wake twice = %v", err)
	}
	if len(api.tasks) != 1 {
		t.Fatalf("tasks = %d", len(api.tasks))
	}
	// Another workspace, or another time, is another.
	if err := tasks.Set(ctx, "def456", at); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Set(ctx, "abc123", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(api.tasks) != 3 {
		t.Fatalf("tasks = %d", len(api.tasks))
	}
}

// A wake that is due already is run at once, and one further off than Cloud
// Tasks can schedule is set for the furthest it can.
func TestWakesAreKeptWithinWhatATaskCanDo(t *testing.T) {
	tasks, api := newTasks(t)
	ctx := context.Background()
	now := tasks.now()
	if err := tasks.Set(ctx, "abc123", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Set(ctx, "def456", now.Add(90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for name, task := range api.tasks {
		schedule, has := task["scheduleTime"].(string)
		switch {
		case strings.Contains(name, "due-abc123-"):
			if has {
				t.Fatalf("a wake already due was scheduled for %s", schedule)
			}
		case strings.Contains(name, "due-def456-"):
			at, err := time.Parse(time.RFC3339, schedule)
			if err != nil || at.After(now.Add(30*24*time.Hour)) || at.Before(now.Add(28*24*time.Hour)) {
				t.Fatalf("a far wake was scheduled for %s (%v)", schedule, err)
			}
		}
	}
}

func TestATaskNeedsItsSettings(t *testing.T) {
	if _, err := NewTasks(context.Background(), "", "u", "a"); err == nil {
		t.Fatal("a queue is required")
	}
	if _, err := NewTasks(context.Background(), "q", "", "a"); err == nil {
		t.Fatal("a URL is required")
	}
	if _, err := NewTasks(context.Background(), "q", "u", ""); err == nil {
		t.Fatal("a service account is required")
	}
}

// A wake is accepted only with a token issued to the tasks' account for this
// URL, and it must name a workspace.
func TestOnlyATaskCanWakeAWorkspace(t *testing.T) {
	validate := func(_ context.Context, token, audience string) (string, error) {
		switch {
		case token == "good" && audience == "https://service.example/internal/due":
			return "tasks@p.iam.gserviceaccount.com", nil
		case token == "other":
			return "someone@example.com", nil
		}
		return "", errors.New("bad token")
	}
	request := func(header, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/internal/due", strings.NewReader(body))
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		return r
	}
	const audience, account = "https://service.example/internal/due", "tasks@p.iam.gserviceaccount.com"
	for name, r := range map[string]*http.Request{
		"no token":            request("", `{"workspace":"abc"}`),
		"a bad token":         request("Bearer nope", `{"workspace":"abc"}`),
		"another account":     request("Bearer other", `{"workspace":"abc"}`),
		"no workspace named":  request("Bearer good", `{}`),
		"a body that is junk": request("Bearer good", `nope`),
	} {
		if key, err := Workspace(r, validate, audience, account); err == nil {
			t.Fatalf("%s was accepted as %q", name, key)
		}
	}
	key, err := Workspace(request("Bearer good", `{"workspace":"abc"}`), validate, audience, account)
	if err != nil || key != "abc" {
		t.Fatalf("key = %q, %v", key, err)
	}
}
