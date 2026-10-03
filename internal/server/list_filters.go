package server

import (
	"errors"
	"github.com/takeshy/kakeratta/internal/store"
	"net/http"
	"time"
)

func listWindow(from, to string) (time.Time, time.Time, error) {
	var start, end time.Time
	var err error
	if from != "" {
		start, err = time.Parse(time.RFC3339Nano, from)
		if err != nil {
			return start, end, errors.New("開始日が不正です")
		}
	}
	if to != "" {
		end, err = time.Parse(time.RFC3339Nano, to)
		if err != nil {
			return start, end, errors.New("終了日が不正です")
		}
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return start, end, errors.New("終了日は開始日以降にしてください")
	}
	return start, end, nil
}
func (s *Server) favoriteSelection(r *http.Request, only bool) (map[string]bool, error) {
	if !only {
		return nil, nil
	}
	records, err := s.store.Records(r.Context(), store.RecordFavorites)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for id := range records {
		ids[id] = true
	}
	return ids, nil
}
