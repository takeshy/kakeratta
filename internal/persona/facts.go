package persona

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A persona's memory is one file, "Memo.md", kept by the app: a summary in
// prose, the facts that hold now, and the records of the kakera not yet folded
// into them.
//
// A turn only adds a record of its kakera (field_records.go). Once a day the
// persona folds the new records into facts - it adds a fact, rewrites one that
// changed, drops one that no longer holds, or merges several into one - and
// the app applies exactly that (summaries.go). A fact that no longer holds is
// deleted, not kept as history: the kakera it rested on stay in the timeline.
// Every fact carries the kakera it rests on, which is how the app takes out
// what a deleted kakera left behind without asking a model.
//
// Memory is not split by subject. When what a persona keeps plainly falls
// apart into a subject of its own, the daily pass suggests a new persona.
//
//	# Memo
//
//	<!-- kakeratta:checked 2026-10-02T07:12:00+09:00 -->
//
//	## まとめ（2026-10-02 更新）
//
//	三田の二郎がずっと気になっている。…
//
//	## いまの事実
//
//	- [f_ab12cd] 三田の二郎に行きたい from:e_a,e_d since:2026-09-08
//
//	## かけらの記録
//
//	- 2026-10-02 12:30 [e_f](/?entry=e_f) 三田の二郎の行列を見た #ラーメン

// MemoPath is a persona's memory file.
const MemoPath = "Memo.md"

// maxFactRunes is the most one fact may say.
const maxFactRunes = 200

// maxFactsRunes is the most the facts that hold may say together.
const maxFactsRunes = 2000

// The headings of the memory file.
const (
	summaryHeading = "## まとめ"
	factsHeading   = "## いまの事実"
	recordsHeading = "## かけらの記録"
)

var summaryHeadingDay = regexp.MustCompile(`^## まとめ（(\d{4}-\d{2}-\d{2})(?: 更新)?）$`)

type fact struct {
	ID    string
	Text  string
	From  []string
	Since time.Time
	// PastReviewed fingerprints a dated fact explicitly retained as a past event.
	PastReviewed string
}

// memo is a persona's memory file.
type memo struct {
	// Summary is the persona's prose over its memory, and SummaryAt the day
	// it was last written; zero when it never was. SummaryDirty is set when
	// facts or records went since, so the daily pass writes it again.
	Summary      string
	SummaryAt    time.Time
	SummaryDirty bool
	// CheckedAt is when the daily pass last read the records: those kept
	// after it are new to it.
	CheckedAt time.Time
	// SuggestedAt is the day the daily pass last suggested a new persona.
	SuggestedAt time.Time
	Facts       []fact
	Records     []record
}

func (m memo) empty() bool {
	return m.Summary == "" && len(m.Facts) == 0 && len(m.Records) == 0
}

// The app's own markers in the memory file; the memory view hides them.
const summaryDirtyMarker = "<!-- kakeratta:summary-dirty -->"

var (
	checkedMarker   = regexp.MustCompile(`^<!-- kakeratta:checked (\S+) -->$`)
	suggestedMarker = regexp.MustCompile(`^<!-- kakeratta:persona-suggested (\d{4}-\d{2}-\d{2}) -->$`)
)

// reviewFingerprint is what a retained past event was reviewed as: its words
// and the day it began, as written on its line. A new source or another time
// zone leaves it as it was; new words or another start day do not.
func (f fact) reviewFingerprint(zone *time.Location) string {
	sum := sha256.Sum256([]byte(f.Text + "\n" + f.Since.In(zone).Format("2006-01-02")))
	return fmt.Sprintf("%x", sum[:8])
}

var factLine = regexp.MustCompile(`^- \[(f_[A-Za-z0-9]+)\] (.*)$`)
var factTrailer = regexp.MustCompile(`\s(from|since|reviewed):(\S+)$`)

// parseMemo reads the memory file. A line that is none of its parts is left
// out.
func parseMemo(text string, zone *time.Location) memo {
	var out memo
	section := ""
	var summary []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == summaryDirtyMarker:
			out.SummaryDirty = true
			continue
		case checkedMarker.MatchString(line):
			out.CheckedAt, _ = time.Parse(time.RFC3339, checkedMarker.FindStringSubmatch(line)[1])
			continue
		case suggestedMarker.MatchString(line):
			out.SuggestedAt, _ = time.ParseInLocation("2006-01-02", suggestedMarker.FindStringSubmatch(line)[1], zone)
			continue
		case line == summaryHeading || summaryHeadingDay.MatchString(line):
			section = "summary"
			if match := summaryHeadingDay.FindStringSubmatch(line); match != nil {
				out.SummaryAt, _ = time.ParseInLocation("2006-01-02", match[1], zone)
			}
			continue
		case line == factsHeading:
			section = "facts"
			continue
		case line == recordsHeading:
			section = "records"
			continue
		case section == "records":
			out.Records = append(out.Records, parseRecords(line, zone)...)
			continue
		case section == "summary":
			summary = append(summary, strings.TrimRight(raw, " \t"))
			continue
		}
		match := factLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		f := fact{ID: match[1]}
		rest := match[2]
		for {
			found := factTrailer.FindStringSubmatch(rest)
			if found == nil {
				break
			}
			switch found[1] {
			case "from":
				f.From = strings.Split(found[2], ",")
			case "since":
				f.Since, _ = time.ParseInLocation("2006-01-02", found[2], zone)
			case "reviewed":
				f.PastReviewed = found[2]
			}
			rest = rest[:len(rest)-len(found[0])]
		}
		f.Text = strings.TrimSpace(rest)
		out.Facts = append(out.Facts, f)
	}
	out.Summary = strings.TrimSpace(strings.Join(summary, "\n"))
	return out
}

func (f fact) line(zone *time.Location) string {
	line := "- [" + f.ID + "] " + f.Text
	if len(f.From) > 0 {
		line += " from:" + strings.Join(f.From, ",")
	}
	if !f.Since.IsZero() {
		line += " since:" + f.Since.In(zone).Format("2006-01-02")
	}
	if f.PastReviewed != "" && f.PastReviewed == f.reviewFingerprint(zone) {
		line += " reviewed:" + f.PastReviewed
	}
	return line
}

// memoText is the memory file holding m.
func memoText(m memo, zone *time.Location) string {
	lines := []string{"# Memo", ""}
	if !m.CheckedAt.IsZero() {
		lines = append(lines, "<!-- kakeratta:checked "+m.CheckedAt.In(zone).Format(time.RFC3339)+" -->", "")
	}
	if !m.SuggestedAt.IsZero() {
		lines = append(lines, "<!-- kakeratta:persona-suggested "+m.SuggestedAt.In(zone).Format("2006-01-02")+" -->", "")
	}
	if m.SummaryDirty {
		lines = append(lines, summaryDirtyMarker, "")
	}
	if m.Summary != "" {
		head := summaryHeading
		if !m.SummaryAt.IsZero() {
			head += "（" + m.SummaryAt.In(zone).Format("2006-01-02") + " 更新）"
		}
		lines = append(lines, head, "", m.Summary, "")
	}
	if len(m.Facts) > 0 {
		lines = append(lines, factsHeading, "")
		for _, f := range m.Facts {
			lines = append(lines, f.line(zone))
		}
		lines = append(lines, "")
	}
	if len(m.Records) > 0 {
		lines = append(lines, recordsHeading, "")
		for _, rec := range m.Records {
			lines = append(lines, rec.fileLine(zone))
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// loadMemo reads a persona's memory file; none reads as empty.
func (r *Runner) loadMemo(personaID string, zone *time.Location) memo {
	note, _ := r.store.Note(personaID, MemoPath)
	return parseMemo(note.Text, zone)
}

// saveMemo writes a persona's memory file, or deletes it once nothing is
// left. The caller holds the persona's keeper lock; nothing is written once
// ctx has ended, since another keeper may be writing.
func (r *Runner) saveMemo(ctx context.Context, personaID string, m memo, zone *time.Location) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.empty() {
		return r.store.DeleteNote(personaID, MemoPath)
	}
	return r.store.WriteNote(personaID, MemoPath, memoText(m, zone))
}

// repairNewlines undoes a model that escaped its own line breaks: text with no
// real newline but "\n" in it.
func repairNewlines(text string) string {
	if strings.Contains(text, "\n") || !strings.Contains(text, `\n`) {
		return text
	}
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t").Replace(text)
}

// cleanFact makes a model's sentence fit one fact line: one line, and nothing
// that would read back as one of the line's own tokens.
func cleanFact(text string) string {
	text = strings.Join(strings.Fields(repairNewlines(text)), " ")
	for _, mark := range []string{"- ", "・", "* ", "• "} {
		text = strings.TrimSpace(strings.TrimPrefix(text, mark))
	}
	for _, key := range []string{"from", "since", "reviewed"} {
		text = strings.ReplaceAll(text, " "+key+":", " "+key+"：")
	}
	return text
}

// factsRunes is how much the facts say together, which is what a turn is
// handed and what the size limit counts.
func factsRunes(facts []fact) int {
	n := 0
	for _, f := range facts {
		n += len([]rune(f.Text))
	}
	return n
}

func newFactID(taken func(string) bool) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	for {
		raw := make([]byte, 6)
		_, _ = rand.Read(raw)
		for i, b := range raw {
			raw[i] = letters[int(b)%len(letters)]
		}
		id := "f_" + string(raw)
		if !taken(id) {
			return id
		}
	}
}

// renderFacts is the summary and the facts that hold now, as a turn is shown
// them.
func renderFacts(m memo, zone *time.Location) string {
	var lines []string
	if m.Summary != "" {
		lines = append(lines, "まとめ:", m.Summary, "", "事実:")
	}
	for _, f := range m.Facts {
		line := "- [" + f.ID + "] " + f.Text
		if !f.Since.IsZero() {
			line += "（" + f.Since.In(zone).Format("2006-01-02") + "〜）"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "（まだありません）"
	}
	return strings.Join(lines, "\n")
}

// factOp is one change the keeper asks for.
type factOp struct {
	// Op is add, update, invalidate, or merge.
	Op   string   `json:"op"`
	ID   string   `json:"id"`
	IDs  []string `json:"ids"`
	Text string   `json:"text"`
	// From are more kakera the fact rests on: on the daily pass, those of
	// the records the persona folded into it. Set by the host, not the model.
	From []string `json:"-"`
}

// planFacts applies ops to m's facts in memory, and says what is wrong with
// them. The kakera they rest on is from (empty for none). It reports whether
// the facts changed; a change marks the summary dirty.
func planFacts(m memo, ops []factOp, from string, now time.Time, zone *time.Location) (memo, bool, []string) {
	facts := append([]fact{}, m.Facts...)
	var problems []string
	changed := false
	used := map[string]bool{}
	today := now.In(zone)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone)
	taken := func(id string) bool {
		for _, f := range facts {
			if f.ID == id {
				return true
			}
		}
		return false
	}
	sources := func(ids ...[]string) []string {
		seen := map[string]bool{}
		var out []string
		for _, list := range ids {
			for _, id := range list {
				if id != "" && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
		return out
	}
	// take removes a fact and returns it.
	take := func(at, id string) (fact, bool) {
		if used[id] {
			problems = append(problems, at+": "+id+" はこの提出ですでに扱っています")
			return fact{}, false
		}
		used[id] = true
		for i, f := range facts {
			if f.ID == id {
				facts = append(facts[:i:i], facts[i+1:]...)
				changed = true
				return f, true
			}
		}
		problems = append(problems, at+": "+id+" はいまの事実にありません")
		return fact{}, false
	}
	text := func(at string, raw string) (string, bool) {
		t := cleanFact(raw)
		switch {
		case t == "":
			problems = append(problems, at+": text が空です")
			return "", false
		case len([]rune(t)) > maxFactRunes:
			problems = append(problems, fmt.Sprintf("%s: text が%d字あります。%d字以内の1件にしてください", at, len([]rune(t)), maxFactRunes))
			return "", false
		}
		return t, true
	}
	for i, op := range ops {
		at := fmt.Sprintf("facts[%d]（%s）", i, op.Op)
		switch op.Op {
		case "add":
			if t, ok := text(at, op.Text); ok {
				facts = append(facts, fact{ID: newFactID(taken), Text: t, From: sources(op.From, []string{from}), Since: day})
				changed = true
			}
		case "update":
			old, ok := take(at, op.ID)
			if !ok {
				continue
			}
			t, good := text(at, op.Text)
			if !good {
				facts = append(facts, old)
				continue
			}
			// The fact keeps its id; what it said before is not kept.
			f := fact{ID: old.ID, Text: t, From: sources(old.From, op.From, []string{from}), Since: day}
			if t == old.Text {
				// Confirmed again: kept as it was.
				f.Since, f.PastReviewed = old.Since, old.PastReviewed
			}
			facts = append(facts, f)
		case "invalidate":
			take(at, op.ID)
		case "merge":
			if len(op.IDs) < 2 {
				problems = append(problems, at+": ids に2件以上を書いてください")
				continue
			}
			t, good := text(at, op.Text)
			from := op.From
			var since time.Time
			for _, id := range op.IDs {
				old, found := take(at, id)
				if !found {
					continue
				}
				from = sources(from, old.From)
				if since.IsZero() || (!old.Since.IsZero() && old.Since.Before(since)) {
					since = old.Since
				}
			}
			if good {
				facts = append(facts, fact{ID: newFactID(taken), Text: t, From: from, Since: since})
			}
		default:
			problems = append(problems, at+": op は add / update / invalidate / merge のどれかです")
		}
	}
	// Facts over the limit already are not made to shrink at once, only not
	// to grow.
	if n := factsRunes(facts); changed && n > maxFactsRunes && n > factsRunes(m.Facts) {
		problems = append(problems, fmt.Sprintf("いまの事実が合わせて%d字になります。%d字以内になるよう、似た事実を merge でまとめてください", n, maxFactsRunes))
	}
	m.Facts = facts
	if changed {
		m = dirtySummary(m)
	}
	return m, changed, problems
}

// dropFactSources takes deleted kakera out of the facts that rest on them. A
// fact left resting on nothing goes; one that rests on another kakera stays.
// Facts that never had a source are left alone. It reports whether anything
// changed.
func (r *Runner) dropFactSources(ctx context.Context, personaID string, gone map[string]bool, zone *time.Location) (bool, error) {
	held, release, err := r.lockKeeper(ctx, personaID)
	if err != nil {
		return false, err
	}
	defer release()
	return r.dropFactSourcesHeld(held, personaID, gone, zone)
}

// dropFactSourcesHeld is dropFactSources for a caller holding the persona's
// keeper lock.
func (r *Runner) dropFactSourcesHeld(held context.Context, personaID string, gone map[string]bool, zone *time.Location) (bool, error) {
	if len(gone) == 0 {
		return false, nil
	}
	m := r.loadMemo(personaID, zone)
	var kept []fact
	touched := false
	for _, f := range m.Facts {
		if len(f.From) == 0 {
			kept = append(kept, f)
			continue
		}
		var from []string
		for _, id := range f.From {
			if !gone[id] {
				from = append(from, id)
			}
		}
		if len(from) != len(f.From) {
			touched = true
		}
		if len(from) == 0 {
			continue
		}
		f.From = from
		kept = append(kept, f)
	}
	if !touched {
		return false, nil
	}
	m.Facts = kept
	return true, r.saveMemo(held, personaID, dirtySummary(m), zone)
}

// ErrFactNotFound is a fact the user asked to change that memory does not
// hold.
var ErrFactNotFound = fmt.Errorf("その事実は見つかりません")

// EditFact puts right the words of one fact, as the user typed them. It is a
// correction, not a change in what holds: the fact keeps its id, its kakera
// and its day. The persona is not woken.
func (r *Runner) EditFact(ctx context.Context, personaID, id, text string) error {
	text = cleanFact(text)
	if text == "" || len([]rune(text)) > maxFactRunes {
		return fmt.Errorf("事実は1〜%d字で書いてください", maxFactRunes)
	}
	return r.changeFact(ctx, personaID, id, func(f *fact) bool { f.Text = text; return true })
}

// ForgetFact throws one fact away.
func (r *Runner) ForgetFact(ctx context.Context, personaID, id string) error {
	return r.changeFact(ctx, personaID, id, func(*fact) bool { return false })
}

// changeFact finds one fact and keeps it changed, or drops it.
func (r *Runner) changeFact(ctx context.Context, personaID, id string, change func(*fact) bool) error {
	held, release, err := r.lockKeeper(ctx, personaID)
	if err != nil {
		return err
	}
	defer release()
	_, settings := r.clientOf()
	zone := settings.Location()
	m := r.loadMemo(personaID, zone)
	found := false
	var facts []fact
	for _, f := range m.Facts {
		if f.ID == id {
			found = true
			if !change(&f) {
				continue
			}
		}
		facts = append(facts, f)
	}
	if !found {
		return ErrFactNotFound
	}
	if n := factsRunes(facts); n > maxFactsRunes && n > factsRunes(m.Facts) {
		return fmt.Errorf("いまの事実が合わせて%d字になります（%d字まで）", n, maxFactsRunes)
	}
	m.Facts = facts
	return r.saveMemo(held, personaID, dirtySummary(m), zone)
}

// dirtySummary marks the summary to be written again: corrections, merges
// and removals need a new one even when no record is new. With no fact and no
// record left, the summary has nothing to rest on and goes too.
func dirtySummary(m memo) memo {
	if len(m.Facts) == 0 && len(m.Records) == 0 {
		return memo{CheckedAt: m.CheckedAt, SuggestedAt: m.SuggestedAt}
	}
	m.SummaryDirty = true
	return m
}
