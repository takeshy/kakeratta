package persona

import (
	"context"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// notesTool is a persona reaching into its own notes.
//
// It reports whether it took the call, so anything it does not recognise falls
// through to the capabilities the skill declared. The split is the point: a
// declared tool goes outside, and these do not go anywhere.
//
// The host carries text without interpreting it, and retains source links
// when a persona reads and consolidates its notes.
func (r *Runner) notesTool(ctx context.Context, personaID, name string, args map[string]any, prov *provenance) (string, bool) {
	switch name {
	case "notes_list":
		return r.listNotesPage(personaID, stringOf(args["tag"]), stringOf(args["after"])), true

	case "notes_read":
		wanted := stringsOf(args["paths"])
		if single := stringOf(args["path"]); single != "" {
			wanted = append(wanted, single)
		}
		if len(wanted) == 0 {
			return "paths がありません。", true
		}
		var parts []string
		for _, path := range wanted {
			note, ok := r.store.Note(personaID, path)
			if !ok {
				parts = append(parts, "=== "+path+" ===\n（ありません）")
				continue
			}
			parts = append(parts, "=== "+note.Path+" ===\n"+note.Text)
			prov.noteRead(note.Path, note.Text)
		}
		return strings.Join(parts, "\n\n"), true

	case "notes_write":
		if err := ctx.Err(); err != nil {
			return "書けませんでした（処理が中断されました）: " + err.Error(), false
		}
		path, text := stringOf(args["path"]), repairFrontmatter(repairNewlines(stringOf(args["text"])))
		if path == "" || strings.TrimSpace(text) == "" {
			return "path と text が要ります。", true
		}
		if why := duplicateNote(r.siblingNotes(personaID, path), path, text); why != "" {
			return "書きませんでした: " + why, true
		}
		if skill, ok := r.skills(ctx).Find(personaID); ok {
			if why := typeNotAllowed(skill.forTurn().Memory, path, text); why != "" {
				return "書きませんでした: " + why, true
			}
		}
		if err := r.store.WriteNote(personaID, path, text, prov.forWrite(path, text)...); err != nil {
			return "書けませんでした: " + err.Error(), true
		}
		prov.noteWritten(path)
		return "書きました: " + path, true

	case "notes_forget":
		if err := ctx.Err(); err != nil {
			return "消せませんでした（処理が中断されました）: " + err.Error(), false
		}
		path := stringOf(args["path"])
		if path == "" {
			return "path がありません。", true
		}
		if err := r.store.DeleteNote(personaID, path); err != nil {
			return "捨てられませんでした: " + err.Error(), true
		}
		// A note thrown away leaves its posts to the notes written this turn
		// in its place, so a consolidation keeps the trail to the originals.
		if sources, to := prov.passOn(path); len(to) > 0 {
			for _, written := range to {
				if note, ok := r.store.Note(personaID, written); ok {
					_ = r.store.WriteNote(personaID, written, note.Text, sources...)
				}
			}
		}
		prov.noteForgotten(path)
		return "捨てました: " + path, true
	}
	return "", false
}

// repairNewlines undoes a model that escaped its own line breaks. A note whose
// text has no real newline but plenty of "\n" is unreadable in the memory view
// and unparseable as OKF, and the persona cannot see the difference from where
// it sits.
func repairNewlines(text string) string {
	// Trimmed, because a blank line around an otherwise unbroken note would
	// otherwise pass for a line break and leave the damage in place.
	body := strings.TrimSpace(text)
	if strings.Contains(body, "\n") || !strings.Contains(body, `\n`) {
		return text
	}
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t").Replace(body)
}

// repairFrontmatter mends the two ways a model breaks a frontmatter block
// without noticing: indenting it, so it reads as prose and the type is lost;
// and gluing the closing fence to the last value ("+09:00---") or to the first
// line of the body ("---基本情報"), so the block never closes and the whole
// note reads as one broken header. A live run kept a wine that way.
func repairFrontmatter(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return text
	}
	lines[0] = "---"
	out := []string{"---"}
	closed := false
	for i := 1; i < len(lines); i++ {
		line := strings.TrimLeft(lines[i], " \t")
		if closed {
			out = append(out, lines[i])
			continue
		}
		switch {
		case line == "---":
			closed = true
		case strings.HasSuffix(line, "---") && len(line) > 3 && !strings.HasPrefix(line, "---"):
			// The fence at the end of a value: the value keeps its line.
			line = strings.TrimRight(strings.TrimSuffix(line, "---"), " \t")
			out = append(out, line)
			line = "---"
			closed = true
		case strings.HasPrefix(line, "---") && len(line) > 3:
			// The fence at the start of the body: the body keeps its line.
			out = append(out, "---")
			line = strings.TrimLeft(strings.TrimPrefix(line, "---"), " \t")
			closed = true
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// notesPageSize is how many notes one notes_list shows. The index is the
// persona's way into its notes; the listing is for looking when it is not.
const notesPageSize = 100

// noteCount tells a waking persona how many notes it keeps, by name alone.
func (r *Runner) noteCount(personaID string) string {
	paths, _ := r.store.NotePaths(personaID)
	if len(paths) == 0 {
		return "あなたのメモ: まだ何もありません"
	}
	return fmt.Sprintf("あなたのメモ: %d件（一覧は渡しません。index.md から辿り、中身は notes_read で読みます）", len(paths))
}

// listNotesPage is one page of the listing, narrowed to one tag or not, the
// most recently written first, continuing after the cursor after.
func (r *Runner) listNotesPage(personaID, tag, after string) string {
	var match func(store.Note) bool
	if tag != "" {
		match = func(note store.Note) bool { return hasTag(noteTags(note.Text), tag) }
	}
	page, err := r.store.MemoryPage(personaID, after, notesPageSize, match)
	if err != nil {
		return "一覧を読めませんでした: " + err.Error()
	}
	if len(page.Notes) == 0 {
		switch {
		case tag != "":
			return "タグ「" + tag + "」のメモはありません。"
		case after != "":
			return "これより後のメモはありません。"
		}
		return "（まだ何もありません）"
	}
	listing := listNotes(page.Notes)
	if page.Next != "" {
		more := map[string]string{"after": page.Next}
		if tag != "" {
			more["tag"] = tag
		}
		args, _ := json.Marshal(more)
		listing += "\n（続きがあります。notes_list の args に " + string(args) + " を書くと次の" + strconv.Itoa(notesPageSize) + "件です）"
	}
	return listing
}

// listNotes is one line per note. Each line carries the note's tags, so a
// persona can pick what to read by them without the index having said so.
func listNotes(notes []store.Note) string {
	var lines []string
	for _, note := range notes {
		line := fmt.Sprintf("- %s（%d字）", note.Path, len([]rune(note.Text)))
		if !note.Written.IsZero() {
			line += " updated: " + note.Written.Format(time.RFC3339)
		}
		if tags := noteTags(note.Text); len(tags) > 0 {
			line += " tags: " + strings.Join(tags, ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// extendReferenced moves the dates of the notes a persona read while
// answering a post, since they are still in use. A note the turn itself
// wrote or forgot is left to what the persona did with it.
func (r *Runner) extendReferenced(personaID string, referenced map[string]bool, steps []stepRecord, zone *time.Location) {
	touched := map[string]bool{}
	for _, step := range steps {
		if step.Path != "" {
			touched[strings.TrimPrefix(step.Path, "/")] = true
		}
	}
	now := time.Now()
	for path := range referenced {
		note, ok := r.store.Note(personaID, strings.TrimPrefix(path, "/"))
		if !ok || touched[note.Path] || reservedNote(note.Path) {
			continue
		}
		text, moved := extendedDates(note.Text, note.Written, now, zone)
		if !moved {
			continue
		}
		if err := r.store.WriteNote(personaID, note.Path, text); err != nil {
			r.emit("error", map[string]any{"where": "extend", "persona": personaID, "message": err.Error()})
		}
	}
}

// siblingNotes are the notes filed in the same directory as path: where a
// second note about the same thing would be put. Only they are read.
func (r *Runner) siblingNotes(personaID, path string) []store.Note {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	paths, err := r.store.NotePaths(personaID)
	if err != nil {
		return nil
	}
	var near []string
	for _, other := range paths {
		if other != path && pathpkg.Dir(other) == pathpkg.Dir(path) {
			near = append(near, other)
		}
	}
	notes, _ := r.store.Notes(personaID, near)
	return notes
}
