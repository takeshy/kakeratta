package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	kserver "github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
)

func reviewApp(t *testing.T) (*kserver.Server, *store.Store, string, string) {
	t.Helper()
	w, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	app := kserver.New(kserver.Options{Store: w, Catalogue: persona.NewCatalogue(w), Alarm: func(time.Time) error { return nil }})
	t.Cleanup(app.Close)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, httptest.NewRequest("POST", "https://timeline.example/api/mcp/connection", strings.NewReader(`{}`)))
	var key struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	if json.Unmarshal(out.Body.Bytes(), &key) != nil || key.Key == "" {
		t.Fatal(out.Body.String())
	}
	return app, w, key.Path, key.Key
}

// This test helper is Hermes: only it holds the kakeratta credential. Bridge's
// HTTP transport is never given that destination or credential.
func relayReview(t *testing.T, app *kserver.Server, path, key string, item ReviewItem) ImportReceipt {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": item.Tool, "arguments": item.Arguments}})
	req := httptest.NewRequest("POST", "https://timeline.example"+path, strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	out := httptest.NewRecorder()
	app.ServeHTTP(out, req)
	var rpc struct {
		Result struct {
			Structured ImportReceipt `json:"structuredContent"`
			IsError    bool          `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal(out.Body.Bytes(), &rpc) != nil || rpc.Result.IsError || rpc.Result.Structured.EntryID == "" {
		t.Fatal(out.Code, out.Body.String())
	}
	return rpc.Result.Structured
}
func TestHermesRelaysRealKakerattaReviewWithLostReceiptAndDiscard(t *testing.T) {
	app, w, path, key := reviewApp(t)
	s, p := importSetup(t)
	ctx := context.Background()
	enableInbox(t, s)
	p.history = `[]`
	base := s.http.Transport
	calls := 0
	s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "timeline.example" {
			t.Fatal("Bridge contacted kakeratta directly")
		}
		calls++
		return base.RoundTrip(r)
	})
	if _, e := s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	view, _ := s.Inbox(ctx)
	if len(view.Messages) != 1 || view.Messages[0].TimelineStatus != "" {
		t.Fatal(view)
	}
	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest("GET", "/api/timeline", nil))
	var timeline store.TimelinePage
	_ = json.Unmarshal(page.Body.Bytes(), &timeline)
	if len(timeline.Entries) != 0 {
		t.Fatal("sync published without Hermes")
	}
	batch, e := s.ReviewContext(ctx, "gmail")
	if e != nil || len(batch.Items) != 1 {
		t.Fatal(batch, e)
	}
	before := calls
	first := relayReview(t, app, path, key, batch.Items[0]) // Simulate a lost response to Hermes.
	again := relayReview(t, app, path, key, batch.Items[0])
	if first.EntryID != again.EntryID || again.Status != "pending" {
		t.Fatal(first, again)
	}
	input := ReviewReceiptInput{Service: "gmail", Token: batch.Items[0].Token, Receipt: again}
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal(e)
	}
	entries, _ := w.Timeline()
	if len(entries) != 0 {
		t.Fatal("pending candidate reached normal posts")
	}
	out := httptest.NewRecorder()
	app.ServeHTTP(out, httptest.NewRequest("POST", "/api/imports/"+again.EntryID+"/decision", strings.NewReader(`{"accept":false,"excludeSimilar":true,"excludeSender":true}`)))
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	batch, e = s.ReviewContext(ctx, "gmail")
	if e != nil || len(batch.Items) != 1 {
		t.Fatal(batch, e)
	}
	input.Token = batch.Items[0].Token
	input.Receipt = relayReview(t, app, path, key, batch.Items[0])
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal(e)
	}
	view, e = s.Inbox(ctx)
	if e != nil || len(view.Messages) != 0 || len(view.Settings.ExcludedTypes) != 1 || !has(view.Settings.ExcludedSenders, "sender@example.com") {
		t.Fatal(view, e)
	}
	if calls != before {
		t.Fatal("relay or receipts made outbound calls")
	}
	cfg := view.Settings
	cfg.ExcludedSenders = nil
	cfg.ExcludedTypes = nil
	if _, e = s.SetInboxSettings(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal("lost acknowledgement not retryable", e)
	}
	view, _ = s.Inbox(ctx)
	if len(view.Settings.ExcludedTypes) != 0 || len(view.Settings.ExcludedSenders) != 0 {
		t.Fatal("replay restored cancelled exclusion")
	}
	// A stale pending response cannot revive a discarded candidate.
	input.Receipt = again
	if _, e = s.ReviewReceipt(ctx, input); e != nil {
		t.Fatal(e)
	}
	batch, _ = s.ReviewContext(ctx, "gmail")
	if len(batch.Items) != 0 {
		t.Fatal("pending receipt resurrected mail")
	}
}

func TestRelayRejectsStaleTokensAndUnrelatedFeedback(t *testing.T) {
	s, _ := importSetup(t)
	enableInbox(t, s)
	ctx := context.Background()
	_, _ = s.SyncGmail(ctx)
	batch, e := s.ReviewContext(ctx, "gmail")
	if e != nil || len(batch.Items) != 1 {
		t.Fatal(batch, e)
	}
	in := ReviewReceiptInput{Service: "gmail", Token: strings.Repeat("x", 64), Receipt: ImportReceipt{Status: "pending", EntryID: "sample-entry"}}
	if _, e = s.ReviewReceipt(ctx, in); e != ErrReviewChanged {
		t.Fatal(e)
	}
	in.Token = batch.Items[0].Token
	mail := batch.Items[0].Arguments["mail"].(map[string]any)
	in.Receipt = ImportReceipt{Status: "rejected", EntryID: reviewEntryID("gmail", mail["connectionId"].(string), mail["messageId"].(string), ""), ExcludedSender: "other@example.com"}
	if _, e = s.ReviewReceipt(ctx, in); e != ErrInput {
		t.Fatal(e)
	}
	view, _ := s.Inbox(ctx)
	if len(view.Settings.ExcludedSenders) != 0 || len(view.Messages) != 1 {
		t.Fatal(view)
	}
	if e = s.ClearInbox(ctx); e != nil {
		t.Fatal(e)
	}
	in.Receipt = ImportReceipt{Status: "pending", EntryID: "sample-entry"}
	if _, e = s.ReviewReceipt(ctx, in); e == nil {
		t.Fatal("cleared cache accepted stale receipt")
	}
}
func TestRelayPollsAllPendingCardsAndPrioritizesNew(t *testing.T) {
	newItems := []ReviewItem{{Token: "new"}}
	pending := []ReviewItem{}
	for _, x := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		pending = append(pending, ReviewItem{Token: x})
	}
	cursor := 0
	seen := map[string]bool{}
	for n := 0; n < 2; n++ {
		for _, x := range relayBatch(newItems, pending, &cursor) {
			seen[x.Token] = true
		}
	}
	if len(seen) != 8 {
		t.Fatal("pending cards starved", seen)
	}
	if got := relayBatch(newItems, pending, &cursor)[0].Token; got != "new" {
		t.Fatal("new candidates not prioritized", got)
	}
}
func TestLegacyTimelineCredentialIsDeletedAtStartup(t *testing.T) {
	s, _ := importSetup(t)
	ctx := context.Background()
	// Opaque legacy ciphertext, intentionally not decryptable with current keys.
	v, _ := s.w.SocialValue(ctx, s.storageKey("timeline-target"))
	if _, e := s.w.SwapSocialValue(ctx, s.storageKey("timeline-target"), v.Version, store.SocialValue{Data: []byte("old encrypted kakeratta credential")}); e != nil {
		t.Fatal(e)
	}
	if _, e := New(s.w, s.cfg); e != nil {
		t.Fatal(e)
	}
	v, e := s.w.SocialValue(ctx, s.storageKey("timeline-target"))
	if e != nil || len(v.Data) != 0 {
		t.Fatal("legacy credential retained", e)
	}
}
func TestTimelineDeliveryPrefersTriageSummary(t *testing.T) {
	m := InboxMessage{Body: "message\n-- signature", Summary: "Reservation confirmed."}
	if got := timelineMailBody(m); got != m.Summary || strings.Contains(got, "signature") {
		t.Fatal(got)
	}
	m.Summary = ""
	if got := timelineMailBody(m); got != m.Body {
		t.Fatal(got)
	}
}
