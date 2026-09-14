package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/takeshy/kakeratta/internal/social"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func socialRequest(s *Server, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestSocialPreviewPublicationRetryAndAccountIsolation(t *testing.T) {
	s, workspace := socialApp(t)
	for _, e := range []timeline.Entry{
		{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "hello", At: time.Now()},
		{ID: "a", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "tabe", Text: "reply", At: time.Now()},
		{ID: "private", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "gout", Text: "private", At: time.Now()},
	} {
		if err := workspace.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	var sent int
	account := "did:plc:one"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "createSession") {
			fmt.Fprintf(w, `{"did":%q,"accessJwt":"secret"}`, account)
			return
		}
		sent++
		var body struct {
			Record map[string]any `json:"record"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(fmt.Sprint(body.Record), "private") {
			t.Error("private reply sent")
		}
		fmt.Fprintf(w, `{"uri":"at://%s/app.bsky.feed.post/%d","cid":"cid-%d"}`, account, sent, sent)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		_, persistence, _ := s.blueskyOAuth()
		connection, _ := persistence.Connection(context.Background())
		did := connection.DID
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p", "a"}}
	preview := socialRequest(s, "/api/social/preview", body)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	var plan social.Plan
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest
	for i := 0; i < 2; i++ {
		result := socialRequest(s, "/api/social/publish", body)
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"error":""`) {
			t.Fatal(result.Body.String())
		}
	}
	if sent != 2 {
		t.Fatalf("duplicate publication: %d", sent)
	}
	account = "did:plc:two"
	setSocialConnection(t, s, account)
	stale := socialRequest(s, "/api/social/publish", body)
	if stale.Code != 409 {
		t.Fatal("account change must invalidate preview")
	}
	preview = socialRequest(s, "/api/social/preview", body)
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest
	result := socialRequest(s, "/api/social/publish", body)
	if result.Code != 200 || sent != 4 {
		t.Fatalf("account: %d %s", sent, result.Body.String())
	}
	entry, _ := workspace.Entry("a")
	entry.Text = "changed"
	workspace.Append(entry)
	result = socialRequest(s, "/api/social/publish", body)
	if result.Code != 409 || sent != 4 {
		t.Fatalf("stale preview: %d %s", sent, result.Body.String())
	}
}
func TestSocialPendingDoesNotPublishAgain(t *testing.T) {
	s, workspace := socialApp(t)
	workspace.Append(timeline.Entry{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "hello", At: time.Now()})
	workspace.ClaimPublication("bluesky:did:plc:one:p", store.Publication{State: "pending"})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "createSession") {
			t.Error("pending post resent")
		}
		fmt.Fprint(w, `{"did":"did:plc:one","accessJwt":"secret"}`)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		_, persistence, _ := s.blueskyOAuth()
		connection, _ := persistence.Connection(context.Background())
		did := connection.DID
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p"}}
	preview := socialRequest(s, "/api/social/preview", body)
	var plan social.Plan
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest
	result := socialRequest(s, "/api/social/publish", body)
	if !strings.Contains(result.Body.String(), "重複防止") {
		t.Fatal(result.Body.String())
	}
}
func TestSocialEndpointsRequireAuthentication(t *testing.T) {
	s, _ := app(t, "secret")
	for _, path := range []string{"/api/social/preview", "/api/social/publish"} {
		if w := socialRequest(s, path, socialBody{}); w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestSocialResumesAfterDefiniteRejection(t *testing.T) {
	s, workspace := socialApp(t)
	for _, e := range []timeline.Entry{
		{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "root", At: time.Now()},
		{ID: "a", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "tabe", Text: "reply", At: time.Now()},
	} {
		workspace.Append(e)
	}
	attempts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "createSession") {
			fmt.Fprint(w, `{"did":"did:plc:one","accessJwt":"secret"}`)
			return
		}
		attempts++
		if attempts == 2 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"RateLimitExceeded"}`)
			return
		}
		if attempts == 3 {
			var body struct {
				Record struct {
					Reply struct {
						Parent struct {
							URI string `json:"uri"`
						} `json:"parent"`
					} `json:"reply"`
				} `json:"record"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Record.Reply.Parent.URI != "at://did:plc:one/app.bsky.feed.post/1" {
				t.Error("lost saved parent")
			}
		}
		fmt.Fprintf(w, `{"uri":"at://did:plc:one/app.bsky.feed.post/%d","cid":"cid"}`, attempts)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		_, persistence, _ := s.blueskyOAuth()
		connection, _ := persistence.Connection(context.Background())
		did := connection.DID
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p", "a"}}
	preview := socialRequest(s, "/api/social/preview", body)
	var plan social.Plan
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest
	first := socialRequest(s, "/api/social/publish", body)
	if !strings.Contains(first.Body.String(), "HTTP 429") {
		t.Fatal(first.Body.String())
	}
	second := socialRequest(s, "/api/social/publish", body)
	if attempts != 3 || !strings.Contains(second.Body.String(), `"error":""`) {
		t.Fatalf("%d %s", attempts, second.Body.String())
	}
}

func socialApp(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("KAKERATTA_PUBLIC_URL", "https://example.com")
	t.Setenv("KAKERATTA_BLUESKY_KEY", strings.Repeat("01", 32))
	s, w := app(t, "")
	setSocialConnection(t, s, "did:plc:one")
	return s, w
}
func setSocialConnection(t *testing.T, s *Server, did string) {
	t.Helper()
	_, persistence, err := s.blueskyOAuth()
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.SaveConnection(context.Background(), &social.Connection{DID: syntax.DID(did), Handle: "alice.bsky.social", SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitPublicationResumesPerPartAndKeepsRootReference(t *testing.T) {
	s, workspace := socialApp(t)
	workspace.Append(timeline.Entry{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: strings.Repeat("文章。", 250), At: time.Now()})
	attempts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 2 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"RateLimitExceeded"}`)
			return
		}
		var body struct {
			Record struct {
				Reply *struct {
					Root struct {
						URI string `json:"uri"`
					} `json:"root"`
					Parent struct {
						URI string `json:"uri"`
					} `json:"parent"`
				} `json:"reply"`
			} `json:"record"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if attempts > 2 {
			wantParent := "at://did:plc:one/app.bsky.feed.post/1"
			if attempts == 4 {
				wantParent = "at://did:plc:one/app.bsky.feed.post/3"
			}
			if body.Record.Reply == nil || body.Record.Reply.Root.URI != "at://did:plc:one/app.bsky.feed.post/1" || body.Record.Reply.Parent.URI != wantParent {
				t.Errorf("lost split parent/root: %+v", body)
			}
		}
		fmt.Fprintf(w, `{"uri":"at://did:plc:one/app.bsky.feed.post/%d","cid":"cid"}`, attempts)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		did := syntax.DID("did:plc:one")
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p"}}
	preview := socialRequest(s, "/api/social/preview", body)
	var plan social.Plan
	json.Unmarshal(preview.Body.Bytes(), &plan)
	if preview.Code != 200 || len(plan.Posts) != 3 {
		t.Fatal(preview.Body.String())
	}
	body.Digest = plan.Digest
	if first := socialRequest(s, "/api/social/publish", body); !strings.Contains(first.Body.String(), "HTTP 429") {
		t.Fatal(first.Body.String())
	}
	for i := 0; i < 2; i++ {
		if result := socialRequest(s, "/api/social/publish", body); !strings.Contains(result.Body.String(), `"error":""`) {
			t.Fatal(result.Body.String())
		}
	}
	if attempts != 4 {
		t.Fatalf("duplicate part: %d attempts", attempts)
	}
}

func TestSocialLargeImagePreviewAndPublish(t *testing.T) {
	s, workspace := socialApp(t)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(rng.Uint32()), byte(rng.Uint32()), byte(rng.Uint32()), 255
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	if original.Len() <= social.MaxImageBytes {
		t.Fatal("fixture too small")
	}
	blobID, err := workspace.PutBlob(original.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Append(timeline.Entry{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "photo", At: time.Now(), Media: &timeline.Media{BlobID: blobID}}); err != nil {
		t.Fatal(err)
	}
	uploads := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "uploadBlob") {
			uploads++
			data, _ := io.ReadAll(r.Body)
			if len(data) > social.MaxImageBytes || r.Header.Get("Content-Type") != "image/jpeg" {
				t.Error("upload not compressed")
			}
			if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"blob":{"$type":"blob","ref":{"$link":"cid"},"mimeType":"image/jpeg","size":100}}`)
			return
		}
		fmt.Fprint(w, `{"uri":"at://did:plc:one/app.bsky.feed.post/a","cid":"cid"}`)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		_, persistence, _ := s.blueskyOAuth()
		connection, _ := persistence.Connection(context.Background())
		did := connection.DID
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p"}}
	preview := socialRequest(s, "/api/social/preview", body)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	if uploads != 0 {
		t.Fatal("preview published")
	}
	var plan social.Plan
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	body.Digest = plan.Digest
	result := socialRequest(s, "/api/social/publish", body)
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"error":""`) || uploads != 1 {
		t.Fatalf("uploads=%d: %s", uploads, result.Body.String())
	}
	saved, mime, err := workspace.Blob(blobID)
	if err != nil || mime != "image/png" || !bytes.Equal(saved, original.Bytes()) {
		t.Fatal("original changed")
	}
}
