package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// instance is one API process of a deployment sharing Firestore, wired as
// main wires it.
func instance(t *testing.T, client *firestore.Client) *Gateway {
	t.Helper()
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), Version: "test",
		FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		OpenWorkspace: func(userKey string, blobs store.BlobStore) (store.Workspace, error) {
			return store.NewFirestoreWorkspace(context.Background(), client, userKey, blobs)
		},
		Shared: &Shared{
			Watch: func(ctx context.Context, queued func(string)) {
				store.WatchReactions(ctx, client, queued)
			},
			Pending:   func(ctx context.Context) (map[string]bool, error) { return store.ClaimableWorkspaces(ctx, client) },
			Claimable: func(ctx context.Context, key string) (bool, error) { return store.WorkspaceClaimable(ctx, client, key) },
			Exists:    func(ctx context.Context, key string) (bool, error) { return store.WorkspaceExists(ctx, client, key) },
			Closing:   func(ctx context.Context, key string) (bool, error) { return store.WorkspaceClosing(ctx, client, key) },
		},
	}, fakeVerifier{})
	t.Cleanup(gateway.Close)
	return gateway
}

// sseRecorder is a ResponseWriter an event stream can be read from while it
// is still being written.
type sseRecorder struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
}

func (r *sseRecorder) Header() http.Header { return r.header }
func (r *sseRecorder) WriteHeader(int)     {}
func (r *sseRecorder) Flush()              {}
func (r *sseRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(data)
}
func (r *sseRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Several instances serve one user with nothing owned by any of them: each
// answers what reaches it, work queued anywhere is done once by whichever
// claims it, pages hear events from every instance, a trial made on one is
// applied on another, an instance that never had the workspace open takes up
// work queued for it, and a deletion asked of any instance holds for all.
func TestInstancesShareWorkspacesWithoutOwners(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the multi-instance test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-shared-gateway-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer func(delays []time.Duration) { sweepDelays = delays }(sweepDelays)
	sweepDelays = []time.Duration{time.Second, 2 * time.Second}

	// The router is asked once per post; the model delivers every post to
	// gourmet only when routing is being checked by a person.
	var routed atomic.Int32
	routedTexts := sync.Map{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if strings.Contains(body, "振り分け") {
			routed.Add(1)
			for _, text := range []string{"one", "two", "three", "four", "late"} {
				if strings.Contains(body, "post-"+text) {
					count, _ := routedTexts.LoadOrStore(text, new(atomic.Int32))
					count.(*atomic.Int32).Add(1)
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"deliver":[]}`}}}})
	}))
	defer provider.Close()

	a, b := instance(t, client), instance(t, client)
	uid := "user-" + store.NewID("u")
	key := workspaceKey(uid)
	cookie := &http.Cookie{Name: authCookieName, Value: "session:" + uid}
	call := func(g *Gateway, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		g.ServeHTTP(recorder, request)
		return recorder
	}
	post := func(g *Gateway, text string) timeline.Entry {
		t.Helper()
		recorder := call(g, http.MethodPost, "/api/post", `{"text":"`+text+`"}`)
		var entry timeline.Entry
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &entry) != nil {
			t.Fatalf("post %s = %d %s", text, recorder.Code, recorder.Body.String())
		}
		return entry
	}
	workspace, err := store.NewFirestoreWorkspace(ctx, client, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	routedRecord := func(id string) func() bool {
		return func() bool {
			record, err := workspace.Routing(id)
			return err == nil && record != nil
		}
	}
	settings := workspace.ConfiguredSettings()
	settings.Provider, settings.BaseURL, settings.APIKey, settings.Active = store.ProviderOpenAI, provider.URL, "test", []string{"gourmet"}
	if err := workspace.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	// b's page hears what a does.
	stream, stopStream := context.WithCancel(ctx)
	events := &sseRecorder{header: http.Header{}}
	streaming := make(chan struct{})
	go func() {
		defer close(streaming)
		request := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(stream)
		request.AddCookie(cookie)
		b.ServeHTTP(events, request)
	}()
	time.Sleep(time.Second) // The stream's listener takes its first snapshot.

	// Posts taken by either instance are each routed once, by whichever
	// instance claimed the reaction.
	var entries []timeline.Entry
	for i, text := range []string{"one", "two", "three", "four"} {
		gateway := a
		if i%2 == 1 {
			gateway = b
		}
		entries = append(entries, post(gateway, "post-"+text))
	}
	for _, entry := range entries {
		eventually(t, "routing of "+entry.Text, routedRecord(entry.ID))
	}
	routedTexts.Range(func(text, count any) bool {
		if n := count.(*atomic.Int32).Load(); n != 1 {
			t.Fatalf("post-%s routed %d times", text, n)
		}
		return true
	})
	eventually(t, "b's page to hear a's post", func() bool { return strings.Contains(events.String(), entries[0].ID) })
	stopStream()
	<-streaming

	// A routing check made through b is applied through a.
	check := call(b, http.MethodPost, "/api/entry/"+entries[0].ID+"/routing/check", `{"persona":"gourmet"}`)
	var result struct {
		Token string `json:"token"`
	}
	if check.Code != http.StatusOK || json.Unmarshal(check.Body.Bytes(), &result) != nil || result.Token == "" {
		t.Fatalf("check via b = %d %s", check.Code, check.Body.String())
	}
	if applied := call(a, http.MethodPost, "/api/entry/"+entries[0].ID+"/routing/apply", `{"persona":"gourmet","token":"`+result.Token+`"}`); applied.Code != http.StatusOK {
		t.Fatalf("apply via a = %d %s", applied.Code, applied.Body.String())
	}
	if again := call(b, http.MethodPost, "/api/entry/"+entries[0].ID+"/routing/apply", `{"persona":"gourmet","token":"`+result.Token+`"}`); again.Code != http.StatusOK {
		t.Fatalf("applying again via b = %d %s", again.Code, again.Body.String())
	}

	// With a and b gone, an instance that never had the workspace open takes
	// up work queued for it.
	a.Close()
	b.Close()
	c := instance(t, client)
	late := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User, Text: "post-late", At: time.Now()}
	late.Root = late.ID
	if err := workspace.Append(late); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveReaction(store.Reaction{ID: store.NewID("r"), At: time.Now(), Lane: "thread:" + late.ID, Entry: late.ID}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "c to take up the queued post", routedRecord(late.ID))

	// Deleting the account through c: nothing is claimed any more, the data
	// goes, and the mark is lifted after the sweeps.
	if got := call(c, http.MethodDelete, "/api/workspace", `{"confirm":true}`); got.Code != http.StatusNoContent {
		t.Fatalf("delete via c = %d %s", got.Code, got.Body.String())
	}
	docs, err := client.Collection("users").Doc(key).Collection("timeline").Documents(ctx).GetAll()
	if err != nil || len(docs) != 0 {
		t.Fatalf("timeline left after deletion: %d %v", len(docs), err)
	}
	eventually(t, "the closing mark to be lifted after the sweeps", func() bool {
		closing, err := store.WorkspaceClosing(ctx, client, key)
		return err == nil && !closing
	})
	if exists, _ := store.WorkspaceExists(ctx, client, key); exists {
		t.Fatal("the workspace came back after the sweeps")
	}
}
