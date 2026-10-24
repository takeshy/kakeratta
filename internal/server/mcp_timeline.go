package server

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// recheckParallel bounds how many rechecked conversations are read at once.
const recheckParallel = 8

// speakerNames are the display names of everyone who speaks in the timeline:
// the user's profile name, the connected agent's name and the personas'.
func (s *Server) speakerNames(ctx context.Context) map[string]string {
	names := s.runner.SpeakerNames(ctx)
	names[timeline.User] = "あなた"
	if name := s.store.Settings().Profile.Name; name != "" {
		names[timeline.User] = name
	}
	return names
}

// authorOf is the display name of who said an entry: the built-in AI by its
// own name, anyone else by their speaker's name, or the speaker id.
func authorOf(entry timeline.Entry, names map[string]string) string {
	if entry.BuiltinAI() {
		return timeline.BuiltinAIName(entry.Language)
	}
	if name := names[entry.Speaker]; name != "" {
		return name
	}
	return entry.Speaker
}

// mcpEntry is a timeline entry with its speaker's display name.
type mcpEntry struct {
	timeline.Entry
	Author string `json:"author"`
}

// validEntryID is an ID a workspace could have given an entry. Anything else
// names no entry, and is not passed on to the store as a document path.
func validEntryID(id string) bool {
	return id != "" && len(id) <= 200 && !strings.ContainsAny(id, "/\\\x00\r\n") && id != "." && id != ".."
}

// mcpTimeline reads one page, plus the whole conversations of explicitly
// rechecked IDs, so a reader importing the timeline also sees new replies
// and edits on old posts. Absence from a page never means deletion; only
// missing does.
func (s *Server) mcpTimeline(ctx context.Context, a mcpArgs) (any, error) {
	if a.Limit == 0 {
		a.Limit = 20
	}
	if a.Limit < 1 || a.Limit > 50 {
		return nil, errors.New("limitは1〜50です")
	}
	if len(a.IDs) > 200 {
		return nil, errors.New("idsは200件までです")
	}
	page, err := s.store.TimelinePage(ctx, a.Limit, a.Before)
	if err != nil {
		return nil, err
	}
	entries := page.Entries
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.ID] = true
	}
	// Each ID not on the page is read on its own, several at once; the
	// results are merged in the order the IDs were given.
	var recheck []string
	for _, id := range a.IDs {
		if !seen[id] {
			recheck = append(recheck, id)
		}
	}
	threads := make([][]timeline.Entry, len(recheck))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(recheckParallel)
	for i, id := range recheck {
		if !validEntryID(id) {
			continue
		}
		group.Go(func() error {
			thread, err := s.mcpThread(groupCtx, id)
			if errors.Is(err, store.ErrSearchThreadMissing) {
				return nil
			}
			threads[i] = thread
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	missing := []string{}
	for i, id := range recheck {
		found := false
		for _, member := range threads[i] {
			found = found || member.ID == id
			if !seen[member.ID] {
				entries = append(entries, member)
				seen[member.ID] = true
			}
		}
		if !found && !seen[id] {
			missing = append(missing, id)
		}
	}
	names := s.speakerNames(ctx)
	authors := map[string]string{}
	described := make([]mcpEntry, len(entries))
	for i, entry := range entries {
		name := authorOf(entry, names)
		// The built-in AI shares the external agent's speaker id; it is named
		// on its entries so a connected agent does not take them for its own.
		if !entry.BuiltinAI() {
			authors[entry.Speaker] = name
		}
		described[i] = mcpEntry{Entry: entry, Author: name}
	}
	result := map[string]any{"entries": described, "authors": authors}
	if page.Next != "" {
		result["next"] = page.Next
	}
	if a.IDs != nil {
		result["missing"] = missing
	}
	return result, nil
}
