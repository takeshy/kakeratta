package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// This tool synchronizes human-authored Timeline entries. It is separate from
// planning/direct-request tools, and clients must not offer it to their model.
type timelineSyncChange struct {
	ID             string    `json:"id"`
	Text           string    `json:"text"`
	At             time.Time `json:"at"`
	ReplyTo        string    `json:"replyTo,omitempty"`
	Revision       string    `json:"revision,omitempty"`
	DeleteRevision string    `json:"deleteRevision,omitempty"`
	Deleted        bool      `json:"deleted,omitempty"`
	React          bool      `json:"react,omitempty"`
	Language       string    `json:"language,omitempty"`
}

type timelineSyncRecord struct {
	Entry          timeline.Entry `json:"entry"`
	Revision       string         `json:"revision"`
	DeleteRevision string         `json:"deleteRevision"`
	AuthorName     string         `json:"authorName"`
}

type timelineSyncResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (s *Server) syncTimelineRecord(ctx context.Context, entry timeline.Entry, thread []timeline.Entry) timelineSyncRecord {
	name := entry.Speaker
	switch entry.Speaker {
	case timeline.User:
		name = s.store.Settings().Profile.Name
		if name == "" {
			name = "あなた"
		}
	case timeline.Agent:
		name = s.activeMCPName()
		if name == "" {
			name = "外部エージェント"
		}
	default:
		if p, ok := s.catalogue.Find(ctx, entry.Speaker); ok {
			name = p.Name
		}
	}
	gone := append([]timeline.Entry{entry}, timeline.Replies(thread, entry.ID)...)
	if entry.Kind == timeline.KindPost {
		gone = thread
	}
	return timelineSyncRecord{Entry: entry, Revision: store.EntryRevision(entry), DeleteRevision: store.DeletionRevision(gone), AuthorName: name}
}

func (s *Server) syncTimeline(ctx context.Context, a mcpArgs) (any, error) {
	if len(a.SyncEntries) > 50 || len(a.SyncIDs) > 200 {
		return nil, errors.New("同期は投稿50件、照合ID200件までです")
	}
	if a.Limit == 0 {
		a.Limit = 50
	}
	if a.Limit < 1 || a.Limit > 50 {
		return nil, errors.New("limitは1〜50です")
	}
	// Validate the entire batch before accepting the first mutation.
	for _, change := range a.SyncEntries {
		if strings.TrimSpace(change.ID) == "" || len(change.ID) > 200 || strings.ContainsAny(change.ID, "/\\\r\n") || len(change.Text) > 200000 {
			return nil, errors.New("同期する投稿が不正です")
		}
		if change.Deleted && change.DeleteRevision == "" {
			return nil, errors.New("削除にはdeleteRevisionが必要です")
		}
		if !change.Deleted && change.Revision == "" && (!strings.HasPrefix(change.ID, "gh-") || change.At.IsZero() || strings.TrimSpace(change.Text) == "") {
			return nil, errors.New("新規投稿にはgh-で始まるID、日時、本文が必要です")
		}
		if change.React && s.runner == nil {
			return nil, errors.New("担当の実行環境がありません")
		}
		if change.React && s.hasMultipleMentionedPeople(ctx, change.Text) {
			return nil, errors.New("@で指名できる担当は1人だけです")
		}
	}
	results := []timelineSyncResult{}
	for _, change := range a.SyncEntries {
		status, err := s.syncTimelineChange(ctx, change)
		if err != nil {
			return nil, err
		}
		results = append(results, timelineSyncResult{ID: change.ID, Status: status})
	}
	page, err := s.store.TimelinePage(ctx, a.Limit, a.Before)
	if err != nil {
		return nil, err
	}
	records := []timelineSyncRecord{}
	seen := map[string]bool{}
	threads := map[string][]timeline.Entry{}
	for _, entry := range page.Entries {
		root := timeline.RootOf(entry)
		threads[root] = append(threads[root], entry)
	}
	for _, entry := range page.Entries {
		records = append(records, s.syncTimelineRecord(ctx, entry, threads[timeline.RootOf(entry)]))
		seen[entry.ID] = true
	}
	missing := []string{}
	for _, id := range a.SyncIDs {
		if seen[id] {
			continue
		}
		entry, exists := s.store.Entry(id)
		if !exists || entry.Deleted {
			missing = append(missing, id)
			continue
		}
		root := timeline.RootOf(entry)
		thread, loaded := threads[root]
		if !loaded {
			thread, err = s.mcpThread(ctx, id)
			if err != nil {
				return nil, err
			}
			threads[root] = thread
		}
		// Include the whole thread so a new reply on an old post is discoverable.
		for _, member := range thread {
			if seen[member.ID] {
				continue
			}
			records = append(records, s.syncTimelineRecord(ctx, member, thread))
			seen[member.ID] = true
		}
	}
	return map[string]any{"records": records, "missing": missing, "results": results, "next": page.Next}, nil
}

func (s *Server) syncTimelineChange(ctx context.Context, change timelineSyncChange) (string, error) {
	ctx, release, err := s.store.Lock(ctx, "timeline-sync:"+change.ID)
	if err != nil {
		return "", err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return "", err
	}

	existing, found := s.store.Entry(change.ID)
	if change.Deleted {
		if !found {
			return "missing", nil
		}
		gone, err := s.store.DeleteEntryChecked(ctx, change.ID, change.DeleteRevision)
		if errors.Is(err, store.ErrEntryConflict) {
			return "conflict", nil
		}
		if errors.Is(err, store.ErrEntryMissing) {
			return "missing", nil
		}
		if err != nil {
			return "", err
		}
		for _, entry := range gone {
			_ = s.store.UpdateRecord(ctx, store.RecordFavorites, entry.ID, time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil })
			entry.Deleted = true
			s.publishEvent("entry", entry)
			_ = s.store.ForgetRouting(entry.ID)
		}
		if s.runner != nil {
			s.runner.EntriesForgotten(timeline.RootOf(existing), gone)
		}
		return "applied", nil
	}
	text := strings.TrimSpace(change.Text)
	if change.Revision != "" {
		if !found {
			return "missing", nil
		}
		if existing.Speaker != timeline.User {
			return "conflict", nil
		}
		if existing.Text == text {
			return "applied", nil
		}
		var reactions []store.Reaction
		if change.React {
			updated := existing
			updated.Text = text
			reaction := store.EntryReaction(updated)
			reaction.Edited = true
			reactions = append(reactions, reaction)
		}
		updated, err := s.store.UpdateTextChecked(change.ID, text, change.Revision, reactions...)
		if errors.Is(err, store.ErrEntryConflict) {
			return "conflict", nil
		}
		if errors.Is(err, store.ErrNotEditable) || errors.Is(err, store.ErrEntryMissing) {
			return "missing", nil
		}
		if err != nil {
			return "", err
		}
		s.publishEvent("entry", updated)
		if len(reactions) > 0 {
			s.runner.RouteAccepted(reactions[0].ID)
		}
		return "applied", nil
	}
	entry := timeline.Entry{ID: change.ID, Text: text, At: change.At.UTC(), Kind: timeline.KindPost, Speaker: timeline.User, Language: displayLanguage(change.Language)}
	if change.ReplyTo != "" {
		parent, exists := s.store.Entry(change.ReplyTo)
		if !exists {
			return "missing", nil
		}
		entry.Kind = timeline.KindReply
		entry.ReplyTo = parent.ID
		entry.Root = timeline.RootOf(parent)
	}
	data, _ := json.Marshal(entry)
	digest := sha256.Sum256(data)
	entry.RequestHash = "gh-sync:" + hex.EncodeToString(digest[:])
	reserved := false
	err = s.store.UpdateRecord(ctx, store.RecordTimelineSync, entry.ID, time.Time{}, func(current []byte, exists bool) ([]byte, error) {
		reserved = !exists
		if exists {
			return current, nil
		}
		return json.Marshal(map[string]string{"requestHash": entry.RequestHash})
	})
	if err != nil {
		return "", err
	}
	if !reserved {
		current, exists := s.store.Entry(entry.ID)
		if !exists {
			return "missing", nil
		} // Never resurrect a deleted or uncertain first submission.
		if current.RequestHash != entry.RequestHash {
			return "conflict", nil
		}
		return "applied", nil
	}
	var reactions []store.Reaction
	if change.React {
		reactions = append(reactions, store.EntryReaction(entry))
	}
	stored, created, err := s.store.CreateEntry(entry, reactions...)
	if errors.Is(err, store.ErrEntryMissing) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if !created && stored.RequestHash != entry.RequestHash {
		return "conflict", nil
	}
	if created {
		s.publishEvent("entry", entry)
		if len(reactions) > 0 {
			s.runner.RouteAccepted(reactions[0].ID)
		}
	}
	return "applied", nil
}
