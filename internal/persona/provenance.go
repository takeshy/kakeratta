package persona

import (
	"strings"

	"github.com/takeshy/kakeratta/internal/store"
)

// provenance is where the notes a turn writes get their links to posts from.
//
// A note written on a post cites that post. A note that builds on another -
// rewrites it, links to it, or replaces it after throwing it away - inherits
// that note's posts too. Nothing else does: a live run read an old tasting
// note for context and every note it wrote that turn came out citing the old
// wine's post, which is a link to nowhere for the person reading it.
type provenance struct {
	// own is the post this turn is about, if any.
	own []store.NoteSource
	// read holds the sources of the notes read this turn, by path.
	read map[string][]store.NoteSource
	// forgotten are the notes thrown away this turn; written are the notes
	// written, so a note thrown away later can still pass its posts on.
	forgotten map[string]bool
	written   map[string]bool
}

func newProvenance(own ...store.NoteSource) *provenance {
	return &provenance{own: own, read: map[string][]store.NoteSource{}, forgotten: map[string]bool{}, written: map[string]bool{}}
}

// reservedNote is a note the app names and every turn rewrites: the index and
// the log. Neither is about any one post, so neither carries post links.
func reservedNote(path string) bool {
	return path == "index.md" || path == "log.md"
}

func (p *provenance) noteRead(path, text string) {
	if p == nil {
		return
	}
	p.read[path] = store.NoteSources(text)
}

func (p *provenance) noteWritten(path string) {
	if p == nil {
		return
	}
	p.written[path] = true
}

func (p *provenance) noteForgotten(path string) {
	if p == nil {
		return
	}
	p.forgotten[path] = true
}

// forWrite is what a note written at path, with this text, may cite: the turn's
// own post, and the posts behind any read note it rewrites, links to, or that
// has been thrown away this turn.
func (p *provenance) forWrite(path, text string) []store.NoteSource {
	if p == nil || reservedNote(path) {
		return nil
	}
	out := append([]store.NoteSource{}, p.own...)
	for read, sources := range p.read {
		if read == path || p.forgotten[read] || mentions(text, read) {
			out = append(out, sources...)
		}
	}
	return out
}

// passOn is what a note thrown away leaves to the notes written earlier this
// turn: its posts, for the notes that took its place.
func (p *provenance) passOn(path string) (sources []store.NoteSource, to []string) {
	if p == nil {
		return nil, nil
	}
	sources = p.read[path]
	if len(sources) == 0 {
		return nil, nil
	}
	for written := range p.written {
		if written != path && !reservedNote(written) {
			to = append(to, written)
		}
	}
	return sources, to
}

// mentions reports whether a note's text links to another note, by its path
// with or without the .md.
func mentions(text, path string) bool {
	if path == "" {
		return false
	}
	return strings.Contains(text, path) || strings.Contains(text, strings.TrimSuffix(path, ".md"))
}
