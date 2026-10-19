package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/takeshy/kakeratta/internal/store"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writable(t *testing.T) (*Service, *atomic.Int32, *string) {
	t.Helper()
	s, p := setup(t)
	p.scope += " " + scopeBase + "gmail.send " + scopeBase + "gmail.compose " + scopeBase + "calendar.events " + scopeBase + "calendar.calendarlist.readonly"
	connect(t, s, "gmail")
	connect(t, s, "calendar")
	count := &atomic.Int32{}
	etag := new(string)
	*etag = `"v1"`
	s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/calendarList/") {
			return response(200, `{"id":"owner@example.com","summary":"Owner calendar","timeZone":"Asia/Tokyo","accessRole":"owner"}`), nil
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/events/e1") {
			return response(200, `{"id":"e1","etag":`+string(mustJSON(*etag))+`,"summary":"before","status":"confirmed","start":{"date":"2026-10-19"},"end":{"date":"2026-10-20"},"attendees":[{"email":"old@example.com"}],"conferenceData":{"entryPoints":[{"uri":"https://meet.google.com/example"}]}}`), nil
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages/m1") {
			return response(200, `{"id":"m1","threadId":"t1","payload":{"headers":[{"name":"Subject","value":"hello"},{"name":"Message-ID","value":"<original@example.com>"}]}}`), nil
		}
		if r.Method != "GET" && r.URL.Host != "oauth2.googleapis.com" {
			count.Add(1)
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Error("authorization missing")
			}
			if r.Method == "PATCH" || r.Method == "DELETE" {
				if r.Header.Get("If-Match") != *etag {
					t.Error("missing version guard")
				}
			}
			if r.Method == "DELETE" {
				return response(204, ""), nil
			}
			return response(200, `{"id":"result1"}`), nil
		}
		return p.request(r)
	})
	return s, count, etag
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func mailProposal(key string) ProposalInput {
	return ProposalInput{Type: "gmail_send", IdempotencyKey: key, Mail: &MailInput{To: []string{"recipient@example.com"}, Subject: "hello", Body: "private outgoing body"}}
}
func approve(t *testing.T, s *Service, in ProposalInput) Action {
	t.Helper()
	a, e := s.Propose(context.Background(), in, "mcp")
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.Decide(context.Background(), a.ID, a.PayloadHash, true)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestActionApprovalExactOnceAndMetadata(t *testing.T) {
	s, count, _ := writable(t)
	ctx := context.Background()
	in := mailProposal("operation-key-0001")
	a, e := s.Propose(ctx, in, "mcp")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(ctx, a.ID, a.PayloadHash, in.IdempotencyKey, "mcp"); !errors.Is(e, ErrApproval) {
		t.Fatal("unapproved", e)
	}
	if _, e = s.Decide(ctx, a.ID, "changed", true); !errors.Is(e, ErrApproval) {
		t.Fatal("changed", e)
	}
	if _, e = s.Decide(ctx, a.ID, a.PayloadHash, true); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ hash, key string }{{"changed", in.IdempotencyKey}, {a.PayloadHash, "different-operation-key"}} {
		if _, e = s.Execute(ctx, a.ID, v.hash, v.key, "mcp"); !errors.Is(e, ErrApproval) {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Execute(ctx, a.ID, a.PayloadHash, in.IdempotencyKey, "mcp")
			if e != nil || r.Status != "succeeded" || r.Payload != nil {
				t.Errorf("receipt %+v %v", r, e)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("duplicate writes", count.Load())
	}
	rows, e := s.Actions(ctx)
	if e != nil || len(rows) != 1 || rows[0].DecidedBy != "owner" || rows[0].ExecutedBy != "mcp" || rows[0].ConsumedAt == nil || rows[0].FinishedAt == nil {
		t.Fatalf("audit: %+v %v", rows, e)
	}
	same, e := s.Propose(ctx, in, "mcp")
	if e != nil || same.ID != a.ID || same.Status != "succeeded" {
		t.Fatal(same, e)
	}
	in.Mail.Body = "modified"
	if _, e = s.Propose(ctx, in, "mcp"); !errors.Is(e, ErrApproval) {
		t.Fatal("key reused", e)
	}
	v, _ := s.w.SocialValue(ctx, s.storageKey("actions-gmail"))
	if strings.Contains(string(v.Data), "private outgoing body") || strings.Contains(string(v.Data), "recipient@example.com") {
		t.Fatal("plaintext ledger")
	}
}
func TestActionExpiryReconnectionRejectionAndDisconnect(t *testing.T) {
	for _, scenario := range []string{"expiry", "reconnect", "disconnect", "reject", "crash"} {
		t.Run(scenario, func(t *testing.T) {
			s, count, _ := writable(t)
			ctx := context.Background()
			in := mailProposal("operation-key-0002")
			a := approve(t, s, in)
			switch scenario {
			case "expiry":
				j, _ := s.ledger(ctx, "gmail")
				j.Ledger.Actions[0].ExpiresAt = time.Now().Add(-time.Second)
				_ = s.save(ctx, "actions-gmail", j)
			case "reconnect":
				connect(t, s, "gmail")
			case "disconnect":
				if _, e := s.Disconnect(ctx, "gmail", false); e != nil {
					t.Fatal(e)
				}
			case "reject": // An approved operation cannot be edited or re-approved.
				if _, e := s.Decide(ctx, a.ID, a.PayloadHash, false); !errors.Is(e, ErrApproval) {
					t.Fatal(e)
				}
				j, _ := s.ledger(ctx, "gmail")
				terminal(&j.Ledger.Actions[0], "rejected")
				_ = s.save(ctx, "actions-gmail", j)
			case "crash":
				j, _ := s.ledger(ctx, "gmail")
				j.Ledger.Actions[0].Status = "executing"
				now := time.Now()
				j.Ledger.Actions[0].ConsumedAt = &now
				_ = s.save(ctx, "actions-gmail", j)
			}
			r, e := s.Execute(ctx, a.ID, a.PayloadHash, in.IdempotencyKey, "mcp")
			if scenario == "crash" {
				if e != nil || r.Status != "unknown" {
					t.Fatal(r, e)
				}
			} else if !errors.Is(e, ErrApproval) {
				t.Fatal("stale approval", e)
			}
			if count.Load() != 0 {
				t.Fatal("unexpected write")
			}
			rows, e := s.Actions(ctx)
			if e != nil || rows[0].Payload != nil {
				t.Fatal("body retained", e)
			}
		})
	}
}
func TestWriteUncertainOutcomeNeverRetries(t *testing.T) {
	for _, status := range []int{0, 401, 403, 429, 500, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, count, _ := writable(t)
			base := s.http.Transport
			s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/messages/send") {
					count.Add(1)
					if status == 0 {
						return nil, errors.New("access-secret timeout")
					}
					return response(status, `{"error":"body must not escape"}`), nil
				}
				return base.RoundTrip(r)
			})
			in := mailProposal("operation-key-0003")
			a := approve(t, s, in)
			for range 2 {
				r, e := s.Execute(context.Background(), a.ID, a.PayloadHash, in.IdempotencyKey, "mcp")
				want := "unknown"
				if status == 401 || status == 403 {
					want = "failed"
				}
				if e != nil || r.Status != want || r.Payload != nil {
					t.Fatal(r, e)
				}
			}
			if count.Load() != 1 {
				t.Fatal(count.Load())
			}
		})
	}
}
func calendarProposal(typ, key string) ProposalInput {
	in := ProposalInput{Type: typ, IdempotencyKey: key, CalendarID: "primary", SendUpdates: "all"}
	if typ != "calendar_create" {
		in.EventID = "e1"
	}
	if typ != "calendar_delete" {
		in.Event = &EventInput{Summary: "after", Description: "review me", Start: eventTime{DateTime: "2026-10-19T10:00:00+09:00", TimeZone: "Asia/Tokyo"}, End: eventTime{DateTime: "2026-10-19T11:00:00+09:00", TimeZone: "Asia/Tokyo"}, Attendees: []attendee{{Email: "guest@example.com"}}}
	}
	return in
}
func TestCalendarWritesAndPreconditions(t *testing.T) {
	for _, typ := range []string{"calendar_create", "calendar_update", "calendar_delete"} {
		t.Run(typ, func(t *testing.T) {
			s, count, _ := writable(t)
			in := calendarProposal(typ, "calendar-operation-key")
			in.CreateMeet = typ == "calendar_create"
			a := approve(t, s, in)
			base := s.http.Transport
			s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					if r.URL.Query().Get("sendUpdates") != "all" {
						t.Error("notification scope")
					}
					data, _ := io.ReadAll(r.Body)
					if typ == "calendar_create" && (!strings.Contains(string(data), a.ID) || r.URL.Query().Get("conferenceDataVersion") != "1") {
						t.Error("Meet/id not frozen")
					}
					r.Body = io.NopCloser(strings.NewReader(string(data)))
				}
				return base.RoundTrip(r)
			})
			r, e := s.Execute(context.Background(), a.ID, a.PayloadHash, in.IdempotencyKey, "mcp")
			if e != nil || r.Status != "succeeded" || count.Load() != 1 {
				t.Fatal(r, e, count.Load())
			}
		})
	}
	s, count, etag := writable(t)
	in := calendarProposal("calendar_update", "changed-calendar-key")
	a := approve(t, s, in)
	*etag = `"v2"`
	if _, e := s.Execute(context.Background(), a.ID, a.PayloadHash, in.IdempotencyKey, "mcp"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if count.Load() != 0 {
		t.Fatal("overwrote stale event")
	}
}
func TestCalendarRejectsDelegatedRecurringAndInvalidInputs(t *testing.T) {
	for _, bad := range []string{"delegated", "recurring", "special", "omitted"} {
		t.Run(bad, func(t *testing.T) {
			s, _, _ := writable(t)
			base := s.http.Transport
			s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if bad == "delegated" && strings.Contains(r.URL.Path, "/calendarList/") {
					return response(200, `{"id":"shared","accessRole":"writer"}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/events/e1") {
					extra := map[string]string{"recurring": `,"recurrence":["RRULE:FREQ=DAILY"]`, "special": `,"eventType":"outOfOffice"`, "omitted": `,"attendeesOmitted":true`}[bad]
					return response(200, `{"id":"e1","etag":"v1"`+extra+`}`), nil
				}
				return base.RoundTrip(r)
			})
			if _, e := s.Propose(context.Background(), calendarProposal("calendar_delete", "invalid-calendar-key"), "mcp"); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
		})
	}
	s, _, _ := writable(t)
	for _, change := range []func(*ProposalInput){func(i *ProposalInput) { i.Event.Start.TimeZone = "Not/AZone" }, func(i *ProposalInput) { i.Event.Start.DateTime = "2026-10-19T10:00:00Z" }, func(i *ProposalInput) { i.Event.End = i.Event.Start }, func(i *ProposalInput) { i.SendUpdates = "" }, func(i *ProposalInput) { i.Event.Attendees[0].Email = "a@example.com\r\nBcc:b@example.com" }} {
		in := calendarProposal("calendar_create", "invalid-event-key")
		change(&in)
		if _, e := s.Propose(context.Background(), in, "mcp"); !errors.Is(e, ErrInput) {
			t.Fatal(e)
		}
	}
}
func TestGmailMIMEReplyAndDraft(t *testing.T) {
	for _, typ := range []string{"gmail_send", "gmail_draft"} {
		t.Run(typ, func(t *testing.T) {
			s, count, _ := writable(t)
			in := mailProposal("reply-operation-key")
			in.Type = typ
			in.Mail.Subject = "Re: hello"
			in.Mail.Body = "日本語\n本文"
			in.Mail.ReplyToMessageID = "m1"
			a := approve(t, s, in)
			base := s.http.Transport
			s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					var v map[string]any
					_ = json.NewDecoder(r.Body).Decode(&v)
					msg := v
					if typ == "gmail_draft" {
						msg = v["message"].(map[string]any)
					}
					raw, _ := base64.RawURLEncoding.DecodeString(msg["raw"].(string))
					for _, part := range []string{"From: owner@example.com\r\n", "In-Reply-To: <original@example.com>", "References: <original@example.com>", "Content-Transfer-Encoding: base64"} {
						if !strings.Contains(string(raw), part) {
							t.Errorf("missing %q", part)
						}
					}
					if msg["threadId"] != "t1" {
						t.Error("reply thread changed")
					}
				}
				return base.RoundTrip(r)
			})
			r, e := s.Execute(context.Background(), a.ID, a.PayloadHash, "", "owner")
			if e != nil || r.Status != "succeeded" || count.Load() != 1 {
				t.Fatal(r, e)
			}
		})
	}
	s, _, _ := writable(t)
	for _, change := range []func(*ProposalInput){func(i *ProposalInput) { i.Mail.To = []string{"x@example.com\r\nBcc:y@example.com"} }, func(i *ProposalInput) { i.Mail.Subject = "hello\r\nBcc:y@example.com" }, func(i *ProposalInput) { i.Mail.Body = strings.Repeat("a", 16001) }, func(i *ProposalInput) { i.Mail.ReplyToMessageID = "../bad" }, func(i *ProposalInput) { i.Mail.ReplyToMessageID = "m1"; i.Mail.Subject = "changed thread subject" }} {
		in := mailProposal("invalid-mail-key")
		change(&in)
		if _, e := s.Propose(context.Background(), in, "mcp"); !errors.Is(e, ErrInput) {
			t.Fatal(e)
		}
	}
}
func TestActionScopeIsolationAndRejectedDecision(t *testing.T) {
	s, _ := setup(t)
	connect(t, s, "gmail")
	if _, e := s.Propose(context.Background(), mailProposal("readonly-scope-key"), "mcp"); !errors.Is(e, ErrScope) {
		t.Fatal(e)
	}
	s, _, _ = writable(t)
	in := mailProposal("rejected-operation")
	a, e := s.Propose(context.Background(), in, "mcp")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Decide(context.Background(), a.ID, a.PayloadHash, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(context.Background(), a.ID, a.PayloadHash, in.IdempotencyKey, "mcp"); !errors.Is(e, ErrApproval) {
		t.Fatal(e)
	}
	other, _, _ := writable(t)
	if _, e = other.Decide(context.Background(), a.ID, a.PayloadHash, true); !errors.Is(e, ErrApproval) {
		t.Fatal("cross owner approval", e)
	}
}

type failingCheckpoint struct {
	store.Workspace
	fail bool
}

func (w *failingCheckpoint) SwapSocialValue(ctx context.Context, key, version string, next store.SocialValue) (bool, error) {
	if w.fail && key == "google-v1:actions-gmail" {
		return false, errors.New("disk failure")
	}
	return w.Workspace.SwapSocialValue(ctx, key, version, next)
}
func TestWriteRequiresSuccessfulConsumedCheckpoint(t *testing.T) {
	s, count, _ := writable(t)
	in := mailProposal("disk-failure-operation")
	a := approve(t, s, in)
	w := &failingCheckpoint{Workspace: s.w, fail: true}
	s.w = w
	if _, e := s.Execute(context.Background(), a.ID, a.PayloadHash, in.IdempotencyKey, "mcp"); !errors.Is(e, ErrStorage) {
		t.Fatal(e)
	}
	if count.Load() != 0 {
		t.Fatal("write before checkpoint")
	}
}
func TestActionPreflightUnauthorizedRequiresReconnect(t *testing.T) {
	s, count, _ := writable(t)
	base := s.http.Transport
	s.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/calendarList/") {
			return response(401, `{"error":"access-secret"}`), nil
		}
		return base.RoundTrip(r)
	})
	if _, e := s.Propose(context.Background(), calendarProposal("calendar_create", "preflight-unauthorized"), "mcp"); !errors.Is(e, ErrReauth) {
		t.Fatal(e)
	}
	rows, e := s.Status(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range rows {
		if r.Service == "calendar" && r.Status != "needs_reauth" {
			t.Fatal(r)
		}
	}
	if count.Load() != 0 {
		t.Fatal("unexpected mutation")
	}
}
