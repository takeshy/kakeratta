package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrApproval = errors.New("Approval missing, expired, changed or already consumed; review a new proposal")
var ErrConflict = errors.New("Google event changed or is outside the supported owner calendar operations; create a new proposal")
var ErrActionLimit = errors.New("Too many pending actions; reject or complete an existing proposal first")

// Inputs contain no authority. Only the browser-only Decide method can grant it.
type MailInput struct {
	To               []string `json:"to"`
	CC               []string `json:"cc,omitempty"`
	Subject          string   `json:"subject"`
	Body             string   `json:"body"`
	ReplyToMessageID string   `json:"replyToMessageId,omitempty"`
}
type EventInput struct {
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Location    string     `json:"location"`
	Start       eventTime  `json:"start"`
	End         eventTime  `json:"end"`
	Attendees   []attendee `json:"attendees"`
}
type attendee struct {
	Email string `json:"email"`
}
type ProposalInput struct {
	Type           string      `json:"type"`
	IdempotencyKey string      `json:"idempotencyKey"`
	Mail           *MailInput  `json:"mail,omitempty"`
	CalendarID     string      `json:"calendarId,omitempty"`
	EventID        string      `json:"eventId,omitempty"`
	Event          *EventInput `json:"event,omitempty"`
	SendUpdates    string      `json:"sendUpdates,omitempty"`
	CreateMeet     bool        `json:"createMeet,omitempty"`
}
type actionPayload struct {
	Mail             *MailInput  `json:"mail,omitempty"`
	From             string      `json:"from,omitempty"`
	ThreadID         string      `json:"threadId,omitempty"`
	InReplyTo        string      `json:"inReplyTo,omitempty"`
	References       string      `json:"references,omitempty"`
	CalendarID       string      `json:"calendarId,omitempty"`
	CalendarName     string      `json:"calendarName,omitempty"`
	CalendarTimeZone string      `json:"calendarTimeZone,omitempty"`
	EventID          string      `json:"eventId,omitempty"`
	Event            *EventInput `json:"event,omitempty"`
	Before           *event      `json:"before,omitempty"`
	SendUpdates      string      `json:"sendUpdates,omitempty"`
	CreateMeet       bool        `json:"createMeet,omitempty"`
}

// This encrypted journal is also the audit log. Terminal entries retain only
// identifiers, digests, actors, timestamps and outcomes, never bodies or tokens.
type Action struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	ConnectionID  string         `json:"connectionId"`
	Generation    string         `json:"generation"`
	KeyHash       string         `json:"keyHash"`
	InputHash     string         `json:"inputHash"`
	PayloadHash   string         `json:"payloadHash"`
	Status        string         `json:"status"`
	ProposedBy    string         `json:"proposedBy"`
	DecidedBy     string         `json:"decidedBy,omitempty"`
	DirectRequest string         `json:"directRequest,omitempty"`
	ExecutedBy    string         `json:"executedBy,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	ExpiresAt     time.Time      `json:"expiresAt"`
	DecidedAt     *time.Time     `json:"decidedAt,omitempty"`
	ConsumedAt    *time.Time     `json:"consumedAt,omitempty"`
	FinishedAt    *time.Time     `json:"finishedAt,omitempty"`
	ProviderID    string         `json:"providerId,omitempty"`
	Payload       *actionPayload `json:"payload,omitempty"`
}
type actionLedger struct{ Actions []Action }

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func serviceForAction(t string) string {
	switch t {
	case "gmail_send", "gmail_draft":
		return "gmail"
	case "calendar_create", "calendar_update", "calendar_delete":
		return "calendar"
	}
	return ""
}
func writePermission(c *connection, t string) bool {
	switch t {
	case "gmail_send":
		return has(c.Scopes, scopeBase+"gmail.send") || has(c.Scopes, scopeBase+"gmail.compose")
	case "gmail_draft":
		return has(c.Scopes, scopeBase+"gmail.compose")
	default:
		return has(c.Scopes, scopeBase+"calendar.events") && has(c.Scopes, scopeBase+"calendar.calendarlist.readonly")
	}
}
func (s *Service) ledger(ctx context.Context, name string) (record, error) {
	r, e := s.read(ctx, "actions-"+name)
	if r.Ledger == nil {
		r.Ledger = &actionLedger{Actions: []Action{}}
	}
	return r, e
}
func terminal(a *Action, status string) {
	now := time.Now().UTC()
	a.Status = status
	a.Payload = nil
	a.FinishedAt = &now
}
func (s *Service) cleanLedger(ctx context.Context, name string, r *record, c *connection) error {
	changed := false
	for i := range r.Ledger.Actions {
		a := &r.Ledger.Actions[i]
		if a.Status == "executing" {
			terminal(a, "unknown")
			changed = true
		} else if a.Payload != nil && (c == nil || a.Generation != c.Generation || a.ConnectionID != c.ID || !time.Now().Before(a.ExpiresAt)) {
			terminal(a, "expired")
			changed = true
		}
	}
	if changed {
		return s.save(ctx, "actions-"+name, *r)
	}
	return nil
}
func (s *Service) clearActionData(ctx context.Context, name string) error {
	r, e := s.ledger(ctx, name)
	if e != nil {
		return e
	}
	return s.cleanLedger(ctx, name, &r, nil)
}
func (s *Service) Actions(ctx context.Context) ([]Action, error) {
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return nil, e
	}
	defer unlock()
	out := []Action{}
	for _, name := range []string{"gmail", "calendar"} {
		r, e := s.ledger(ctx, name)
		if e != nil {
			return nil, e
		}
		c, e := s.read(ctx, name)
		if e != nil {
			return nil, e
		}
		if e = s.cleanLedger(ctx, name, &r, c.Connection); e != nil {
			return nil, e
		}
		// Also rewrap journal ciphertext with the current encryption key.
		if e = s.save(ctx, "actions-"+name, r); e != nil {
			return nil, e
		}
		out = append(out, r.Ledger.Actions...)
	}
	return out, nil
}
func validText(v string, n int) bool {
	return utf8.ValidString(v) && len(v) <= n && !strings.ContainsRune(v, 0)
}
func address(v string) (string, error) {
	if len(v) > 320 || strings.ContainsAny(v, "\r\n\x00") {
		return "", ErrInput
	}
	a, e := mail.ParseAddress(v)
	if e != nil || a.Name != "" || a.Address != v || !strings.Contains(v, "@") {
		return "", ErrInput
	}
	return a.Address, nil
}
func validateMail(m *MailInput) error {
	if m == nil || len(m.To) == 0 || len(m.To)+len(m.CC) > 20 || !validText(m.Subject, 900) || strings.ContainsAny(m.Subject, "\r\n") || !validText(m.Body, 16000) {
		return ErrInput
	}
	for _, v := range append(append([]string{}, m.To...), m.CC...) {
		if _, e := address(v); e != nil {
			return e
		}
	}
	return nil
}
func validateEvent(e *EventInput) error {
	if e == nil || strings.TrimSpace(e.Summary) == "" || !validText(e.Summary, 1000) || !validText(e.Description, 8000) || !validText(e.Location, 1000) || len(e.Attendees) > 20 {
		return ErrInput
	}
	if e.Attendees == nil {
		e.Attendees = []attendee{}
	}
	for _, a := range e.Attendees {
		if _, err := address(a.Email); err != nil {
			return err
		}
	}
	parse := func(v eventTime) (time.Time, error) {
		if v.Date != "" {
			if v.DateTime != "" || v.TimeZone != "" {
				return time.Time{}, ErrInput
			}
			return time.Parse("2006-01-02", v.Date)
		}
		loc, err := time.LoadLocation(v.TimeZone)
		if err != nil || v.TimeZone == "" || v.TimeZone == "Local" {
			return time.Time{}, ErrInput
		}
		t, err := time.Parse(time.RFC3339, v.DateTime)
		if err != nil {
			return t, ErrInput
		}
		_, a := t.Zone()
		_, b := t.In(loc).Zone()
		if a != b {
			return t, ErrInput
		}
		return t, nil
	}
	a, x := parse(e.Start)
	b, y := parse(e.End)
	if x != nil || y != nil || (e.Start.Date == "") != (e.End.Date == "") || e.Start.TimeZone != e.End.TimeZone || !b.After(a) || b.Sub(a) > 366*24*time.Hour {
		return ErrInput
	}
	return nil
}

type calendarInfo struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	TimeZone   string `json:"timeZone"`
	AccessRole string `json:"accessRole"`
}

func (s *Service) ownedCalendar(ctx context.Context, c *connection, id string) (calendarInfo, error) {
	var out calendarInfo
	if id == "" {
		id = "primary"
	}
	if len(id) > 320 || strings.ContainsAny(id, "/\\\r\n\x00") || id == "." || id == ".." {
		return out, ErrInput
	}
	e := s.actionGet(ctx, "calendar", c, "https://www.googleapis.com/calendar/v3/users/me/calendarList/"+url.PathEscape(id), &out)
	if e != nil {
		return out, e
	}
	if out.AccessRole != "owner" || out.ID == "" || len(out.ID) > 320 || strings.ContainsAny(out.ID, "/\\\r\n\x00") || out.ID == "." || out.ID == ".." {
		return out, ErrConflict
	}
	return out, nil
}
func eventURL(id, eventID string) string {
	v := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(id) + "/events"
	if eventID != "" {
		v += "/" + eventID
	}
	return v
}
func (s *Service) targetEvent(ctx context.Context, c *connection, cal, id string) (event, error) {
	var out event
	if !messageID.MatchString(id) {
		return out, ErrInput
	}
	e := s.actionGet(ctx, "calendar", c, eventURL(cal, id), &out)
	if e != nil {
		return out, e
	}
	if out.ID != id || out.ETag == "" || len(out.ETag) > 256 || strings.ContainsAny(out.ETag, "\r\n") || len(out.Recurrence) > 0 || out.Status == "cancelled" || out.EventType != "" && out.EventType != "default" || out.AttendeesOmitted {
		return out, ErrConflict
	}
	b, _ := json.Marshal(out)
	if len(b) > 24000 {
		return out, ErrConflict
	}
	return out, nil
}
func (s *Service) Propose(ctx context.Context, in ProposalInput, actor string) (Action, error) {
	name := serviceForAction(in.Type)
	if name == "" || len(in.IdempotencyKey) < 16 || len(in.IdempotencyKey) > 128 || !validText(in.IdempotencyKey, 128) || (actor != "mcp" && actor != "owner") {
		return Action{}, ErrInput
	}
	if name == "gmail" {
		if in.CalendarID != "" || in.EventID != "" || in.Event != nil || in.SendUpdates != "" || in.CreateMeet {
			return Action{}, ErrInput
		}
		if e := validateMail(in.Mail); e != nil {
			return Action{}, e
		}
	} else {
		if in.Mail != nil || in.SendUpdates != "none" && in.SendUpdates != "all" && in.SendUpdates != "externalOnly" || in.Type == "calendar_delete" && (in.Event != nil || in.CreateMeet) || in.Type != "calendar_create" && in.CreateMeet || in.Type == "calendar_create" && in.EventID != "" {
			return Action{}, ErrInput
		}
		if in.Type != "calendar_delete" {
			if e := validateEvent(in.Event); e != nil {
				return Action{}, e
			}
		}
	}
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return Action{}, e
	}
	defer unlock()
	r, e := s.read(ctx, name)
	if e != nil {
		return Action{}, e
	}
	if e = s.access(ctx, name, &r, false); e != nil {
		return Action{}, e
	}
	if !writePermission(r.Connection, in.Type) {
		return Action{}, ErrScope
	}
	journal, e := s.ledger(ctx, name)
	if e != nil {
		return Action{}, e
	}
	if e = s.cleanLedger(ctx, name, &journal, r.Connection); e != nil {
		return Action{}, e
	}
	kh, ih := digest(in.IdempotencyKey), digest(in)
	live := 0
	for _, a := range journal.Ledger.Actions {
		if a.KeyHash == kh {
			if a.InputHash != ih {
				return Action{}, ErrApproval
			}
			return a, nil
		}
		if a.Payload != nil {
			live++
		}
	}
	// Bound active payloads; retain compact audit tombstones so keys are never reused.
	if live >= 8 {
		return Action{}, ErrActionLimit
	}
	p := &actionPayload{Mail: in.Mail, Event: in.Event, EventID: in.EventID, SendUpdates: in.SendUpdates, CreateMeet: in.CreateMeet}
	if name == "gmail" {
		if _, e = address(r.Connection.Email); e != nil {
			return Action{}, ErrProvider
		}
		p.From = r.Connection.Email
		if in.Mail.ReplyToMessageID != "" {
			if !messageID.MatchString(in.Mail.ReplyToMessageID) {
				return Action{}, ErrInput
			}
			var m message
			if e = s.actionGet(ctx, "gmail", r.Connection, "https://gmail.googleapis.com/gmail/v1/users/me/messages/"+in.Mail.ReplyToMessageID+"?format=metadata", &m); e != nil {
				return Action{}, e
			}
			if !messageID.MatchString(m.ThreadID) || m.ID != in.Mail.ReplyToMessageID {
				return Action{}, ErrProvider
			}
			subject := ""
			for _, h := range m.Payload.Headers {
				switch strings.ToLower(h.Name) {
				case "message-id":
					p.InReplyTo = h.Value
				case "references":
					p.References = h.Value
				case "subject":
					subject = h.Value
				}
			}
			validRef := func(v string) bool {
				return len(v) <= 2000 && !strings.ContainsAny(v, "\r\n\x00") && strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">")
			}
			if !validRef(p.InReplyTo) || p.References != "" && !validRef(p.References) || in.Mail.Subject != subject && in.Mail.Subject != "Re: "+subject {
				return Action{}, ErrInput
			}
			p.ThreadID = m.ThreadID
		}
	} else {
		cal, err := s.ownedCalendar(ctx, r.Connection, in.CalendarID)
		if err != nil {
			return Action{}, err
		}
		p.CalendarID = cal.ID
		p.CalendarName = cal.Summary
		p.CalendarTimeZone = cal.TimeZone
		if in.Type != "calendar_create" {
			ev, err := s.targetEvent(ctx, r.Connection, cal.ID, in.EventID)
			if err != nil {
				return Action{}, err
			}
			p.Before = &ev
		}
	}
	if !writePermission(r.Connection, in.Type) {
		return Action{}, ErrScope
	}
	a := Action{ID: random(), Type: in.Type, ConnectionID: r.Connection.ID, Generation: r.Connection.Generation, KeyHash: kh, InputHash: ih, Status: "pending", ProposedBy: actor, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(5 * time.Minute).UTC(), Payload: p}
	if a.ID == "" {
		return Action{}, ErrStorage
	}
	a.PayloadHash = digest(struct {
		ID, Type, Connection, Generation string
		Payload                          *actionPayload
	}{a.ID, a.Type, a.ConnectionID, a.Generation, p})
	journal.Ledger.Actions = append(journal.Ledger.Actions, a)
	if e = s.save(ctx, "actions-"+name, journal); e != nil {
		return Action{}, e
	}
	return a, nil
}

// Decide is exposed ONLY through the management-cookie HTTP boundary. There is
// deliberately no corresponding MCP tool, nor can a payload be edited here.
func (s *Service) Decide(ctx context.Context, id, hash string, approve bool) (Action, error) {
	return s.changeAction(ctx, id, func(a *Action) error {
		if a.PayloadHash != hash || a.Status != "pending" {
			return ErrApproval
		}
		now := time.Now().UTC()
		a.DecidedBy = "owner"
		a.DecidedAt = &now
		if approve {
			a.Status = "approved"
		} else {
			terminal(a, "rejected")
		}
		return nil
	})
}

// Run executes an owner's direct request to Hermes without a Web approval
// step. The verbatim request is retained in the audit journal so the owner can
// review what Hermes acted on. It reuses the immutable proposal, idempotency
// and unknown-outcome handling of the approval path.
func (s *Service) Run(ctx context.Context, in ProposalInput, userRequest string) (Action, error) {
	userRequest = strings.TrimSpace(userRequest)
	if userRequest == "" || !validText(userRequest, 1000) {
		return Action{}, ErrInput
	}
	a, e := s.Propose(ctx, in, "mcp")
	if e != nil {
		return Action{}, e
	}
	if a.Status == "pending" {
		a, e = s.changeAction(ctx, a.ID, func(x *Action) error {
			if x.Status != "pending" {
				return nil
			}
			now := time.Now().UTC()
			x.DecidedBy = "direct"
			x.DecidedAt = &now
			x.DirectRequest = userRequest
			x.Status = "approved"
			return nil
		})
		if e != nil {
			return Action{}, e
		}
	}
	return s.Execute(ctx, a.ID, a.PayloadHash, in.IdempotencyKey, "mcp")
}
func (s *Service) changeAction(ctx context.Context, id string, fn func(*Action) error) (Action, error) {
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return Action{}, e
	}
	defer unlock()
	for _, name := range []string{"gmail", "calendar"} {
		j, e := s.ledger(ctx, name)
		if e != nil {
			return Action{}, e
		}
		r, e := s.read(ctx, name)
		if e != nil {
			return Action{}, e
		}
		if e = s.cleanLedger(ctx, name, &j, r.Connection); e != nil {
			return Action{}, e
		}
		for i := range j.Ledger.Actions {
			a := &j.Ledger.Actions[i]
			if a.ID != id {
				continue
			}
			if e = fn(a); e != nil {
				if errors.Is(e, ErrConflict) {
					terminal(a, "conflict")
					if saveErr := s.save(ctx, "actions-"+name, j); saveErr != nil {
						return Action{}, saveErr
					}
				}
				return Action{}, e
			}
			if e = s.save(ctx, "actions-"+name, j); e != nil {
				return Action{}, e
			}
			return *a, nil
		}
	}
	return Action{}, ErrApproval
}
func rawMail(a *Action) string {
	p := a.Payload
	m := p.Mail
	var b strings.Builder
	b.WriteString("From: " + p.From + "\r\nTo: " + strings.Join(m.To, ",\r\n ") + "\r\n")
	if len(m.CC) > 0 {
		b.WriteString("Cc: " + strings.Join(m.CC, ",\r\n ") + "\r\n")
	}
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\nDate: " + a.CreatedAt.Format(time.RFC1123Z) + "\r\nMessage-ID: <" + a.ID + "@google-bridge.invalid>\r\n")
	if p.InReplyTo != "" {
		refs := strings.TrimSpace(p.References + " " + p.InReplyTo)
		b.WriteString("In-Reply-To: " + p.InReplyTo + "\r\nReferences: " + refs + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	body := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")))
	for len(body) > 76 {
		b.WriteString(body[:76] + "\r\n")
		body = body[76:]
	}
	b.WriteString(body + "\r\n")
	return base64.RawURLEncoding.EncodeToString([]byte(b.String()))
}
func (s *Service) Execute(ctx context.Context, id, hash, key, actor string) (Action, error) {
	if actor != "mcp" && actor != "owner" {
		return Action{}, ErrInput
	}
	return s.changeAction(ctx, id, func(a *Action) error {
		if a.PayloadHash != hash || (actor != "owner" || key != "") && a.KeyHash != digest(key) {
			return ErrApproval
		}
		if a.ConsumedAt != nil {
			return nil
		}
		if a.Status != "approved" || a.Payload == nil || a.DecidedBy != "owner" && a.DecidedBy != "direct" {
			return ErrApproval
		}
		name := serviceForAction(a.Type)
		r, e := s.read(ctx, name)
		if e != nil {
			return e
		}
		if e = s.access(ctx, name, &r, false); e != nil {
			return e
		}
		if !writePermission(r.Connection, a.Type) {
			return ErrScope
		}
		if r.Connection.ID != a.ConnectionID || r.Connection.Generation != a.Generation {
			return ErrApproval
		}
		p := a.Payload
		method, endpoint := "POST", ""
		var body any
		if name == "gmail" {
			msg := map[string]any{"raw": rawMail(a)}
			if p.ThreadID != "" {
				msg["threadId"] = p.ThreadID
			}
			endpoint = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
			body = msg
			if a.Type == "gmail_draft" {
				endpoint = "https://gmail.googleapis.com/gmail/v1/users/me/drafts"
				body = map[string]any{"message": msg}
			}
		} else {
			cal, e := s.ownedCalendar(ctx, r.Connection, p.CalendarID)
			if e != nil {
				return e
			}
			if cal.ID != p.CalendarID {
				return ErrConflict
			}
			if p.Before != nil {
				ev, e := s.targetEvent(ctx, r.Connection, p.CalendarID, p.EventID)
				if e != nil {
					return e
				}
				if ev.ETag != p.Before.ETag {
					return ErrConflict
				}
			}
			endpoint = eventURL(p.CalendarID, p.EventID) + "?sendUpdates=" + url.QueryEscape(p.SendUpdates)
			if a.Type == "calendar_delete" {
				method = "DELETE"
			} else {
				data, _ := json.Marshal(p.Event)
				var fields map[string]any
				_ = json.Unmarshal(data, &fields)
				body = fields
				if a.Type == "calendar_update" {
					method = "PATCH"
					// PATCH merges time objects: explicitly clear the opposite
					// representation when switching timed and all-day events.
					for key, value := range map[string]eventTime{"start": p.Event.Start, "end": p.Event.End} {
						times := fields[key].(map[string]any)
						if value.Date != "" {
							times["dateTime"] = nil
							times["timeZone"] = nil
						} else {
							times["date"] = nil
						}
					}
				} else {
					fields["id"] = a.ID
					if p.CreateMeet {
						fields["conferenceData"] = map[string]any{"createRequest": map[string]any{"requestId": a.ID, "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}}
						endpoint += "&conferenceDataVersion=1"
					}
				}
			}
		}
		if !writePermission(r.Connection, a.Type) {
			return ErrScope
		}
		data, _ := json.Marshal(body)
		if body == nil {
			data = nil
		}
		req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
		if e != nil {
			return ErrInput
		}
		req.Header.Set("Authorization", "Bearer "+r.Connection.Token.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		if p.Before != nil {
			req.Header.Set("If-Match", p.Before.ETag)
		}
		// Persist the consumed capability BEFORE issuing the write. A crash or a
		// timeout cannot authorize a second request. Never retry writes, even on 401.
		now := time.Now().UTC()
		a.ConsumedAt = &now
		a.ExecutedBy = actor
		a.Status = "executing"
		// changeAction owns the same ledger, but the pre-request checkpoint must be
		// saved here as well. It contains this exact action, including consumption.
		j, e := s.ledger(ctx, name)
		if e != nil {
			return e
		}
		for i := range j.Ledger.Actions {
			if j.Ledger.Actions[i].ID == a.ID {
				j.Ledger.Actions[i] = *a
			}
		}
		if e = s.save(ctx, "actions-"+name, j); e != nil {
			return e
		}
		res, e := s.http.Do(req)
		if e != nil {
			terminal(a, "unknown")
			return nil
		}
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if e != nil || len(b) > 64<<10 {
			terminal(a, "unknown")
			return nil
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			var result struct {
				ID string `json:"id"`
			}
			if a.Type != "calendar_delete" && (json.Unmarshal(b, &result) != nil || !messageID.MatchString(result.ID)) {
				terminal(a, "unknown")
				return nil
			}
			a.ProviderID = result.ID
			terminal(a, "succeeded")
		} else if res.StatusCode == 412 {
			terminal(a, "conflict")
		} else if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 408 && res.StatusCode != 429 {
			terminal(a, "failed")
		} else {
			terminal(a, "unknown")
		}
		return nil
	})
}

// Reads needed to review an action may refresh and retry once. The subsequent
// write never retries. Keep the live connection pointer in sync with new scopes.
func (s *Service) actionGet(ctx context.Context, name string, c *connection, endpoint string, out any) error {
	e := s.get(ctx, c.Token, endpoint, out)
	if !errors.Is(e, errUnauthorized) {
		return e
	}
	r, e := s.read(ctx, name)
	if e != nil {
		return e
	}
	if e = s.access(ctx, name, &r, true); e != nil {
		return e
	}
	*c = *r.Connection
	e = s.get(ctx, c.Token, endpoint, out)
	if errors.Is(e, errUnauthorized) {
		c.Status = "needs_reauth"
		c.Token.AccessToken = ""
		r.Connection = c
		if e = s.save(ctx, name, r); e != nil {
			return e
		}
		return ErrReauth
	}
	return e
}
