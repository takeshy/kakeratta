package persona

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// recordsMonth is a month a records read may be narrowed to.
var recordsMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)

// readRecords is a persona looking through its own records: newest first,
// narrowed by tag, by words, and to before a month. The fields are handed over
// whole at the start of every turn, so the records are all there is to read.
// It also returns the ids of the records it showed, or of the one whose entry
// it read: those records are in use.
func (r *Runner) readRecords(personaID string, args map[string]any, zone *time.Location) (string, []string) {
	if id := strings.TrimSpace(stringOf(args["id"])); id != "" {
		return r.recordEntry(id, zone), []string{id}
	}
	tag, query, before := stringOf(args["tag"]), stringOf(args["query"]), strings.TrimSpace(stringOf(args["before"]))
	if before != "" && !recordsMonth.MatchString(before) {
		return "before は 2026-09 のような年月で書いてください。", nil
	}
	found, more := r.findRecords(personaID, tag, query, before, recordsPageSize, zone)
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
	return text + "\n（[ ] の投稿そのものは、records の args に {\"id\":\"…\"} を書くと読めます）", ids
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
