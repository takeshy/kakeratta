package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"time"
)

// A persona keeps up to maxNotes notes and never stops adding to them, so the
// page that shows them reads them a page at a time, the most recently written
// first: what was written lately is what is looked for. The cursor is the last
// note on the page.

// MemoryPage is one page of a persona's notes. Paths names every note the
// persona keeps, so a link to a note on a later page still resolves; it is
// only sent with the first page.
type MemoryPage struct {
	Memory
	Paths []string `json:"paths,omitempty"`
	Next  string   `json:"next,omitempty"`
}

var ErrMemoryCursor = errors.New("メモの続き位置が不正です")

type noteCursor struct {
	Written time.Time `json:"w"`
	Path    string    `json:"p"`
}

func decodeNoteCursor(raw string) (noteCursor, error) {
	var c noteCursor
	if raw == "" {
		return c, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(data, &c) != nil || c.Path == "" {
		return c, ErrMemoryCursor
	}
	return c, nil
}

func encodeNoteCursor(note Note) string {
	raw, _ := json.Marshal(noteCursor{Written: note.Written, Path: note.Path})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// newerNote says whether a comes before b on a page: written later, or, at
// the same moment, later in name order.
func newerNote(a, b Note) bool {
	if a.Written.Equal(b.Written) {
		return a.Path > b.Path
	}
	return a.Written.After(b.Written)
}

// notesPage takes up to limit notes from notes, which come newest first, that
// match accepts (nil accepts every note). next is the cursor to continue
// after, and is empty when no matching note is left: notes is read until one
// more match than fits on the page has been seen.
func notesPage(notes iter.Seq[Note], limit int, match func(Note) bool) (page []Note, next string) {
	for note := range notes {
		if match != nil && !match(note) {
			continue
		}
		if len(page) == limit {
			return page, encodeNoteCursor(page[len(page)-1])
		}
		page = append(page, note)
	}
	return page, ""
}
