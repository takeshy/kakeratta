package persona

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// A persona's memory is two things, both kept by the app:
//
//   - One body per field (分野) - the kinds its memory layout names - holding
//     everything learned in that field so far, rewritten whole and kept short.
//     A field file is "{type}.md"; it is plain text, not OKF.
//   - A record of every kakera the persona took in: one sentence, its tags,
//     and the entry to go back to - the persona's reply when it replied, the
//     kakera itself when it did not. A reply leads to its kakera through
//     ReplyTo. Records are "records/YYYY-MM.md", one line each:
//     "- 2026-10-01 12:34 [e_x](/?entry=e_x) sentence #tag #tag", then any
//     dates (recordDate): a record is what index.md's line was, and like
//     it, it may be set to go.
//
// The persona decides what the bodies say and how a kakera is summed up; the
// app writes both, so nothing else ever ends up in memory.

// maxFieldRunes is the most a field's body may hold.
const maxFieldRunes = 2000

// maxSummaryRunes and maxTags bound one record.
const (
	maxSummaryRunes = 140
	maxTags         = 5
	maxTagRunes     = 20
)

// recordsDir holds the monthly record files.
const recordsDir = "records/"

// defaultField is the one field of a persona whose layout names none.
const defaultField = "Memo"

// memoryFields are the fields a persona keeps, in the order its layout names
// them.
func memoryFields(skill Skill) []string {
	if fields := allowedTypes(skill.forTurn().Memory); len(fields) > 0 {
		return fields
	}
	return []string{defaultField}
}

// fieldPath is where a field's body is kept.
func fieldPath(field string) string {
	return field + ".md"
}

// fieldNamed finds the field a model meant, whatever case it wrote it in.
func fieldNamed(fields []string, name string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".md")
	for _, field := range fields {
		if strings.EqualFold(field, name) {
			return field, true
		}
	}
	return "", false
}

func recordsPath(at time.Time) string {
	return recordsDir + at.Format("2006-01") + ".md"
}

func isRecordsPath(path string) bool {
	return strings.HasPrefix(strings.TrimPrefix(path, "/"), recordsDir)
}

// record is one kakera, as the persona summed it up.
type record struct {
	At      time.Time
	ID      string
	Summary string
	Tags    []string
	// Expires is when the app deletes the record: something only the persona
	// itself brought up, kept for a day or so. Review is when the persona
	// asks the user whether it can be forgotten: something the user said whose
	// lifetime nobody knows yet. Either may be unset.
	Expires, Review recordDate
	// Asked is the persona's post that asked about the record under review,
	// so the user's reply to that post is known to be about it.
	Asked string
	// Of is the kakera a record kept under a reply is of, so a search that
	// finds the kakera finds its record.
	Of string
}

// recordDate is a day a record is set to, and the span it was given then,
// in days: reading the record again in use gives it that span afresh.
type recordDate struct {
	Day  time.Time
	Days int
}

func (d recordDate) set() bool { return !d.Day.IsZero() }

func (d recordDate) token(key string, zone *time.Location) string {
	if !d.set() {
		return ""
	}
	return fmt.Sprintf(" %s:%s/%dd", key, d.Day.In(zone).Format("2006-01-02"), d.Days)
}

// dateFrom is a day a model wrote ("2026-11-01"), with its span counted from
// since; "" or a day that does not parse is no date.
func dateFrom(raw string, since time.Time, zone *time.Location) recordDate {
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), zone)
	if err != nil {
		return recordDate{}
	}
	from := since.In(zone)
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, zone)
	days := int(day.Sub(start).Hours()/24 + 0.5)
	if days < 1 {
		days = 1
	}
	return recordDate{Day: day, Days: days}
}

// extended is the date moved so its span starts again today, and whether it
// moved: reading never brings a date closer.
func (d recordDate) extended(now time.Time, zone *time.Location) (recordDate, bool) {
	if !d.set() || d.Days < 1 {
		return d, false
	}
	today := now.In(zone)
	moved := time.Date(today.Year(), today.Month(), today.Day()+d.Days, 0, 0, 0, 0, zone)
	if !moved.After(d.Day) {
		return d, false
	}
	return recordDate{Day: moved, Days: d.Days}, true
}

var recordLine = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) \[([^\]\s]+)\](?:\(/\?entry=[^)\s]*\))? (.*)$`)

// recordTrailer is one trailing token of a record line: a tag, a date, or
// the post that asked about it.
var recordTrailer = regexp.MustCompile(`\s(?:#(\S+)|(expires|review):(\d{4}-\d{2}-\d{2})/(\d+)d|asked:(\S+)|of:(\S+))$`)

// line is a record as a persona is shown it: the id bare.
func (rec record) line(zone *time.Location) string {
	return rec.format(zone, "["+rec.ID+"]")
}

// fileLine is a record as it is kept: the id a link to the entry, so the
// memory view opens it.
func (rec record) fileLine(zone *time.Location) string {
	return rec.format(zone, "["+rec.ID+"](/?entry="+url.QueryEscape(rec.ID)+")")
}

func (rec record) format(zone *time.Location, id string) string {
	line := "- " + rec.At.In(zone).Format("2006-01-02 15:04") + " " + id + " " + rec.Summary
	for _, tag := range rec.Tags {
		line += " #" + tag
	}
	line += rec.Expires.token("expires", zone) + rec.Review.token("review", zone)
	if rec.Asked != "" {
		line += " asked:" + rec.Asked
	}
	if rec.Of != "" && rec.Of != rec.ID {
		line += " of:" + rec.Of
	}
	return line
}

// parseRecords reads the lines of a records file. A line that is not a record
// is left out.
func parseRecords(text string, zone *time.Location) []record {
	var out []record
	for _, line := range strings.Split(text, "\n") {
		match := recordLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", match[1], zone)
		if err != nil {
			continue
		}
		rec := record{At: at, ID: match[2]}
		rest := match[3]
		for {
			found := recordTrailer.FindStringSubmatch(rest)
			if found == nil {
				break
			}
			switch {
			case found[1] != "":
				rec.Tags = append([]string{found[1]}, rec.Tags...)
			case found[2] != "":
				day, err := time.ParseInLocation("2006-01-02", found[3], zone)
				days, _ := strconv.Atoi(found[4])
				if err == nil {
					if found[2] == "expires" {
						rec.Expires = recordDate{Day: day, Days: days}
					} else {
						rec.Review = recordDate{Day: day, Days: days}
					}
				}
			case found[5] != "":
				rec.Asked = found[5]
			default:
				rec.Of = found[6]
			}
			rest = rest[:len(rest)-len(found[0])]
		}
		rec.Summary = strings.TrimSpace(rest)
		out = append(out, rec)
	}
	return out
}

// recordsText is a records file holding these records, oldest first.
func recordsText(month string, records []record, zone *time.Location) string {
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })
	lines := []string{"# " + month + " のかけら", ""}
	for _, rec := range records {
		lines = append(lines, rec.fileLine(zone))
	}
	return strings.Join(lines, "\n") + "\n"
}

// cleanSummary makes a model's sentence fit one record line.
// A "#" it holds would read back as a tag, so it is made full-width.
func cleanSummary(text string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(text), " "), "#", "＃")
}

// cleanTag makes a model's tag one word that a record line can carry.
func cleanTag(tag string) string {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
	return strings.Join(strings.Fields(tag), "_")
}

// recordsPaths are a persona's records files, newest month first.
func (r *Runner) recordsPaths(personaID string) []string {
	paths, _ := r.store.NotePaths(personaID)
	var out []string
	for _, path := range paths {
		if isRecordsPath(path) {
			out = append(out, path)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// findRecords are the records that match, newest first, at most limit: those
// carrying tag when one is given, holding every word of query when one is
// given, and from before the month before when one is given (YYYY-MM).
func (r *Runner) findRecords(personaID, tag, query, before string, limit int, zone *time.Location) (found []record, more bool) {
	words := strings.Fields(strings.ToLower(query))
	return r.findRecordsWhere(personaID, tag, before, limit, zone, func(rec record) bool { return holdsAll(rec, words) })
}

// findRecordsWhere is findRecords with any test of a record's text.
func (r *Runner) findRecordsWhere(personaID, tag, before string, limit int, zone *time.Location, match func(record) bool) (found []record, more bool) {
	tag = strings.ToLower(cleanTag(tag))
	for _, path := range r.recordsPaths(personaID) {
		month := strings.TrimSuffix(strings.TrimPrefix(path, recordsDir), ".md")
		if before != "" && month >= before {
			continue
		}
		note, ok := r.store.Note(personaID, path)
		if !ok {
			continue
		}
		records := parseRecords(note.Text, zone)
		for i := len(records) - 1; i >= 0; i-- {
			rec := records[i]
			if tag != "" && !hasRecordTag(rec, tag) {
				continue
			}
			if !match(rec) {
				continue
			}
			if len(found) == limit {
				return found, true
			}
			found = append(found, rec)
		}
	}
	return found, false
}

func hasRecordTag(rec record, tag string) bool {
	for _, have := range rec.Tags {
		if strings.ToLower(have) == tag {
			return true
		}
	}
	return false
}

func holdsAll(rec record, words []string) bool {
	text := strings.ToLower(rec.Summary + " " + strings.Join(rec.Tags, " "))
	for _, word := range words {
		if !strings.Contains(text, strings.TrimPrefix(word, "#")) {
			return false
		}
	}
	return true
}

// recentRecordsLimit is how many records a turn is handed without asking.
const recentRecordsLimit = 15

// recordsPageSize is how many records one records read returns.
const recordsPageSize = 30

// listRecords is records, one line each, as a persona is shown them.
func listRecords(records []record, zone *time.Location) string {
	if len(records) == 0 {
		return "（まだありません）"
	}
	lines := make([]string, 0, len(records))
	for _, rec := range records {
		lines = append(lines, rec.line(zone))
	}
	return strings.Join(lines, "\n")
}

// memoryView is what a persona is handed of its memory at the start of every
// turn: its facts that hold now, and its latest records.
func (r *Runner) memoryView(skill Skill, zone *time.Location) string {
	recent, more := r.findRecords(skill.ID, "", "", "", recentRecordsLimit, zone)
	text := "### 分野ごとの事実（いま当てはまるもの。[ ] は事実の id）\n" + renderFacts(r.loadFacts(skill, zone), zone) +
		"\n\n### かけらの記録（新しい順、最新" + fmt.Sprint(recentRecordsLimit) + "件まで。[ ] はたどれる投稿のID）\n" + listRecords(recent, zone)
	if more {
		text += "\n（もっと前の記録は read の records で、tag・query・before を指定して探せます）"
	}
	return text
}

// proseFields are the field files a persona kept as prose, before fields
// were lists of facts: a field-shaped file with text and no fact line.
func (r *Runner) proseFields(skill Skill) ([]store.Note, error) {
	paths, err := r.store.NotePaths(skill.ID)
	if err != nil {
		return nil, err
	}
	var out []store.Note
	for _, path := range paths {
		if !fieldFile.MatchString(path) {
			continue
		}
		note, ok := r.store.Note(skill.ID, path)
		if !ok {
			continue
		}
		if _, prose := proseText(note.Text); prose {
			out = append(out, note)
		}
	}
	return out, nil
}

// eachRecord passes every record a persona keeps to change, month by month,
// and writes back the months it changed. change reports whether to keep the
// record and whether it changed it. A month left without records is deleted.
// The caller holds the persona's keeper lock; writes stop if ctx ends.
func (r *Runner) eachRecord(ctx context.Context, personaID string, zone *time.Location, change func(rec *record) (keep, changed bool)) error {
	for _, path := range r.recordsPaths(personaID) {
		note, ok := r.store.Note(personaID, path)
		if !ok {
			continue
		}
		records := parseRecords(note.Text, zone)
		kept := records[:0]
		touched := false
		for i := range records {
			rec := records[i]
			keep, changed := change(&rec)
			if changed || !keep {
				touched = true
			}
			if keep {
				kept = append(kept, rec)
			}
		}
		if !touched {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if len(kept) == 0 {
			err = r.store.DeleteNote(personaID, path)
		} else {
			err = r.store.WriteNote(personaID, path, recordsText(strings.TrimSuffix(strings.TrimPrefix(path, recordsDir), ".md"), kept, zone))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// extendRecords gives the records a persona read while answering a post their
// span afresh: they are still in use.
func (r *Runner) extendRecords(ctx context.Context, personaID string, ids map[string]bool, now time.Time, zone *time.Location) error {
	if len(ids) == 0 {
		return nil
	}
	held, release, err := r.lockKeeper(ctx, personaID)
	if err != nil {
		return err
	}
	defer release()
	return r.eachRecord(held, personaID, zone, func(rec *record) (bool, bool) {
		if !ids[rec.ID] {
			return true, false
		}
		expires, a := rec.Expires.extended(now, zone)
		review, b := rec.Review.extended(now, zone)
		rec.Expires, rec.Review = expires, review
		return true, a || b
	})
}

// recordsByID are the records kept under these ids, newest first.
func (r *Runner) recordsByID(personaID string, ids map[string]bool, zone *time.Location) []record {
	if len(ids) == 0 {
		return nil
	}
	var out []record
	for _, path := range r.recordsPaths(personaID) {
		note, ok := r.store.Note(personaID, path)
		if !ok {
			continue
		}
		records := parseRecords(note.Text, zone)
		for i := len(records) - 1; i >= 0; i-- {
			if ids[records[i].ID] {
				out = append(out, records[i])
			}
		}
	}
	return out
}
