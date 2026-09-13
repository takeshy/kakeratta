package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
)

func (s *Server) searchTimeline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query  string `json:"query"`
		Mode   string `json:"mode"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := readJSONLimit(r, &body, 8<<10); err != nil {
		fail(w, 400, "検索条件を読めませんでした")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	result, err := s.search.Search(ctx, body.Query, body.Mode, body.Limit, body.Offset)
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
