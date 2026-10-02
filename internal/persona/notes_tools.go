package persona

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// maxEntryReads is how many posts one records read may open by id.
const maxEntryReads = 10

// recordsMonth is a month a records read may be narrowed to.
var recordsMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)

// readRecords is a persona looking through its own records: newest first,
// narrowed by tag, by words, and to before a month. The fields are handed over
// whole at the start of every turn, so the records are all there is to read.
// It also returns the ids of the records it showed, or of those whose entries
// it read: those records are in use.
func (r *Runner) readRecords(ctx context.Context, personaID string, args map[string]any, zone *time.Location) (string, []string) {
	if ids := entryIDs(args); len(ids) > 0 {
		if len(ids) > maxEntryReads {
			return fmt.Sprintf("一度に読める投稿は%d件までです（%d件指定されました）。分けて読んでください。", maxEntryReads, len(ids)), nil
		}
		var parts []string
		for _, id := range ids {
			parts = append(parts, "=== "+id+" ===\n"+r.recordEntry(id, zone))
		}
		return strings.Join(parts, "\n\n"), ids
	}
	if skill, ok := r.skills(ctx).Find(personaID); ok && !skill.Remembers() {
		return "この担当は記憶を使いません。", nil
	}
	tag, query, before := stringOf(args["tag"]), stringOf(args["query"]), strings.TrimSpace(stringOf(args["before"]))
	if before != "" && !recordsMonth.MatchString(before) {
		return "before は 2026-09 のような年月で書いてください。", nil
	}
	words := strings.Fields(strings.ToLower(query))
	// Words find a record that says them; meaning finds one whose kakera is
	// about them, however the record or its tags were worded.
	similar := r.similarKakera(ctx, query)
	found, more := r.findRecordsWhere(personaID, tag, before, recordsPageSize, zone, func(rec record) bool {
		return holdsAll(rec, words) || similar[rec.ID] || (rec.Of != "" && similar[rec.Of])
	})
	if len(found) == 0 {
		return "合う記録はありません。", nil
	}
	var ids []string
	for _, rec := range found {
		ids = append(ids, rec.ID)
	}
	text := listRecords(found, zone)
	if more {
		last := found[len(found)-1].At.In(zone)
		text += fmt.Sprintf("\n（続きがあります。さらに前は before に %s を指定すると、その月より前から探せます。同じ月の残りは query や tag で絞ってください）", last.Format("2006-01"))
	}
	return text + "\n（[ ] の投稿そのものは、records の args に {\"ids\":[\"…\",\"…\"]} を書くと、10件までまとめて読めます）", ids
}

// entryIDs are the posts a records read asks to open: "ids", "id", or both,
// each once, in the order given.
func entryIDs(args map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range append(stringsOf(args["ids"]), stringOf(args["id"])) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// similarKakera are the posts a meaning search of the timeline finds for
// query, by id. Without a query, or a search to ask, it is empty.
func (r *Runner) similarKakera(ctx context.Context, query string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(query) == "" || r.search == nil {
		return out
	}
	result, err := r.search.Unified(ctx, query, search.Window{}, recordsPageSize, 0, 0.5)
	if err != nil {
		return out
	}
	for _, hit := range result.Hits {
		out[hit.Entry.ID] = true
		if hit.Entry.ReplyTo != "" {
			out[hit.Entry.ReplyTo] = true
		}
	}
	return out
}

// recordEntry is the entry a record leads to, and the kakera it answered when
// it is a reply.
func (r *Runner) recordEntry(id string, zone *time.Location) string {
	entry, ok := r.store.Entry(id)
	if !ok || entry.Deleted {
		return "その投稿はありません（消された可能性があります）: " + id
	}
	text := entry.Render(nil, zone)
	if parent, ok := r.store.Entry(entry.ReplyTo); ok && !parent.Deleted && entry.Speaker != timeline.User {
		text = "返信もと:\n" + parent.Render(nil, zone) + "\n\nこの記録の返信:\n" + text
	}
	return text
}

// RecordsTagged says whether a memory file is a records file holding a record
// with tag, for the memory view's tag filter.
func RecordsTagged(path, text, tag string) bool {
	if !isRecordsPath(path) {
		return false
	}
	tag = strings.ToLower(cleanTag(tag))
	for _, rec := range parseRecords(text, time.UTC) {
		if hasRecordTag(rec, tag) {
			return true
		}
	}
	return false
}
