package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/takeshy/kakeratta/internal/cluster"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// instance is one API process of a deployment sharing Firestore.
func instance(t *testing.T, client *firestore.Client, name string) *Gateway {
	t.Helper()
	gateway := newGateway(AuthOptions{
		Root: t.TempDir(), Version: "test",
		FirebaseProjectID: "project", FirebaseAPIKey: "key", FirebaseAuthDomain: "project.firebaseapp.com",
		OpenWorkspace: func(userKey string, blobs store.BlobStore) (store.Workspace, error) {
			return store.NewFirestoreWorkspace(context.Background(), client, userKey, blobs)
		},
		Cluster: cluster.NewFirestore(client, name),
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

// Two instances serve one user: the one holding the workspace runs its
// persona work and answers what needs the runner; the other takes posts,
// streams events and carries runner requests over. When the owner goes, the
// other takes the workspace and its unfinished reactions over, and a
// deletion asked of either reaches both.
func TestTwoInstancesShareOneWorkspace(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the multi-instance test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-cluster-gateway-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer func(delay time.Duration) { sweepDelay = delay }(sweepDelay)
	sweepDelay = time.Second

	// The model says every post is for nobody: the router's decision is
	// kept either way, and it is kept by whichever instance ran the router.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"deliver":[]}`}}}})
	}))
	defer provider.Close()

	a, b := instance(t, client, "a"), instance(t, client, "b")
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
			t.Fatalf("post via %s = %d %s", text, recorder.Code, recorder.Body.String())
		}
		return entry
	}
	routed := func(g *Gateway, id string) func() bool {
		return func() bool {
			tenant, err := g.workspace(key)
			if err != nil {
				return false
			}
			record, err := tenant.store.Routing(id)
			return err == nil && record != nil
		}
	}
	owner := func() string {
		lease, err := a.cluster.Lease(ctx, key)
		if err != nil || !lease.Live(time.Now()) {
			return ""
		}
		return lease.Owner
	}

	// a comes to own the workspace by answering a request that needs it.
	if got := call(a, http.MethodGet, "/api/mcp/connection", ""); got.Code != http.StatusOK {
		t.Fatalf("owner route via a = %d %s", got.Code, got.Body.String())
	}
	if owner() != "a" {
		t.Fatalf("owner = %q", owner())
	}
	tenant, err := a.workspace(key)
	if err != nil {
		t.Fatal(err)
	}
	settings := tenant.store.ConfiguredSettings()
	settings.Provider, settings.BaseURL, settings.APIKey, settings.Active = store.ProviderOpenAI, provider.URL, "test", []string{"gourmet"}
	if err := tenant.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	// b streams the workspace's events, whoever publishes them.
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

	// A post taken by b is reacted to by a, and a's own post reaches b's page.
	fromB := post(b, "follower post")
	eventually(t, "a to route b's post", routed(a, fromB.ID))
	if bTenant, err := b.workspace(key); err != nil || bTenant.leading.Load() {
		t.Fatalf("b leads the workspace: %v", err)
	}
	fromA := post(a, "owner post")
	eventually(t, "b's page to hear a's post", func() bool { return strings.Contains(events.String(), fromA.ID) })
	stopStream()
	<-streaming

	// A runner request made of b is answered by a.
	if got := call(b, http.MethodGet, "/api/mcp/connection", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"agent"`) {
		t.Fatalf("forwarded = %d %s", got.Code, got.Body.String())
	}
	if owner() != "a" {
		t.Fatalf("forwarding moved the workspace to %q", owner())
	}

	// a shuts down with a reaction still unstarted; b takes the workspace
	// over and works it.
	left := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.User, Text: "left behind", At: time.Now()}
	left.Root = left.ID
	if err := tenant.store.Append(left); err != nil {
		t.Fatal(err)
	}
	if err := tenant.store.SaveReaction(store.Reaction{ID: store.NewID("r"), At: time.Now(), Entry: left.ID}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	if owner() != "" {
		t.Fatalf("a's lease outlived it: %q", owner())
	}
	if got := call(b, http.MethodGet, "/api/mcp/connection", ""); got.Code != http.StatusOK {
		t.Fatalf("take over = %d %s", got.Code, got.Body.String())
	}
	if owner() != "b" {
		t.Fatalf("owner after take over = %q", owner())
	}
	eventually(t, "b to take up the reaction a left", routed(b, left.ID))

	// Deleting the account through another instance reaches the owner: it
	// lets the workspace go, and the data goes.
	c := instance(t, client, "c")
	if got := call(c, http.MethodDelete, "/api/workspace", `{"confirm":true}`); got.Code != http.StatusNoContent {
		t.Fatalf("delete via c = %d %s", got.Code, got.Body.String())
	}
	b.mu.Lock()
	_, stillOwned := b.owned[key]
	b.mu.Unlock()
	if stillOwned {
		t.Fatal("b still works a deleted workspace")
	}
	docs, err := client.Collection("users").Doc(key).Collection("timeline").Documents(ctx).GetAll()
	if err != nil || len(docs) != 0 {
		t.Fatalf("timeline left after deletion: %d %v", len(docs), err)
	}
	eventually(t, "the closing mark to be lifted after the sweep", func() bool {
		lease, err := c.cluster.Lease(ctx, key)
		return err == nil && !lease.Closing
	})
}
