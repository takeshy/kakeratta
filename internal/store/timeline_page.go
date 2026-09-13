package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A page counts parent posts; all their replies travel with them.
type TimelinePage struct {
	Entries []timeline.Entry `json:"entries"`
	Next    string           `json:"next,omitempty"`
}

type pageCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

var ErrTimelineCursor = errors.New("タイムラインの続き位置が不正です")

func decodeCursor(raw string) (pageCursor, error) {
	var c pageCursor
	if raw == "" {
		return c, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(data, &c) != nil || c.At.IsZero() || c.ID == "" {
		return c, ErrTimelineCursor
	}
	return c, nil
}
func encodeCursor(e timeline.Entry) string {
	raw, _ := json.Marshal(pageCursor{At: e.At, ID: e.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func pageSize(limit int) int {
	if limit < 1 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}
func newer(a, b timeline.Entry) bool {
	if a.At.Equal(b.At) {
		return a.ID > b.ID
	}
	return a.At.After(b.At)
}
func sortEntries(entries []timeline.Entry) {
	sort.Slice(entries, func(i, j int) bool { return newer(entries[j], entries[i]) })
}

func (s *Store) TimelinePage(ctx context.Context, limit int, before string) (TimelinePage, error) {
	c, err := decodeCursor(before)
	if err != nil {
		return TimelinePage{}, err
	}
	all, err := s.Timeline()
	if err != nil {
		return TimelinePage{}, err
	}
	if err := ctx.Err(); err != nil {
		return TimelinePage{}, err
	}
	limit = pageSize(limit)
	posts := []timeline.Entry{}
	for _, entry := range all {
		if entry.Kind == timeline.KindPost && !entry.Deleted && (before == "" || newer(timeline.Entry{At: c.At, ID: c.ID}, entry)) {
			posts = append(posts, entry)
		}
	}
	sort.Slice(posts, func(i, j int) bool { return newer(posts[i], posts[j]) })
	page := TimelinePage{Entries: []timeline.Entry{}}
	if len(posts) > limit {
		posts = posts[:limit]
		page.Next = encodeCursor(posts[len(posts)-1])
	}
	roots := map[string]bool{}
	for _, post := range posts {
		roots[post.ID] = true
	}
	for _, entry := range all {
		if !entry.Deleted && (roots[entry.ID] || roots[entry.Root]) {
			page.Entries = append(page.Entries, entry)
		}
	}
	sortEntries(page.Entries)
	return page, nil
}

func (w *FirestoreWorkspace) TimelinePage(ctx context.Context, limit int, before string) (TimelinePage, error) {
	c, err := decodeCursor(before)
	if err != nil {
		return TimelinePage{}, err
	}
	limit = pageSize(limit)
	query := w.timeline().Where("Kind", "==", string(timeline.KindPost)).OrderBy("At", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	if before != "" {
		query = query.StartAfter(c.At, c.ID)
	}
	// Deleted posts still exist in the append-only timeline. Read bounded batches
	// until there are enough visible parents, plus one to determine whether to continue.
	posts := []timeline.Entry{}
	for len(posts) <= limit {
		docs, err := query.Limit(limit + 1).Documents(ctx).GetAll()
		if err != nil {
			return TimelinePage{}, err
		}
		for _, doc := range docs {
			var entry timeline.Entry
			if err := doc.DataTo(&entry); err != nil {
				return TimelinePage{}, err
			}
			if !entry.Deleted {
				posts = append(posts, entry)
			}
		}
		if len(docs) < limit+1 {
			break
		}
		query = query.StartAfter(docs[len(docs)-1])
	}
	page := TimelinePage{Entries: []timeline.Entry{}}
	if len(posts) > limit {
		posts = posts[:limit]
		page.Next = encodeCursor(posts[len(posts)-1])
	}
	for _, post := range posts {
		page.Entries = append(page.Entries, post)
		docs, err := w.timeline().Where("Root", "==", post.ID).Documents(ctx).GetAll()
		if err != nil {
			return TimelinePage{}, err
		}
		for _, doc := range docs {
			var entry timeline.Entry
			if err := doc.DataTo(&entry); err != nil {
				return TimelinePage{}, err
			}
			if entry.ID != post.ID && !entry.Deleted {
				page.Entries = append(page.Entries, entry)
			}
		}
	}
	sortEntries(page.Entries)
	return page, nil
}
