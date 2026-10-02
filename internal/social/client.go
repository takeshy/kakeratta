package social

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/takeshy/kakeratta/internal/store"
)

type rejected struct{ status int }

func (e rejected) Error() string {
	return fmt.Sprintf("Bluesky APIがエラーを返しました（HTTP %d）", e.status)
}

type Client struct{ API *atclient.APIClient }

func NewClient(api *atclient.APIClient) *Client { return &Client{API: api} }
func (c *Client) Account() string {
	if c.API.AccountDID == nil {
		return ""
	}
	return c.API.AccountDID.String()
}
func (c *Client) request(ctx context.Context, endpoint, mime string, data []byte, result any) error {
	req := atclient.NewAPIRequest(http.MethodPost, syntax.NSID(endpoint), bytes.NewReader(data))
	req.Headers.Set("Content-Type", mime)
	resp, err := c.API.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("Blueskyへの接続が中断されました")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return rejected{resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result); err != nil {
		return fmt.Errorf("Blueskyの応答を読み取れませんでした")
	}
	return nil
}
func (c *Client) Publish(ctx context.Context, post Post, root, parent store.Publication, data []byte, mime string) (store.Publication, error) {
	record := map[string]any{"$type": "app.bsky.feed.post", "text": post.Text, "createdAt": time.Now().UTC().Format(time.RFC3339Nano)}
	if post.Parent != "" {
		record["reply"] = map[string]any{"root": map[string]string{"uri": root.URI, "cid": root.CID}, "parent": map[string]string{"uri": parent.URI, "cid": parent.CID}}
	}
	if len(data) > 0 {
		var uploaded struct {
			Blob json.RawMessage `json:"blob"`
		}
		if err := c.request(ctx, "com.atproto.repo.uploadBlob", mime, data, &uploaded); err != nil {
			return store.Publication{State: "retryable"}, err
		}
		if len(uploaded.Blob) == 0 {
			return store.Publication{State: "retryable"}, fmt.Errorf("画像のアップロード応答が不正です")
		}
		record["embed"] = map[string]any{"$type": "app.bsky.embed.images", "images": []any{map[string]any{"alt": post.ImageAlt, "image": uploaded.Blob}}}
	}
	raw, err := json.Marshal(map[string]any{"repo": c.Account(), "collection": "app.bsky.feed.post", "record": record})
	if err != nil {
		return store.Publication{State: "retryable"}, err
	}
	var result store.Publication
	err = c.request(ctx, "com.atproto.repo.createRecord", "application/json", raw, &result)
	if err != nil {
		if rejection, ok := err.(rejected); ok && rejection.status >= 400 && rejection.status < 500 && rejection.status != 408 {
			result.State = "retryable"
		}
		return result, err
	}
	if result.URI == "" || result.CID == "" {
		return result, fmt.Errorf("投稿の識別情報を受け取れませんでした")
	}
	result.State = "posted"
	result.URL = "https://bsky.app/profile/" + c.Account() + "/post/" + result.URI[strings.LastIndex(result.URI, "/")+1:]
	return result, nil
}
