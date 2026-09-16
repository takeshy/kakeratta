package store

import (
	"net/url"
	"regexp"
	"strings"
)

// NoteSource records which timeline entries were involved in writing a note.
// These are provenance, not a claim that every entry proves every statement.
// Stored as Markdown inside OKF so exports retain the links too.
type NoteSource struct {
	EntryID string
	Label   string
}

const noteSourcesStart = "<!-- kakeratta:note-sources -->"
const noteSourcesEnd = "<!-- /kakeratta:note-sources -->"

var noteSourceLink = regexp.MustCompile(`(?m)^\* \[([^\n]*)\]\(/\?entry=([^\s)]+)\)$`)

func splitNoteSources(text string) (string, []NoteSource) {
	start := strings.LastIndex(text, noteSourcesStart)
	if start < 0 {
		return text, nil
	}
	end := strings.Index(text[start:], noteSourcesEnd)
	if end < 0 {
		return text, nil
	}
	end += start
	var sources []NoteSource
	for _, match := range noteSourceLink.FindAllStringSubmatch(text[start:end], -1) {
		id, err := url.QueryUnescape(match[2])
		if err == nil && id != "" {
			sources = append(sources, NoteSource{EntryID: id, Label: match[1]})
		}
	}
	return strings.TrimSpace(text[:start] + text[end+len(noteSourcesEnd):]), sources
}

// NoteSources lets consolidation carry forward the provenance of notes read.
func NoteSources(text string) []NoteSource {
	_, sources := splitNoteSources(text)
	return sources
}

// withoutNoteSource drops one entry from the block the app manages. The body is
// returned exactly as the persona wrote it: this block is the app's own, and a
// link to a post that no longer exists is the app's own stale data.
func withoutNoteSource(text, entryID string) (string, bool) {
	body, sources := splitNoteSources(text)
	kept := make([]NoteSource, 0, len(sources))
	found := false
	for _, source := range sources {
		if source.EntryID == entryID {
			found = true
			continue
		}
		kept = append(kept, source)
	}
	if !found {
		return text, false
	}
	return withNoteSources("", body, kept), true
}

func withNoteSources(previous, next string, added []NoteSource) string {
	_, old := splitNoteSources(previous)
	body, _ := splitNoteSources(next)
	// Ignore any rewritten managed block: only retained and server-provided IDs
	// become automatic provenance. Ordinary persona-authored links remain intact.
	sources := append(old, added...)
	seen := map[string]bool{}
	var lines []string
	for _, source := range sources {
		if source.EntryID == "" || seen[source.EntryID] {
			continue
		}
		seen[source.EntryID] = true
		label := strings.Join(strings.Fields(source.Label), " ")
		label = strings.NewReplacer("[", "（", "]", "）", "<", "〈", ">", "〉").Replace(label)
		if label == "" {
			label = "投稿"
		}
		lines = append(lines, "* ["+label+"](/?entry="+url.QueryEscape(source.EntryID)+")")
	}
	if len(lines) == 0 {
		return body
	}
	return strings.TrimSpace(body) + "\n\n" + noteSourcesStart + "\n# 記憶を更新した投稿\n\n" + strings.Join(lines, "\n") + "\n" + noteSourcesEnd + "\n"
}
