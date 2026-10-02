package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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

func TestSocialPreviewAllowsSelectedReplyWithoutItsParent(t *testing.T) {
	s, workspace := socialApp(t)
	for _, entry := range []timeline.Entry{
		{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "original", At: time.Now()},
		{ID: "private-parent", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "tabe", Text: "not selected", At: time.Now()},
		{ID: "chosen", Root: "p", ReplyTo: "private-parent", Kind: timeline.KindReply, Speaker: "user", Text: "chosen reply", At: time.Now()},
	} {
		if err := workspace.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	preview := socialRequest(s, "/api/social/preview", socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p", "chosen"}})
	var plan social.Plan
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &plan) != nil {
		t.Fatal(preview.Body.String())
	}
	if len(plan.Posts) != 2 || plan.Posts[1].EntryID != "chosen" || plan.Posts[1].Parent != "p" {
		t.Fatalf("excluded parent included or wrong reply parent: %+v", plan.Posts)
	}
}

func TestSocialHTMLPreviewAndPublishImages(t *testing.T) {
	s, workspace := socialApp(t)
	source := "<html><body><h1>図</h1></body></html>"
	for _, e := range []timeline.Entry{
		{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "hello", At: time.Now()},
		{ID: "a", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "tabe", Text: source, At: time.Now()},
		{ID: "c", Root: "p", ReplyTo: "a", Kind: timeline.KindReply, Speaker: "user", Text: "thanks", At: time.Now()},
	} {
		if err := workspace.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 20, 30))); err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())
	rendered := social.HTMLImages{SourceHash: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), Images: []string{dataURL, dataURL}, Alt: "図の内容"}
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p", "a", "c"}, HTMLImages: map[string]social.HTMLImages{"a": rendered}}
	uploads, posts := 0, 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "uploadBlob") {
			uploads++
			data, _ := io.ReadAll(r.Body)
			if !bytes.Equal(data, pngData.Bytes()) {
				t.Error("preview image changed")
			}
			fmt.Fprint(w, `{"blob":{"$type":"blob","ref":{"$link":"cid"},"mimeType":"image/png","size":100}}`)
			return
		}
		posts++
		var sent struct {
			Record map[string]any `json:"record"`
		}
		json.NewDecoder(r.Body).Decode(&sent)
		if strings.Contains(fmt.Sprint(sent.Record["text"]), "<html>") {
			t.Error("source leaked")
		}
		if posts == 2 || posts == 3 {
			if sent.Record["embed"].(map[string]any)["images"].([]any)[0].(map[string]any)["alt"] != rendered.Alt {
				t.Error("alt missing")
			}
		}
		if posts == 4 {
			parent := sent.Record["reply"].(map[string]any)["parent"].(map[string]any)["uri"]
			if parent != "at://did:plc:one/app.bsky.feed.post/3" {
				t.Error("reply missed final image")
			}
		}
		fmt.Fprintf(w, `{"uri":"at://did:plc:one/app.bsky.feed.post/%d","cid":"cid"}`, posts)
	}))
	defer provider.Close()
	s.socialClient = func(context.Context) (*social.Client, error) {
		api := atclient.NewAPIClient(provider.URL)
		did := syntax.DID("did:plc:one")
		api.AccountDID = &did
		return social.NewClient(api), nil
	}
	preview := socialRequest(s, "/api/social/preview", body)
	var plan social.Plan
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &plan) != nil || len(plan.Posts) != 4 {
		t.Fatal(preview.Body.String())
	}
	if uploads != 0 || posts != 0 {
		t.Fatal("preview published")
	}
	body.Digest = plan.Digest
	modified := rendered
	modified.Alt = "changed"
	body.HTMLImages["a"] = modified
	if result := socialRequest(s, "/api/social/publish", body); result.Code != 409 {
		t.Fatal("image alteration accepted: ", result.Body.String())
	}
	body.HTMLImages["a"] = rendered
	for i := 0; i < 2; i++ {
		result := socialRequest(s, "/api/social/publish", body)
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"error":""`) {
			t.Fatal(result.Body.String())
		}
	}
	if uploads != 2 || posts != 4 {
		t.Fatalf("duplicate images/posts: %d/%d", uploads, posts)
	}
	entry, _ := workspace.Entry("a")
	if entry.Text != source || entry.Media != nil {
		t.Fatal("saved HTML changed")
	}
}

func TestSocialHTMLRejectsMissingStaleOrMalformedImages(t *testing.T) {
	s, workspace := socialApp(t)
	source := "<html><body>図</body></html>"
	workspace.Append(timeline.Entry{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "tabe", Text: source, At: time.Now()})
	body := socialBody{Root: "p", Target: "bluesky", EntryIDs: []string{"p"}}
	if result := socialRequest(s, "/api/social/preview", body); result.Code != 400 {
		t.Fatal("missing conversion accepted")
	}
	body.HTMLImages = map[string]social.HTMLImages{"p": {SourceHash: "stale", Images: []string{"data:image/png;base64,bad"}}}
	if result := socialRequest(s, "/api/social/preview", body); result.Code != 400 {
		t.Fatal("stale conversion accepted")
	}
	rendered := body.HTMLImages["p"]
	rendered.SourceHash = fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	body.HTMLImages["p"] = rendered
	if result := socialRequest(s, "/api/social/preview", body); result.Code != 400 {
		t.Fatal("malformed image accepted")
	}
	rendered.Images = []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not PNG"))}
	body.HTMLImages["p"] = rendered
	if result := socialRequest(s, "/api/social/preview", body); result.Code != 400 {
		t.Fatal("non-image accepted")
	}
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
		if body.Record["text"] == "new reply" {
			reply := body.Record["reply"].(map[string]any)
			if reply["root"].(map[string]any)["uri"] != "at://did:plc:one/app.bsky.feed.post/1" || reply["parent"].(map[string]any)["uri"] != "at://did:plc:one/app.bsky.feed.post/2" {
				t.Error("new reply was not attached to saved parents")
			}
		}
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
	// A reopened preview exposes saved history, then adding a reply sends only it.
	preview = socialRequest(s, "/api/social/preview", body)
	var history struct {
		Posts []struct {
			Publication store.Publication `json:"publication"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Posts) != 2 {
		t.Fatal(preview.Body.String())
	}
	for _, post := range history.Posts {
		if post.Publication.State != "posted" {
			t.Fatal("missing posted state")
		}
	}
	if err := workspace.Append(timeline.Entry{ID: "new", Root: "p", ReplyTo: "a", Kind: timeline.KindReply, Speaker: "user", Text: "new reply", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	body.EntryIDs = []string{"p", "a", "new"}
	preview = socialRequest(s, "/api/social/preview", body)
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	body.Digest = plan.Digest
	added := socialRequest(s, "/api/social/publish", body)
	if added.Code != 200 || !strings.Contains(added.Body.String(), `"error":""`) || sent != 3 {
		t.Fatalf("new reply: sent=%d %s", sent, added.Body.String())
	}
	body.EntryIDs = []string{"p", "a"}
	preview = socialRequest(s, "/api/social/preview", body)
	json.Unmarshal(preview.Body.Bytes(), &plan)
	body.Digest = plan.Digest

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
	if result.Code != 200 || sent != 5 {
		t.Fatalf("account: %d %s", sent, result.Body.String())
	}
	entry, _ := workspace.Entry("a")
	entry.Text = "changed"
	workspace.Append(entry)
	result = socialRequest(s, "/api/social/publish", body)
	if result.Code != 409 || sent != 5 {
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
	s, w := app(t)
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

func TestAnswerOnlyPublishAndExtendSavedThread(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			s, workspace := socialApp(t)
			for i, entry := range []timeline.Entry{
				{ID: "p", Root: "p", Kind: timeline.KindPost, Speaker: "user", Text: "private original"},
				{ID: "a", Root: "p", ReplyTo: "p", Kind: timeline.KindRemark, Speaker: "tabe", Text: "answer"},
				{ID: "b", Root: "p", ReplyTo: "a", Kind: timeline.KindRemark, Speaker: "tabe", Text: "more"},
			} {
				entry.At = time.Now().Add(time.Duration(i) * time.Second)
				if err := workspace.Append(entry); err != nil {
					t.Fatal(err)
				}
			}
			rootURI := "at://did:plc:one/app.bsky.feed.post/1"
			if legacy {
				workspace.SavePublication("bluesky:did:plc:one:p", store.Publication{State: "posted", URI: "legacy-root", CID: "legacy-cid"})
				workspace.SavePublication("bluesky:did:plc:one:a", store.Publication{State: "posted", URI: rootURI, CID: "cid"})
			}
			sent := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Record map[string]any `json:"record"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				sent++
				if payload.Record["text"] == "private original" {
					t.Error("unselected original leaked")
				}
				reply, hasReply := payload.Record["reply"]
				if !legacy && sent == 1 {
					if hasReply {
						t.Error("answer-only first post must be standalone")
					}
				} else {
					wantRoot := rootURI
					if legacy {
						wantRoot = "legacy-root"
					}
					if !hasReply {
						t.Error("missing reply")
					} else {
						refs := reply.(map[string]any)
						if refs["root"].(map[string]any)["uri"] != wantRoot || refs["parent"].(map[string]any)["uri"] != rootURI {
							t.Errorf("wrong thread: %+v", refs)
						}
					}
				}
				fmt.Fprintf(w, `{"uri":"at://did:plc:one/app.bsky.feed.post/%d","cid":"cid"}`, sent)
			}))
			defer provider.Close()
			s.socialClient = func(context.Context) (*social.Client, error) {
				api := atclient.NewAPIClient(provider.URL)
				did := syntax.DID("did:plc:one")
				api.AccountDID = &did
				return social.NewClient(api), nil
			}
			selections := [][]string{{"a"}, {"a", "b"}, {"a", "b"}}
			for _, ids := range selections {
				body := socialBody{Root: "p", Target: "bluesky", EntryIDs: ids}
				preview := socialRequest(s, "/api/social/preview", body)
				var plan social.Plan
				if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &plan) != nil || plan.Root != "a" {
					t.Fatal(preview.Body.String())
				}
				body.Digest = plan.Digest
				result := socialRequest(s, "/api/social/publish", body)
				if result.Code != 200 || !strings.Contains(result.Body.String(), `"error":""`) {
					t.Fatal(result.Body.String())
				}
			}
			want := 2
			if legacy {
				want = 1
			}
			if sent != want {
				t.Fatalf("duplicate: sent=%d want=%d", sent, want)
			}
		})
	}
}
