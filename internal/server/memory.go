package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
)

// getMemory shows what a persona has kept. It is the persona's own text, and
// seeing it is the only way to tell what it decided was worth keeping.
//
// They are the persona's field summaries and the monthly records of its
// kakera. The records only ever grow, so the files come a page at a time, the
// most recently written first: ?after= is the next cursor of the page already
// shown, and ?tag= keeps only the records files that hold a record with that
// tag.
func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 || limit > maxMemoryPage {
		limit = defaultMemoryPage
	}
	var match func(store.Note) bool
	if tag := strings.TrimPrefix(strings.TrimSpace(query.Get("tag")), "#"); tag != "" {
		match = func(note store.Note) bool { return persona.RecordsTagged(note.Path, note.Text, tag) }
	}
	page, err := s.store.MemoryPage(r.PathValue("id"), query.Get("after"), limit, match)
	if errors.Is(err, store.ErrMemoryCursor) {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

const (
	defaultMemoryPage = 50
	maxMemoryPage     = 200
)

// getNote is one note by name: a link in a note can point at one on a page
// not read yet.
func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	note, ok := s.store.Note(r.PathValue("id"), r.URL.Query().Get("path"))
	if !ok {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// A separate route ensures a missing note path can never erase all memory.
func (s *Server) forgetNote(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		fail(w, http.StatusBadRequest, "メモを指定してください")
		return
	}
	note, ok := s.store.Note(r.PathValue("id"), path)
	if !ok || note.Path != path {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	if err := s.store.DeleteNote(r.PathValue("id"), path); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A field deleted starts again from the next kakera; a month of records
	// deleted is gone. Nothing else rests on either, so nobody is woken.
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// editNote replaces the body of one note with what the user typed. A persona's
// summaries are its own, but a misread label is a misread label: when the text
// a persona was given was wrong, the person who knows can put it right without
// waiting for the persona to be told. The persona is not woken.
func (s *Server) editNote(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		fail(w, http.StatusBadRequest, "メモを指定してください")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "メモの本文を入力してください")
		return
	}
	id := r.PathValue("id")
	note, ok := s.store.Note(id, path)
	if !ok || note.Path != path {
		fail(w, http.StatusNotFound, "メモが見つかりません")
		return
	}
	if err := s.store.WriteNote(id, path, body.Text); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, _ := s.store.Note(id, path)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) forgetMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForgetMemory(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Nothing is left to reconcile, so the persona is not woken. The schedules
	// are the app's own bookkeeping and go with the memory they served.
	if err := s.store.DeleteSchedules(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
