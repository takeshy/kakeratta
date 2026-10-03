package store

import (
	"context"
	"errors"
	"github.com/takeshy/kakeratta/internal/timeline"
	"sort"
	"time"
)

// FilteredTimelinePage selects conversations before pagination. Saved replies
// select their parent conversation, even if that post is outside the date range.
func FilteredTimelinePage(ctx context.Context, w Workspace, limit int, before string, from, to time.Time, ids map[string]bool) (TimelinePage, error) {
	cursor, err := decodeCursor(before)
	if err != nil {
		return TimelinePage{}, err
	}
	var candidates []timeline.Entry
	if ids != nil {
		for id := range ids {
			if e, ok := w.Entry(id); ok {
				candidates = append(candidates, e)
			}
		}
	} else {
		candidates, err = SearchTimelineWindow(ctx, w, from, to)
		if err != nil {
			return TimelinePage{}, err
		}
	}
	roots := map[string]timeline.Entry{}
	for _, e := range candidates {
		if e.Deleted || (!from.IsZero() && e.At.Before(from)) || (!to.IsZero() && e.At.After(to)) || (ids == nil && e.Kind != timeline.KindPost) {
			continue
		}
		root := timeline.RootOf(e)
		p := e
		if root != e.ID {
			var ok bool
			p, ok = w.Entry(root)
			if !ok {
				continue
			}
		}
		if p.Deleted || p.Kind != timeline.KindPost {
			continue
		}
		if before != "" && !newer(timeline.Entry{At: cursor.At, ID: cursor.ID}, p) {
			continue
		}
		roots[root] = p
	}
	posts := make([]timeline.Entry, 0, len(roots))
	for _, p := range roots {
		posts = append(posts, p)
	}
	sort.Slice(posts, func(i, j int) bool { return newer(posts[i], posts[j]) })
	page := TimelinePage{Entries: []timeline.Entry{}}
	limit = pageSize(limit)
	if len(posts) > limit {
		posts = posts[:limit]
		page.Next = encodeCursor(posts[len(posts)-1])
	}
	for _, p := range posts {
		thread, err := SearchThread(ctx, w, p.ID)
		if errors.Is(err, ErrSearchThreadMissing) {
			continue
		}
		if err != nil {
			return TimelinePage{}, err
		}
		page.Entries = append(page.Entries, thread...)
	}
	sortEntries(page.Entries)
	return page, nil
}
