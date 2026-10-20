package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	kserver "github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
)

func TestBridgeDeliversRealKakerattaReviewAndRespectsDiscard(t *testing.T) {
	w, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	app := kserver.New(kserver.Options{Store: w, Catalogue: persona.NewCatalogue(w), Alarm: func(time.Time) error { return nil }})
	t.Cleanup(app.Close)
	request := httptest.NewRequest("POST", "https://timeline.example/api/mcp/connection", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, request)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var credential struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &credential)
	s, p := importSetup(t)
	ctx := context.Background()
	enableInbox(t, s)
	p.history = `[]`
	base := s.http.Transport
	deliveries := 0
	loseResponse := true
	s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "timeline.example" {
			deliveries++
			out := httptest.NewRecorder()
			app.ServeHTTP(out, r)
			if loseResponse {
				loseResponse = false
				return nil, io.ErrUnexpectedEOF
			}
			return out.Result(), nil
		}
		return base.RoundTrip(r)
	})
	target := TimelineTargetInput{Enabled: true, URL: "https://timeline.example" + credential.Path, Key: credential.Key}
	if _, e = s.SetTimelineTarget(ctx, target); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SyncGmail(ctx); e != ErrTimeline {
		t.Fatal(e)
	} // Delivery succeeded, receipt was lost.
	entries, _ := w.Timeline()
	if len(entries) != 0 {
		t.Fatal("pending mail reached real timeline store")
	}
	if _, e = s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	view, e := s.Inbox(ctx)
	if e != nil || len(view.Messages) != 1 || view.Messages[0].TimelineStatus != "pending" {
		t.Fatal(view, e)
	}
	pageReq := httptest.NewRequest("GET", "/api/timeline", nil)
	out := httptest.NewRecorder()
	app.ServeHTTP(out, pageReq)
	var page store.TimelinePage
	_ = json.Unmarshal(out.Body.Bytes(), &page)
	if len(page.Entries) != 1 || page.Entries[0].Import.Status != "pending" {
		t.Fatal(out.Body.String())
	}
	id := view.Messages[0].TimelineEntryID
	delivered := view.Messages[0]
	decision := httptest.NewRequest("POST", "/api/imports/"+id+"/decision", strings.NewReader(`{"accept":false,"excludeSimilar":true,"excludeSender":true}`))
	out = httptest.NewRecorder()
	app.ServeHTTP(out, decision)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if _, e = s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	view, e = s.Inbox(ctx)
	if e != nil || len(view.Messages) != 0 {
		t.Fatal("rejected mail retained", view, e)
	}
	if len(view.Settings.ExcludedTypes) != 1 {
		t.Fatal("discard did not learn a mail type", view.Settings)
	}
	if !has(view.Settings.ExcludedSenders, "sender@example.com") {
		t.Fatal("discard did not learn the canonical From", view.Settings)
	}
	if deliveries != 3 {
		t.Fatal("delivery retries", deliveries)
	}
	// Cancelling rules must survive a replay of the receiver's old receipt.
	settings := view.Settings
	settings.ExcludedSenders = nil
	settings.ExcludedTypes = nil
	if _, e = s.SetInboxSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	r, e := s.read(ctx, "gmail-inbox")
	if e != nil {
		t.Fatal(e)
	}
	r.Sync.Messages = []InboxMessage{delivered}
	if e = s.publishInbox(ctx, r.Sync); e != nil {
		t.Fatal(e)
	}
	if len(r.Sync.Settings.ExcludedSenders) != 0 || len(r.Sync.Settings.ExcludedTypes) != 0 {
		t.Fatal("replayed receipt restored cancelled rules", r.Sync.Settings)
	}
	status, e := s.TimelineTarget(ctx)
	raw, _ := json.Marshal(status)
	if e != nil || strings.Contains(string(raw), credential.Key) {
		t.Fatal("target secret escaped", e)
	}
	saved, _ := s.w.SocialValue(ctx, s.storageKey("timeline-target"))
	if strings.Contains(string(saved.Data), credential.Key) {
		t.Fatal("unencrypted target key")
	}
	if e = s.ClearTimelineTarget(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = s.TimelineTarget(ctx)
	if e != nil || status.Configured || status.Enabled {
		t.Fatal(status, e)
	}
}
func TestTargetRequiresOwnerConfiguredHTTPSAndValidKey(t *testing.T) {
	s, _ := importSetup(t)
	for _, u := range []string{"http://remote.example/api/mcp/users/u", "https://user:pass@x/api/mcp/users/u", "https://x/other", "https://x/api/mcp/users/u?secret=1"} {
		if _, e := s.SetTimelineTarget(context.Background(), TimelineTargetInput{Enabled: true, URL: u, Key: strings.Repeat("a", 64)}); e != ErrInput {
			t.Fatal(u, e)
		}
	}
}

func TestTimelineDeliveryPrefersTriageSummary(t *testing.T) {
	m := InboxMessage{Body: "message\n\n-- \nsignature", Summary: "Reservation confirmed for October 20."}
	if got := timelineMailBody(m); got != m.Summary || strings.Contains(got, "signature") {
		t.Fatal(got)
	}
	m.Summary = ""
	if got := timelineMailBody(m); got != m.Body {
		t.Fatal("local classification lost its body", got)
	}
}
