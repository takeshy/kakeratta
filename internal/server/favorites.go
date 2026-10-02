package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type favorite struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

func (s *Server) saveFavorite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entries, err := store.SearchThread(r.Context(), s.store, id)
	if errors.Is(err, store.ErrSearchThreadMissing) {
		fail(w, 404, "投稿が見つかりません")
		return
	}
	if err != nil {
		fail(w, 500, "投稿を読み込めませんでした")
		return
	}
	exists := false
	for _, e := range entries {
		if e.ID == id {
			exists = true
		}
	}
	if !exists {
		fail(w, 404, "投稿が見つかりません")
		return
	}
	saved := favorite{ID: id, At: time.Now().UTC()}
	err = s.store.UpdateRecord(r.Context(), store.RecordFavorites, id, time.Time{}, func(current []byte, exists bool) ([]byte, error) {
		if exists {
			return current, json.Unmarshal(current, &saved)
		}
		return json.Marshal(saved)
	})
	if err != nil {
		fail(w, 500, "お気に入りを保存できませんでした")
		return
	}
	s.publishEvent("favorite", saved)
	writeJSON(w, 200, saved)
}

func (s *Server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	err := s.store.UpdateRecord(r.Context(), store.RecordFavorites, r.PathValue("id"), time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil })
	if err != nil {
		fail(w, 400, "お気に入りを解除できませんでした")
		return
	}
	s.publishEvent("favorite", map[string]any{"id": r.PathValue("id"), "removed": true})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getFavorites(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.Records(r.Context(), store.RecordFavorites)
	if err != nil {
		fail(w, 500, "お気に入りを読み込めませんでした")
		return
	}
	items := []struct {
		favorite
		Entry timeline.Entry `json:"entry"`
	}{}
	speakers := map[string]bool{}
	ids := []string{}
	for id, raw := range records {
		var f favorite
		if err := json.Unmarshal(raw, &f); err != nil {
			fail(w, 500, "お気に入りを読み込めませんでした")
			return
		}
		entry, ok := s.store.Entry(id)
		if !ok || entry.Deleted {
			continue
		}
		ids = append(ids, id)
		speakers[entry.Speaker] = true
		if filter := r.URL.Query().Get("speaker"); filter != "" && filter != entry.Speaker {
			continue
		}
		items = append(items, struct {
			favorite
			Entry timeline.Entry `json:"entry"`
		}{f, entry})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].At.Equal(items[j].At) {
			return items[i].ID > items[j].ID
		}
		return items[i].At.After(items[j].At)
	})
	total := len(items)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	offset = max(0, min(offset, total))
	items = items[offset:min(total, offset+limit)]
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "ids": ids, "speakers": speakers})
}
