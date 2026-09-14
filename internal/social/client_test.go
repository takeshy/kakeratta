package social

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(url string) *Client {
	api := atclient.NewAPIClient(url)
	did := syntax.DID("did:plc:test")
	api.AccountDID = &did
	return NewClient(api)
}
func TestBlueskyUploadsImageAndPreservesRootAndParent(t *testing.T) {
	var record map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.repo.uploadBlob":
			if r.Header.Get("Content-Type") != "image/png" {
				t.Error("missing blob headers")
			}
			fmt.Fprint(w, `{"blob":{"$type":"blob","ref":{"$link":"image-cid"},"mimeType":"image/png","size":4}}`)
		case "/xrpc/com.atproto.repo.createRecord":
			var body struct {
				Record map[string]any `json:"record"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			record = body.Record
			fmt.Fprint(w, `{"uri":"at://did:plc:test/app.bsky.feed.post/abc","cid":"cid"}`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	result, err := testClient(srv.URL).Publish(context.Background(), Post{Text: "reply", Parent: "a"}, store.Publication{URI: "root", CID: "r"}, store.Publication{URI: "parent", CID: "p"}, []byte("test"), "image/png")
	if err != nil || result.State != "posted" {
		t.Fatalf("%+v %v", result, err)
	}
	reply := record["reply"].(map[string]any)
	if reply["root"].(map[string]any)["uri"] != "root" || reply["parent"].(map[string]any)["uri"] != "parent" || record["embed"] == nil {
		t.Fatal(record)
	}
}
func TestProviderErrorsDoNotExposeSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); fmt.Fprint(w, `secret-access-token`) }))
	defer srv.Close()
	result, err := testClient(srv.URL).Publish(context.Background(), Post{Text: "hello"}, store.Publication{}, store.Publication{}, nil, "")
	if err == nil || strings.Contains(err.Error(), "secret") || result.State != "retryable" {
		t.Fatalf("%+v %v", result, err)
	}
}
