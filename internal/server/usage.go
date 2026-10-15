package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/takeshy/kakeratta/internal/usage"
)

func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().In(s.store.Settings().Location()).Format("2006-01")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		fail(w, http.StatusBadRequest, "invalid usage month")
		return
	}
	data, err := usage.Read(r.Context(), s.store, month)
	if err != nil {
		fail(w, http.StatusInternalServerError, "usage unavailable")
		return
	}
	if id := r.URL.Query().Get("persona"); id != "" {
		rows := make([]usage.Row, 0)
		for _, row := range data.Rows {
			if row.Persona == id {
				rows = append(rows, row)
			}
		}
		data.Rows = rows
	}
	sort.Slice(data.Rows, func(i, j int) bool {
		if data.Rows[i].Persona != data.Rows[j].Persona {
			return data.Rows[i].Persona < data.Rows[j].Persona
		}
		return data.Rows[i].Category < data.Rows[j].Category
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"usage": data, "ratesUpdated": "2026-10-16"})
}
