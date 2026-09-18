package server

import (
	"net/http"
	"sort"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// routingRow is one persona as the router saw it for a post. The name and
// icon are looked up now, so a persona renamed since still reads as itself; a
// persona gone from the catalogue keeps its id for a name.
type routingRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	Reason string `json:"reason,omitempty"`
	// For a persona the router chose: whether its turn has finished, and
	// whether it said anything in the thread after this post. Both false while
	// it is still working; done and not spoke is a persona that stayed quiet.
	Done  *bool `json:"done,omitempty"`
	Spoke *bool `json:"spoke,omitempty"`
	// Noted: whether one of its notes cites this post as a source.
	Noted *bool `json:"noted,omitempty"`
}

func (s *Server) routingRow(id, reason string) routingRow {
	row := routingRow{ID: id, Name: id, Reason: reason}
	if skill, ok := s.catalogue.Find(id); ok {
		row.Name, row.Icon = skill.Name, skill.Icon
	}
	return row
}

// getRouting shows what the router decided about one post: who it chose, and
// whom it passed over and why. A post the router never answered for has no
// record, which is a different thing from a post it sent to nobody.
func (s *Server) getRouting(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, ok := s.store.Entry(id)
	if !ok || entry.Deleted {
		fail(w, http.StatusNotFound, "見つかりません")
		return
	}
	record, err := s.store.Routing(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if record == nil || (record.EntryText != nil && *record.EntryText != entry.Text) {
		writeJSON(w, http.StatusOK, map[string]any{"recorded": false})
		return
	}
	// A record from before turns were noted reads as finished.
	done := func(id string) bool {
		if record.Done == nil {
			return true
		}
		for _, finished := range record.Done {
			if finished == id {
				return true
			}
		}
		return false
	}
	spoke := map[string]bool{}
	if entries, err := s.store.Timeline(); err == nil {
		for _, other := range timeline.Thread(entries, timeline.RootOf(entry)) {
			if other.Kind == timeline.KindRemark && !other.Deleted && other.At.After(entry.At) {
				spoke[other.Speaker] = true
			}
		}
	}
	delivered := make([]routingRow, 0, len(record.Delivered))
	for _, id := range record.Delivered {
		row := s.routingRow(id, "")
		finished, said, noted := done(id), spoke[id], false
		for _, note := range s.store.Memory(id).Notes {
			for _, source := range store.NoteSources(note.Text) {
				if source.EntryID == entry.ID {
					noted = true
				}
			}
		}
		row.Done, row.Spoke, row.Noted = &finished, &said, &noted
		delivered = append(delivered, row)
	}
	excluded := make([]routingRow, 0, len(record.Excluded))
	for id, reason := range record.Excluded {
		excluded = append(excluded, s.routingRow(id, reason))
	}
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].ID < excluded[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true, "delivered": delivered, "excluded": excluded})
}

// checkRouting runs the router over a post again for one persona, without
// delivering anything. It is how a person who has just changed a persona's
// conditions finds out whether the change did what they meant.
func (s *Server) checkRouting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Persona string `json:"persona"`
	}
	if err := readJSON(r, &body); err != nil || body.Persona == "" {
		fail(w, http.StatusBadRequest, "担当を指定してください")
		return
	}
	check, err := s.runner.CheckRouting(r.Context(), r.PathValue("id"), body.Persona)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, check)
}
