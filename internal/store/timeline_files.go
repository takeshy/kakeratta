package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

const timelineDir = "timeline"
const maxEntryBytes = 8 << 20

var dailyPath = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}\.json$`)

// Only routing and location metadata stay in memory; bodies are read by day.
type timelineRef struct {
	day   string
	entry timeline.Entry
}

func timelineDay(at time.Time, zone *time.Location) string {
	return at.In(zone).Format("2006/01/02") + ".json"
}

func (s *Store) timelineZoneLocked() *time.Location {
	var settings Settings
	_ = readJSON(s.path("settings.json"), &settings)
	return settings.Location()
}

func (s *Store) openTimeline() error {
	if err := os.MkdirAll(s.path(timelineDir), 0o755); err != nil {
		return err
	}
	s.timelineRefs = map[string]timelineRef{}
	s.timelineThreads = map[string]map[string]bool{}
	return filepath.WalkDir(s.path(timelineDir), func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(s.path(timelineDir), path)
		if err != nil {
			return err
		}
		day := filepath.ToSlash(relative)
		if !dailyPath.MatchString(day) {
			return nil
		}
		if _, err := time.Parse("2006/01/02.json", day); err != nil {
			return fmt.Errorf("invalid timeline date %s: %w", day, err)
		}
		entries, err := s.readTimelineDay(day)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ID == "" {
				return fmt.Errorf("timeline %s: missing entry ID", day)
			}
			if _, exists := s.timelineRefs[entry.ID]; exists {
				return fmt.Errorf("timeline %s: duplicate entry %s", day, entry.ID)
			}
			s.indexTimelineEntry(day, entry)
		}
		return nil
	})
}

func (s *Store) indexTimelineEntry(day string, entry timeline.Entry) {
	if old, ok := s.timelineRefs[entry.ID]; ok {
		root := timeline.RootOf(old.entry)
		delete(s.timelineThreads[root], entry.ID)
		if len(s.timelineThreads[root]) == 0 {
			delete(s.timelineThreads, root)
		}
	}
	metadata := timeline.Entry{ID: entry.ID, Kind: entry.Kind, Speaker: entry.Speaker, At: entry.At,
		Root: entry.Root, ReplyTo: entry.ReplyTo, Deleted: entry.Deleted}
	s.timelineRefs[entry.ID] = timelineRef{day: day, entry: metadata}
	root := timeline.RootOf(entry)
	if s.timelineThreads[root] == nil {
		s.timelineThreads[root] = map[string]bool{}
	}
	s.timelineThreads[root][entry.ID] = true
}

func (s *Store) readTimelineDay(day string) ([]timeline.Entry, error) {
	var entries []timeline.Entry
	err := readJSON(s.path(timelineDir, filepath.FromSlash(day)), &entries)
	if os.IsNotExist(err) {
		for _, ref := range s.timelineRefs {
			if ref.day == day {
				return nil, fmt.Errorf("missing timeline day %s: %w", day, err)
			}
		}
		return []timeline.Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read timeline %s: %w", day, err)
	}
	return entries, nil
}

// A synced temporary file and rename keep the previous complete day readable
// if writing the replacement fails. No update history is retained in this file.
func writeTimelineDay(path string, entries []timeline.Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".timeline-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(entries); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Append retains the Workspace API name but replaces the entry's current state.
func (s *Store) Append(entry timeline.Entry) error {
	if entry.Deleted {
		_, err := s.DeleteEntry(context.Background(), entry.ID)
		if errors.Is(err, ErrEntryMissing) {
			return nil
		}
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(entry)
}

func (s *Store) appendLocked(entry timeline.Entry) error {
	if entry.Deleted {
		return errors.New("deletion notifications cannot be stored")
	}
	if entry.ID == "" {
		return errors.New("投稿IDがありません")
	}
	if entry.Kind != timeline.KindPost {
		for _, id := range []string{timeline.RootOf(entry), entry.ReplyTo} {
			if id != "" {
				if _, ok := s.timelineRefs[id]; !ok {
					return ErrEntryMissing
				}
			}
		}
	}
	day := timelineDay(entry.At, s.timelineZoneLocked())
	if old, exists := s.timelineRefs[entry.ID]; exists {
		day = old.day // Keep the original bucket after timezone changes or edits.
		entry.At = old.entry.At
		if old.entry.Deleted && !entry.Deleted {
			return nil // A stale asynchronous writer cannot revive a tombstone.
		}
	}
	if !dailyPath.MatchString(day) {
		return errors.New("invalid timeline date")
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(raw) >= maxEntryBytes {
		return errors.New("投稿が大きすぎます")
	}
	entries, err := s.readTimelineDay(day)
	if err != nil {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].ID == entry.ID {
			entries[i], found = entry, true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}
	sortEntries(entries)
	if err := writeTimelineDay(s.path(timelineDir, filepath.FromSlash(day)), entries); err != nil {
		return err
	}
	s.indexTimelineEntry(day, entry)
	return nil
}

func (s *Store) Timeline() ([]timeline.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timelineLocked()
}

func (s *Store) timelineLocked() ([]timeline.Entry, error) {
	days := map[string]bool{}
	for _, ref := range s.timelineRefs {
		days[ref.day] = true
	}
	return s.timelineDaysLocked(context.Background(), days)
}

func (s *Store) timelineDaysLocked(ctx context.Context, days map[string]bool) ([]timeline.Entry, error) {
	entries := []timeline.Entry{}
	for day := range days {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := s.readTimelineDay(day)
		if err != nil {
			return nil, err
		}
		entries = append(entries, part...)
	}
	sortEntries(entries)
	return entries, nil
}

func (s *Store) entryLocked(id string) (timeline.Entry, bool, error) {
	ref, exists := s.timelineRefs[id]
	if !exists {
		return timeline.Entry{}, false, nil
	}
	entries, err := s.readTimelineDay(ref.day)
	if err != nil {
		return timeline.Entry{}, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true, nil
		}
	}
	return timeline.Entry{}, false, fmt.Errorf("timeline entry %s missing from %s", id, ref.day)
}

func (s *Store) Entry(id string) (timeline.Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, exists, err := s.entryLocked(id)
	return entry, exists && err == nil
}

// CreateEntry preserves the first accepted entry atomically.
func (s *Store) CreateEntry(entry timeline.Entry, initial ...Reaction) (timeline.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists, err := s.entryLocked(entry.ID)
	if err != nil || exists {
		return old, false, err
	}
	err = s.appendWithReactionLocked(entry, initial)
	return entry, err == nil, err
}

// appendWithReactionLocked keeps routing before making the entry visible.
func (s *Store) appendWithReactionLocked(entry timeline.Entry, initial []Reaction) (err error) {
	if err := validateInitialReaction(entry, initial); err != nil {
		return err
	}
	// Keep the request first, under the same mutex as the entry. A process
	// that stops here leaves only an orphan request, which routing discards.
	// Once the entry exists, its request is already recoverable on restart.
	for _, reaction := range initial {
		path := s.path(reactionsDir, reaction.ID+".json")
		if _, err := os.Stat(path); err == nil {
			return ErrReactionExists
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := writeJSON(path, reaction); err != nil {
			return err
		}
	}
	defer func() {
		if err != nil {
			for _, reaction := range initial {
				_ = os.Remove(s.path(reactionsDir, reaction.ID+".json"))
			}
		}
	}()
	for _, reaction := range initial {
		if !reaction.Edited {
			continue
		}
		// Clear the old routing before the edited entry and its request can
		// be claimed. Clearing it from HTTP afterwards could erase a result
		// already prepared by another worker.
		records, readErr := s.readRouting()
		if readErr != nil {
			return readErr
		}
		delete(records, entry.ID)
		if err := writeJSON(routingPath(s.root), records); err != nil {
			return err
		}
	}
	return s.appendLocked(entry)
}

func (s *Store) UpdateMedia(id string, media *timeline.Media) (timeline.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists, err := s.entryLocked(id)
	if err != nil {
		return timeline.Entry{}, err
	}
	if !exists {
		return timeline.Entry{}, errors.New("投稿が見つかりません")
	}
	if entry.Deleted {
		return entry, nil
	}
	entry.Media = media
	return entry, s.appendLocked(entry)
}

var ErrNotEditable = errors.New("編集できる投稿が見つかりません")
var ErrEmptyText = errors.New("本文を入力してください")

func editText(entry *timeline.Entry, text string) error {
	summary := entry.Speaker == timeline.App && entry.Summary != ""
	if entry.Deleted || (entry.Speaker != timeline.User && !summary) || (entry.Kind != timeline.KindPost && entry.Kind != timeline.KindReply) {
		return ErrNotEditable
	}
	text = strings.TrimSpace(text)
	if text == "" && entry.Media == nil && entry.Link == "" {
		return ErrEmptyText
	}
	entry.Text, entry.SearchDirty = text, true
	return nil
}

func (s *Store) UpdateText(id, text string, initial ...Reaction) (timeline.Entry, error) {
	return s.updateTextChecked(id, text, "", initial...)
}

func (s *Store) UpdateTextChecked(id, text, expected string, initial ...Reaction) (timeline.Entry, error) {
	return s.updateTextChecked(id, text, expected, initial...)
}

func (s *Store) updateTextChecked(id, text, expected string, initial ...Reaction) (timeline.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists, err := s.entryLocked(id)
	if err != nil {
		return timeline.Entry{}, err
	}
	if !exists {
		return timeline.Entry{}, ErrNotEditable
	}
	if expected != "" && EntryRevision(entry) != expected {
		return timeline.Entry{}, ErrEntryConflict
	}
	if err := editText(&entry, text); err != nil {
		return timeline.Entry{}, err
	}
	return entry, s.appendWithReactionLocked(entry, initial)
}

// SearchThread reads only the days containing the requested conversation.
func (s *Store) SearchThread(ctx context.Context, id string) ([]timeline.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, exists := s.timelineRefs[id]
	if !exists || ref.entry.Deleted {
		return nil, ErrSearchThreadMissing
	}
	root := timeline.RootOf(ref.entry)
	parent, exists := s.timelineRefs[root]
	if !exists || parent.entry.Deleted || parent.entry.Kind != timeline.KindPost {
		return nil, ErrSearchThreadMissing
	}
	days := map[string]bool{}
	for entryID := range s.timelineThreads[root] {
		days[s.timelineRefs[entryID].day] = true
	}
	entries, err := s.timelineDaysLocked(ctx, days)
	if err != nil {
		return nil, err
	}
	return timeline.Thread(entries, root), nil
}

// TimelineWindow includes parents outside the window so search can validate
// reply eligibility without loading unrelated dates or deleted threads.
func (s *Store) TimelineWindow(ctx context.Context, from, to time.Time) ([]timeline.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	days, ids := map[string]bool{}, map[string]bool{}
	for id, ref := range s.timelineRefs {
		if (!from.IsZero() && ref.entry.At.Before(from)) || (!to.IsZero() && ref.entry.At.After(to)) {
			continue
		}
		ids[id], days[ref.day] = true, true
		if parent, ok := s.timelineRefs[timeline.RootOf(ref.entry)]; ok {
			ids[parent.entry.ID], days[parent.day] = true, true
		}
	}
	entries, err := s.timelineDaysLocked(ctx, days)
	if err != nil {
		return nil, err
	}
	selected := entries[:0]
	for _, entry := range entries {
		if ids[entry.ID] {
			selected = append(selected, entry)
		}
	}
	return selected, nil
}
