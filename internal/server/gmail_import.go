package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Pending mail lives outside Timeline and its search index. Only an owner Web
// decision can create the ordinary user post and its normal routing reaction.
const gmailImportKey = "gmail-import-reviews-v1"

type gmailImportPrincipal struct{}
type gmailImportInput struct {
	SourceType    string    `json:"-"`
	CalendarID    string    `json:"-"`
	EventID       string    `json:"-"`
	SeriesID      string    `json:"-"`
	CategoryID    string    `json:"categoryId"`
	CategoryLabel string    `json:"categoryLabel"`
	ConnectionID  string    `json:"connectionId"`
	MessageID     string    `json:"messageId"`
	ThreadID      string    `json:"threadId"`
	From          string    `json:"from"`
	Subject       string    `json:"subject"`
	Body          string    `json:"body"`
	ReceivedAt    time.Time `json:"receivedAt"`
	ImportedAt    time.Time `json:"importedAt"`
	SourceURL     string    `json:"sourceUrl"`
}
type gmailImportReceipt struct {
	ExcludedCalendar  *calendarImportExclusion `json:"excludedCalendar,omitempty"`
	ExcludedSender    string                   `json:"excludedSender,omitempty"`
	ExcludedTypeID    string                   `json:"excludedTypeId,omitempty"`
	ExcludedTypeLabel string                   `json:"excludedTypeLabel,omitempty"`
	Status            string                   `json:"status"`
	EntryID           string                   `json:"entryId"`
	Hash              string                   `json:"hash"`
}
type gmailImportReviews struct {
	Pending  map[string]timeline.Entry
	Receipts map[string]gmailImportReceipt
}

var importID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *Server) importReviews(ctx context.Context) (gmailImportReviews, store.SocialValue, error) {
	v, e := s.store.SocialValue(ctx, gmailImportKey)
	out := gmailImportReviews{Pending: map[string]timeline.Entry{}, Receipts: map[string]gmailImportReceipt{}}
	if e == nil && len(v.Data) > 0 {
		e = json.Unmarshal(v.Data, &out)
	}
	return out, v, e
}
func (s *Server) saveImportReviews(ctx context.Context, in gmailImportReviews, v store.SocialValue) error {
	data, e := json.Marshal(in)
	if e != nil || len(data) > 700<<10 {
		return errors.New("Google import review queue is full")
	}
	ok, e := s.store.SwapSocialValue(ctx, gmailImportKey, v.Version, store.SocialValue{Data: data})
	if e != nil || !ok {
		return errors.New("Could not save Google import review")
	}
	return nil
}
func (s *Server) importReceipt(ctx context.Context, id string) (gmailImportReceipt, bool, error) {
	v, e := s.store.SocialValue(ctx, "gmail-import-receipt:"+id)
	var receipt gmailImportReceipt
	if e != nil {
		return receipt, false, e
	}
	if len(v.Data) == 0 {
		return receipt, false, nil
	}
	e = json.Unmarshal(v.Data, &receipt)
	return receipt, e == nil, e
}
func (s *Server) finishImportReview(ctx context.Context, id string, receipt gmailImportReceipt, data gmailImportReviews, v store.SocialValue) error {
	old, e := s.store.SocialValue(ctx, "gmail-import-receipt:"+id)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(receipt)
	ok, e := s.store.SwapSocialValue(ctx, "gmail-import-receipt:"+id, old.Version, store.SocialValue{Data: b})
	if e != nil || !ok {
		return errors.New("Could not save Google import decision")
	}
	delete(data.Pending, id)
	delete(data.Receipts, id)
	return s.saveImportReviews(ctx, data, v)
}
func (s *Server) pendingGmail(ctx context.Context) ([]timeline.Entry, error) {
	data, _, e := s.importReviews(ctx)
	if e != nil {
		return nil, e
	}
	rows := []timeline.Entry{}
	for _, entry := range data.Pending {
		rows = append(rows, entry)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
	return rows, nil
}
func (s *Server) proposeGmailImport(ctx context.Context, in *gmailImportInput) (any, error) {
	allowed, _ := ctx.Value(gmailImportPrincipal{}).(bool)
	if !allowed {
		return nil, errors.New("Gmail import requires an owner MCP connection")
	}
	if in == nil || (in.CategoryID != "" && (!importID.MatchString(in.CategoryID) || len(in.CategoryID) > 64 || len(in.CategoryLabel) > 300 || !utf8.ValidString(in.CategoryLabel) || strings.ContainsAny(in.CategoryLabel, "\r\n\x00"))) || !importID.MatchString(in.ConnectionID) || !importID.MatchString(in.MessageID) || !importID.MatchString(in.ThreadID) || !utf8.ValidString(in.Body) || len([]rune(in.Body)) > 8000 || len(in.From) > 4000 || len(in.Subject) > 4000 || !utf8.ValidString(in.From+in.Subject) || strings.ContainsRune(in.From+in.Subject+in.Body, 0) || in.ReceivedAt.IsZero() || in.ImportedAt.IsZero() || in.ReceivedAt.After(time.Now().Add(24*time.Hour)) || in.ImportedAt.After(time.Now().Add(5*time.Minute)) {
		return nil, errors.New("Invalid Gmail import")
	}
	u, e := url.Parse(in.SourceURL)
	if e != nil || u.Scheme != "https" || (in.SourceType != "calendar" && u.Host != "mail.google.com" || in.SourceType == "calendar" && u.Host != "calendar.google.com") || u.User != nil {
		return nil, errors.New("Invalid Gmail source URL")
	}
	identity := in.ConnectionID + "\x00" + in.MessageID
	if in.SourceType == "calendar" {
		identity += "\x00" + in.CalendarID
	}
	sum := sha256.Sum256([]byte(identity))
	prefix := "gmail_"
	if in.SourceType == "calendar" {
		prefix = "calendar_"
	}
	id := prefix + hex.EncodeToString(sum[:])
	raw, _ := json.Marshal(in)
	h := sha256.Sum256(raw)
	hash := hex.EncodeToString(h[:])
	ctx, unlock, e := s.store.Lock(ctx, "gmail-import-reviews")
	if e != nil {
		return nil, e
	}
	defer unlock()
	data, v, e := s.importReviews(ctx)
	if e != nil {
		return nil, e
	}
	if receipt, ok, e := s.importReceipt(ctx, id); e != nil {
		return nil, e
	} else if ok {
		return receipt, nil
	}
	if receipt, ok := data.Receipts[id]; ok {
		// Repeated delivery never changes the original review, even if local
		// retention settings shortened the sender's cached copy.
		return receipt, nil
	}
	if len(data.Pending) >= 20 {
		return nil, errors.New("Google import review queue is full")
	}
	sender := ""
	if address, e := mail.ParseAddress(in.From); e == nil {
		sender = strings.ToLower(address.Address)
	}
	entry := timeline.Entry{ID: id, Root: id, Kind: timeline.KindPost, Speaker: timeline.User, At: in.ImportedAt, RequestHash: hash, Text: fmt.Sprintf("送信者: %s\n件名: %s\n受信日時: %s\n\n%s\n\n元メール: %s", in.From, in.Subject, in.ReceivedAt.Format(time.RFC3339), in.Body, in.SourceURL), Import: &timeline.ImportSource{Sender: sender, CategoryID: in.CategoryID, CategoryLabel: in.CategoryLabel, From: in.From, Subject: in.Subject, Type: "gmail", TrustLevel: "external", Status: "pending", ConnectionID: in.ConnectionID, MessageID: in.MessageID, ThreadID: in.ThreadID, ReceivedAt: in.ReceivedAt, ImportedAt: in.ImportedAt, URL: in.SourceURL}}
	if in.SourceType == "calendar" {
		entry.Import.Type = "calendar"
		entry.Import.CalendarID, entry.Import.EventID, entry.Import.SeriesID = in.CalendarID, in.EventID, in.SeriesID
		entry.Import.From, entry.Import.Sender = "", ""
		entry.Text = in.Body
	}
	receipt := gmailImportReceipt{Status: "pending", EntryID: id, Hash: hash}
	data.Pending[id] = entry
	data.Receipts[id] = receipt
	if e = s.saveImportReviews(ctx, data, v); e != nil {
		return nil, e
	}
	s.publishEvent("entry", entry)
	return receipt, nil
}
func (s *Server) decideGmailImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Accept         *bool `json:"accept"`
		ExcludeSimilar bool  `json:"excludeSimilar"`
		ExcludeSender  bool  `json:"excludeSender"`
		ExcludeEvent   bool  `json:"excludeEvent"`
	}
	if readJSON(r, &in) != nil || in.Accept == nil {
		fail(w, 400, "Invalid Gmail import decision")
		return
	}
	ctx, unlock, e := s.store.Lock(r.Context(), "gmail-import-reviews")
	if e != nil {
		fail(w, 503, "Could not lock Google import review")
		return
	}
	defer unlock()
	data, v, e := s.importReviews(ctx)
	if e != nil {
		fail(w, 503, "Could not read Google import review")
		return
	}
	id := r.PathValue("id")
	// A terminal receipt survives deletion of the post and contains no body.
	if receipt, ok, err := s.importReceipt(ctx, id); err != nil {
		fail(w, 503, "Could not read Google import decision")
		return
	} else if ok {
		entry := data.Pending[id]
		if err = s.finishImportReview(ctx, id, receipt, data, v); err != nil {
			fail(w, 503, err.Error())
			return
		}
		if receipt.Status == "accepted" {
			if existing, exists := s.store.Entry(id); exists {
				s.publishEvent("entry", existing)
				writeJSON(w, 200, map[string]any{"status": "accepted", "entry": existing})
				return
			}
		}
		if entry.ID != "" {
			entry = timeline.Entry{ID: id, Root: id, Kind: timeline.KindPost, Deleted: true, Import: &timeline.ImportSource{Type: "gmail", Status: "pending"}}
			s.publishEvent("entry", entry)
		}
		writeJSON(w, 200, map[string]any{"status": receipt.Status, "entryId": id})
		return
	}
	receipt, ok := data.Receipts[id]
	if !ok {
		fail(w, 404, "Google import not found")
		return
	}
	entry, pending := data.Pending[id]
	if !pending {
		writeJSON(w, 200, map[string]any{"status": receipt.Status, "entryId": id})
		return
	}
	if !*in.Accept {
		// An acceptance whose response was lost cannot be undone by rejecting the
		// still-cached card: remove accepted posts through the normal delete flow.
		if receipt.Status == "accepted" {
			fail(w, 409, "Google import was already accepted; delete the imported post to remove it")
			return
		}
		if in.ExcludeEvent && entry.Import != nil && entry.Import.Type == "calendar" {
			receipt.ExcludedCalendar = &calendarImportExclusion{CalendarID: entry.Import.CalendarID, EventID: entry.Import.EventID, SeriesID: entry.Import.SeriesID, Label: entry.Import.Subject}
		}
		if in.ExcludeSender && entry.Import != nil && entry.Import.Sender != "" {
			receipt.ExcludedSender = entry.Import.Sender
		}
		if in.ExcludeSimilar && entry.Import != nil && entry.Import.CategoryID != "" {
			receipt.ExcludedTypeID = entry.Import.CategoryID
			receipt.ExcludedTypeLabel = entry.Import.CategoryLabel
		}
		receipt.Status = "rejected"
		data.Receipts[id] = receipt
		if e = s.finishImportReview(ctx, id, receipt, data, v); e != nil {
			fail(w, 503, e.Error())
			return
		}
		entry = timeline.Entry{ID: id, Root: id, Kind: timeline.KindPost, Deleted: true, Import: &timeline.ImportSource{Type: "gmail", Status: "pending"}}
		s.publishEvent("entry", entry)
		writeJSON(w, 200, map[string]any{"status": "rejected", "discarded": entry})
		return
	}
	// Reserve the owner's decision before accepting. Failed acceptance remains
	// visible for explicit owner retry; a repeated MCP submission never routes it.
	receipt.Status = "accepted"
	data.Receipts[id] = receipt
	if e = s.saveImportReviews(ctx, data, v); e != nil {
		fail(w, 503, e.Error())
		return
	}
	entry.Import.Status = "accepted"
	stored, status, e := s.acceptEntry(entry, nil)
	if e != nil {
		fail(w, status, e.Error())
		return
	}
	data, v, e = s.importReviews(ctx)
	if e == nil {
		e = s.finishImportReview(ctx, id, receipt, data, v)
	}
	if e != nil {
		fail(w, 503, "Google import accepted; refresh and retry the review to finish cleanup")
		return
	}
	s.publishEvent("entry", stored)
	writeJSON(w, 200, map[string]any{"status": "accepted", "entry": stored})
}
