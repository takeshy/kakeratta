package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func (s *Server) searchTimeline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query         string `json:"query"`
		FavoritesOnly bool   `json:"favoritesOnly"`
		From          string `json:"from"`
		To            string `json:"to"`
		Mode          string `json:"mode"`
		Limit         int    `json:"limit"`
		Offset        int    `json:"offset"`
	}
	if err := readJSONLimit(r, &body, 8<<10); err != nil {
		fail(w, 400, "検索条件を読めませんでした")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	from, to, err := listWindow(body.From, body.To)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ids, err := s.favoriteSelection(r, body.FavoritesOnly)
	if err != nil {
		fail(w, 500, "お気に入りを読み込めませんでした")
		return
	}
	if !from.IsZero() || !to.IsZero() {
		entries, readErr := store.SearchTimelineWindow(ctx, s.store, from, to)
		if readErr != nil {
			fail(w, 500, "投稿を読み込めませんでした")
			return
		}
		kept := map[string]bool{}
		for _, e := range entries {
			if !e.Deleted && (from.IsZero() || !e.At.Before(from)) && (to.IsZero() || !e.At.After(to)) && (ids == nil || ids[e.ID]) {
				kept[e.ID] = true
			}
		}
		ids = kept
	}
	var result search.Result
	if ids != nil {
		result, err = s.search.SearchSelected(ctx, body.Query, body.Mode, body.Limit, body.Offset, ids)
	} else {
		result, err = s.search.Search(ctx, body.Query, body.Mode, body.Limit, body.Offset)
	}

	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, search.ErrQuery) || errors.Is(err, search.ErrMode) {
			code = http.StatusBadRequest
		}
		if errors.Is(err, search.ErrBusy) {
			fail(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		// Firestore reports an expired deadline as a gRPC status, not as
		// context.DeadlineExceeded, so ask the request context as well.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fail(w, 504, "意味検索の準備に時間がかかっています。もう一度検索すると続きから準備します")
			return
		}
		fail(w, code, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

// speakerEntries is what one speaker said, newest first: the list opened from a
// name on the timeline. Each remark is read under its post, so the conversations
// the page touches come with it - one request, not one per conversation.
func (s *Server) speakerEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	from, to, err := listWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ids, err := s.favoriteSelection(r, r.URL.Query().Get("favoritesOnly") == "true")
	if err != nil {
		fail(w, 500, "お気に入りを読み込めませんでした")
		return
	}
	result, err := s.search.BySpeaker(r.Context(), r.PathValue("id"), limit, offset, func(id string, at time.Time) bool {
		return (ids == nil || ids[id]) && (from.IsZero() || !at.Before(from)) && (to.IsZero() || !at.After(to))
	})
	if err != nil {
		if errors.Is(err, search.ErrSpeaker) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		fail(w, http.StatusInternalServerError, "発言を読み込めませんでした")
		return
	}
	entries, err := s.conversations(r.Context(), result.Hits)
	if err != nil {
		fail(w, http.StatusInternalServerError, "発言を読み込めませんでした")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		search.Result
		Entries []timeline.Entry `json:"entries"`
	}{result, entries})
}

// conversations reads the conversation of each hit once, a few at a time. One
// that has gone (its post was deleted) is left out, not an error.
func (s *Server) conversations(ctx context.Context, hits []search.Hit) ([]timeline.Entry, error) {
	var roots []string
	seen := map[string]bool{}
	for _, hit := range hits {
		if root := timeline.RootOf(hit.Entry); root != "" && !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	threads := make([][]timeline.Entry, len(roots))
	errs := make([]error, len(roots))
	slots := make(chan struct{}, 5)
	var group sync.WaitGroup
	for i, root := range roots {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			threads[i], errs[i] = store.SearchThread(ctx, s.store, root)
		}()
	}
	group.Wait()
	entries := []timeline.Entry{}
	for i := range roots {
		if errs[i] != nil {
			if errors.Is(errs[i], store.ErrSearchThreadMissing) {
				continue
			}
			return nil, errs[i]
		}
		entries = append(entries, threads[i]...)
	}
	return entries, nil
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	thread, err := store.SearchThread(r.Context(), s.store, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrSearchThreadMissing) {
			fail(w, 404, err.Error())
			return
		}
		fail(w, 500, "投稿を読み込めませんでした")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"entries": thread})
}
