package google

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var ErrTimeline = errors.New("Could not deliver Gmail review to kakeratta; check its MCP URL, key, version and review queue")

type timelineTarget struct {
	Enabled  bool
	URL, Key string
}
type TimelineTargetInput struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Key     string `json:"key"`
}
type TimelineTargetStatus struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	Configured bool   `json:"configured"`
}

func targetStatus(t *timelineTarget) TimelineTargetStatus {
	if t == nil {
		return TimelineTargetStatus{}
	}
	return TimelineTargetStatus{Enabled: t.Enabled, URL: t.URL, Configured: t.Key != ""}
}
func (s *Service) TimelineTarget(ctx context.Context) (TimelineTargetStatus, error) {
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return TimelineTargetStatus{}, ErrStorage
	}
	defer unlock()
	r, e := s.read(ctx, "timeline-target")
	return targetStatus(r.Target), e
}
func (s *Service) SetTimelineTarget(ctx context.Context, in TimelineTargetInput) (TimelineTargetStatus, error) {
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return TimelineTargetStatus{}, ErrStorage
	}
	defer unlock()
	r, e := s.read(ctx, "timeline-target")
	if e != nil {
		return TimelineTargetStatus{}, e
	}
	u, e := url.Parse(in.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/api/mcp/users/") || len(u.Path) > 200 || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return TimelineTargetStatus{}, ErrInput
	}
	if in.Key == "" && r.Target != nil && r.Target.URL == in.URL {
		in.Key = r.Target.Key
	}
	key, e := hex.DecodeString(in.Key)
	if e != nil || len(key) != 32 {
		return TimelineTargetStatus{}, ErrInput
	}
	r.Target = &timelineTarget{Enabled: in.Enabled, URL: in.URL, Key: in.Key}
	if e = s.save(ctx, "timeline-target", r); e != nil {
		return TimelineTargetStatus{}, e
	}
	return targetStatus(r.Target), nil
}
func (s *Service) ClearTimelineTarget(ctx context.Context) error {
	ctx, unlock, e := s.lock(ctx)
	if e != nil {
		return ErrStorage
	}
	defer unlock()
	return s.save(ctx, "timeline-target", record{})
}
func (s *Service) publishInbox(ctx context.Context, st *gmailSyncState) error {
	r, e := s.read(ctx, "timeline-target")
	if e != nil {
		return e
	}
	t := r.Target
	if t == nil || !t.Enabled || !st.Settings.Enabled {
		return nil
	}
	targetID := digest(t.URL)
	sent := 0
	sort.SliceStable(st.Messages, func(i, j int) bool {
		rank := func(m InboxMessage) int {
			if m.TimelineTarget != targetID || m.TimelineStatus == "" {
				return 0
			}
			if m.TimelineStatus == "pending" {
				return 1
			}
			return 2
		}
		return rank(st.Messages[i]) < rank(st.Messages[j])
	})
	rows := []InboxMessage{}
	for i := range st.Messages {
		m := st.Messages[i]
		if m.TimelineTarget != targetID && excludedType(st.Settings.ExcludedTypes, m.CategoryID) {
			continue
		}
		if m.TimelineTarget == targetID && m.TimelineStatus == "rejected" {
			continue
		}
		if sent >= 5 || m.TimelineTarget == targetID && (m.TimelineStatus == "accepted" || m.TimelineStatus == "rejected") {
			rows = append(rows, m)
			continue
		}
		payload := map[string]any{"categoryId": m.CategoryID, "categoryLabel": m.CategoryLabel, "connectionId": m.ConnectionID, "messageId": m.MessageID, "threadId": m.ThreadID, "from": m.From, "subject": m.Subject, "body": m.Body, "receivedAt": m.ReceivedAt, "importedAt": m.ImportedAt, "sourceUrl": m.SourceURL}
		receipt, e := s.deliverImport(ctx, t, "propose_gmail_import", map[string]any{"mail": payload})
		if e != nil {
			return e
		}
		feedbackID := digest(struct{ Target, Entry string }{targetID, receipt.EntryID})
		if receipt.Status == "rejected" && !st.FeedbackSeen[feedbackID] {
			if receipt.ExcludedSender != "" {
				sender, e := address(receipt.ExcludedSender)
				if e != nil {
					return ErrTimeline
				}
				sender = strings.ToLower(sender)
				if !has(st.Settings.ExcludedSenders, sender) {
					if len(st.Settings.ExcludedSenders) >= 50 {
						return ErrInput
					}
					st.Settings.ExcludedSenders = append(st.Settings.ExcludedSenders, sender)
				}
			}
			if receipt.ExcludedTypeID != "" {
				kind := MailType{ID: receipt.ExcludedTypeID, Label: receipt.ExcludedTypeLabel}
				if !validMailType(kind) {
					return ErrTimeline
				}
				if !excludedType(st.Settings.ExcludedTypes, kind.ID) {
					if len(st.Settings.ExcludedTypes) >= 100 {
						return ErrInput
					}
					st.Settings.ExcludedTypes = append(st.Settings.ExcludedTypes, kind)
				}
			}
			st.FeedbackSeen[feedbackID] = true
		}
		m.TimelineStatus = receipt.Status
		m.TimelineEntryID = receipt.EntryID
		m.TimelineTarget = targetID
		if receipt.Status == "rejected" {
			m.Body = ""
			m.Subject = ""
			m.From = ""
		}
		st.Messages[i] = m
		sent++
		if receipt.Status != "rejected" {
			rows = append(rows, m)
		}
	}
	st.Messages = rows
	return nil
}

type importDeliveryReceipt struct {
	ExcludedCalendar  *CalendarExclusion `json:"excludedCalendar,omitempty"`
	ExcludedSender    string             `json:"excludedSender"`
	ExcludedTypeID    string             `json:"excludedTypeId"`
	ExcludedTypeLabel string             `json:"excludedTypeLabel"`

	Status  string `json:"status"`
	EntryID string `json:"entryId"`
}

func (s *Service) deliverImport(ctx context.Context, t *timelineTarget, tool string, args map[string]any) (importDeliveryReceipt, error) {
	rpc := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}
	b, _ := json.Marshal(rpc)
	req, e := http.NewRequestWithContext(ctx, "POST", t.URL, bytes.NewReader(b))
	if e != nil {
		return importDeliveryReceipt{}, ErrTimeline
	}
	req.Header.Set("Authorization", "Bearer "+t.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	res, e := s.http.Do(req)
	if e != nil {
		return importDeliveryReceipt{}, ErrTimeline
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, (128<<10)+1))
	_ = res.Body.Close()
	if e != nil || len(data) > 128<<10 || res.StatusCode != 200 {
		return importDeliveryReceipt{}, ErrTimeline
	}
	var out struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &out) != nil || len(out.Error) > 0 || out.Result.IsError {
		return importDeliveryReceipt{}, ErrTimeline
	}
	result := out.Result.Structured
	if len(result) == 0 && len(out.Result.Content) == 1 && out.Result.Content[0].Type == "text" {
		result = []byte(out.Result.Content[0].Text)
	}
	var receipt importDeliveryReceipt

	if json.Unmarshal(result, &receipt) != nil || receipt.Status != "pending" && receipt.Status != "accepted" && receipt.Status != "rejected" || !messageID.MatchString(receipt.EntryID) {
		return importDeliveryReceipt{}, ErrTimeline
	}

	return receipt, nil
}
