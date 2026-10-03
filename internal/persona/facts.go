package persona

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A field is kept as a list of facts, not as prose. The keeper never rewrites
// a field: it adds a fact, replaces one with a newer one, says one no longer
// holds, or merges several into one, and the app applies exactly that. So a
// fact goes only when the keeper says so - nothing is lost to a rewrite - and
// every fact carries the kakera it rests on, which is how the app takes out
// what a deleted kakera left behind without asking a model.
//
// Over the facts sits a summary in prose, the persona's own reading of the
// field. It is not rewritten with every kakera: once a day, on the due pass,
// the persona rewrites it from what changed among the facts since it was last
// written (summaries.go).
//
// A field file, "{Field}.md", holds the summary with the day it was written,
// the facts that hold now, then those that no longer do, with when they
// stopped and what replaced them:
//
//	# Shop
//
//	## まとめ（2026-10-02 更新）
//
//	三田の二郎がずっと気になっている。…
//
//	## いまの事実
//
//	- [f_ab12cd] 三田の二郎に行きたい from:e_a,e_d since:2026-09-08
//
//	## 以前のこと
//
//	- [f_9x8y7z] 豚骨に寄る from:e_c since:2026-09-01 until:2026-09-20 by:f_ab12cd

// maxFactRunes is the most one fact may say.
const maxFactRunes = 200

// maxPastFacts is how many facts that no longer hold a field keeps; the oldest
// go first.
const maxPastFacts = 50

// The headings of a field file.
const (
	summaryHeading = "## まとめ"
	currentHeading = "## いまの事実"
	pastHeading    = "## 以前のこと"
)

var fieldSummaryDay = regexp.MustCompile(`^## まとめ（(\d{4}-\d{2}-\d{2})(?: 更新)?）$`)

type fact struct {
	ID    string
	Text  string
	From  []string
	Since time.Time
	// Until and By are set once the fact no longer holds: when, and the fact
	// that replaced it, if one did.
	Until time.Time
	By    string
}

type fieldFacts struct {
	// Summary is the persona's prose over the field, and SummaryAt the day it
	// was last written; zero when it never was.
	Summary      string
	SummaryAt    time.Time
	SummaryDirty bool
	Current      []fact
	Past         []fact
}

func (ff fieldFacts) empty() bool {
	return ff.Summary == "" && len(ff.Current) == 0 && len(ff.Past) == 0
}

const summaryDirtyMarker = "<!-- kakeratta:summary-dirty -->"

var factLine = regexp.MustCompile(`^- \[(f_[A-Za-z0-9]+)\] (.*)$`)
var factTrailer = regexp.MustCompile(`\s(from|since|until|by):(\S+)$`)

// parseFacts reads a field file. A line that is not a fact is left out.
func parseFacts(text string, zone *time.Location) fieldFacts {
	var out fieldFacts
	section := ""
	var summary []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == summaryDirtyMarker:
			out.SummaryDirty = true
			continue
		case line == summaryHeading || fieldSummaryDay.MatchString(line):
			section = "summary"
			if match := fieldSummaryDay.FindStringSubmatch(line); match != nil {
				out.SummaryAt, _ = time.ParseInLocation("2006-01-02", match[1], zone)
			}
			continue
		case line == currentHeading:
			section = "current"
			continue
		case line == pastHeading:
			section = "past"
			continue
		case section == "summary":
			summary = append(summary, strings.TrimRight(raw, " \t"))
			continue
		}
		past := section == "past"
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
			case "until":
				f.Until, _ = time.ParseInLocation("2006-01-02", found[2], zone)
			case "by":
				f.By = found[2]
			}
			rest = rest[:len(rest)-len(found[0])]
		}
		f.Text = strings.TrimSpace(rest)
		if past {
			out.Past = append(out.Past, f)
		} else {
			out.Current = append(out.Current, f)
		}
	}
	out.Summary = strings.TrimSpace(strings.Join(summary, "\n"))
	if prose, ok := proseText(text); ok {
		// A field still kept as prose reads as its summary, so a fact
		// added before it is split keeps the prose rather than losing it.
		out.Summary = prose
	}
	return out
}

// proseText is a field file kept as prose - text with no heading of a field's
// sections and no fact line - without its title, and whether it is one.
func proseText(text string) (string, bool) {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == summaryHeading || fieldSummaryDay.MatchString(line) || line == currentHeading || line == pastHeading || factLine.MatchString(line) {
			return "", false
		}
		lines = append(lines, strings.TrimRight(raw, " \t"))
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		lines = lines[1:]
	}
	prose := strings.TrimSpace(strings.Join(lines, "\n"))
	return prose, prose != ""
}

func (f fact) line(zone *time.Location) string {
	line := "- [" + f.ID + "] " + f.Text
	if len(f.From) > 0 {
		line += " from:" + strings.Join(f.From, ",")
	}
	if !f.Since.IsZero() {
		line += " since:" + f.Since.In(zone).Format("2006-01-02")
	}
	if !f.Until.IsZero() {
		line += " until:" + f.Until.In(zone).Format("2006-01-02")
	}
	if f.By != "" {
		line += " by:" + f.By
	}
	return line
}

// factsText is a field file holding these facts.
func factsText(field string, ff fieldFacts, zone *time.Location) string {
	lines := []string{"# " + field, ""}
	if ff.SummaryDirty {
		lines = append(lines, summaryDirtyMarker, "")
	}
	if ff.Summary != "" {
		head := summaryHeading
		if !ff.SummaryAt.IsZero() {
			head += "（" + ff.SummaryAt.In(zone).Format("2006-01-02") + " 更新）"
		}
		lines = append(lines, head, "", ff.Summary, "", currentHeading, "")
	}
	for _, f := range ff.Current {
		lines = append(lines, f.line(zone))
	}
	if len(ff.Past) > 0 {
		lines = append(lines, "", pastHeading, "")
		for _, f := range ff.Past {
			lines = append(lines, f.line(zone))
		}
	}
	return strings.Join(lines, "\n") + "\n"
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
	// A bullet carried over from prose is the list's, not the fact's.
	for _, mark := range []string{"- ", "・", "* ", "• "} {
		text = strings.TrimSpace(strings.TrimPrefix(text, mark))
	}
	for _, key := range []string{"from", "since", "until", "by"} {
		text = strings.ReplaceAll(text, " "+key+":", " "+key+"：")
	}
	return text
}

// currentRunes is how much the facts that hold say, which is what a turn is
// handed and what the field's size limit counts.
func currentRunes(ff fieldFacts) int {
	n := 0
	for _, f := range ff.Current {
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

// fieldFile is a top-level "{Field}.md": the shape of a field's file, whether
// or not the persona's layout names the field now.
var fieldFile = regexp.MustCompile(`^([A-Z][A-Za-z0-9_-]{1,29})\.md$`)

// memoryFacts is everything a persona keeps as facts: the fields its layout
// names, in order, then fields kept from before the layout changed.
type memoryFacts struct {
	Fields []string
	// Orphans are fields with a file that the layout no longer names. Their
	// facts are still shown and may be moved into a field.
	Orphans []string
	ByField map[string]fieldFacts
}

func (m memoryFacts) all() []string { return append(append([]string{}, m.Fields...), m.Orphans...) }

// find is the field holding the fact with this id, and whether it holds now.
func (m memoryFacts) find(id string) (field string, current, ok bool) {
	for name, ff := range m.ByField {
		for _, f := range ff.Current {
			if f.ID == id {
				return name, true, true
			}
		}
		for _, f := range ff.Past {
			if f.ID == id {
				return name, false, true
			}
		}
	}
	return "", false, false
}

func (r *Runner) loadFacts(skill Skill, zone *time.Location) memoryFacts {
	m := memoryFacts{Fields: memoryFields(skill), ByField: map[string]fieldFacts{}}
	named := map[string]bool{}
	for _, field := range m.Fields {
		named[field] = true
		if note, ok := r.store.Note(skill.ID, fieldPath(field)); ok {
			m.ByField[field] = parseFacts(note.Text, zone)
		}
	}
	paths, _ := r.store.NotePaths(skill.ID)
	for _, path := range paths {
		match := fieldFile.FindStringSubmatch(path)
		if match == nil || named[match[1]] {
			continue
		}
		if note, ok := r.store.Note(skill.ID, path); ok {
			ff := parseFacts(note.Text, zone)
			if !ff.empty() {
				m.Orphans = append(m.Orphans, match[1])
				m.ByField[match[1]] = ff
			}
		}
	}
	sort.Strings(m.Orphans)
	return m
}

// renderFacts is the facts that hold now, by field, as a turn is shown them.
func renderFacts(m memoryFacts, zone *time.Location) string {
	var parts []string
	for _, field := range m.all() {
		ff := m.ByField[field]
		head := "## " + field
		if contains(m.Orphans, field) {
			head += "（分野の設定から外れた分野。事実は update で今の分野へ移せる）"
		}
		var lines []string
		if ff.Summary != "" {
			lines = append(lines, "まとめ:", ff.Summary, "", "事実:")
		}
		for _, f := range ff.Current {
			line := "- [" + f.ID + "] " + f.Text
			if !f.Since.IsZero() {
				line += "（" + f.Since.In(zone).Format("2006-01-02") + "〜）"
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			lines = []string{"（まだありません）"}
		}
		parts = append(parts, head+"\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// factOp is one change the keeper asks for.
type factOp struct {
	// Op is add, update, invalidate, or merge.
	Op    string   `json:"op"`
	Field string   `json:"field"`
	ID    string   `json:"id"`
	IDs   []string `json:"ids"`
	Text  string   `json:"text"`
}

// planFacts applies ops to m in memory, and says what is wrong with them. The
// kakera they rest on is from (empty for none). It returns the fields changed.
func planFacts(m memoryFacts, ops []factOp, from string, now time.Time, zone *time.Location) (memoryFacts, []string, []string) {
	next := memoryFacts{Fields: m.Fields, Orphans: m.Orphans, ByField: map[string]fieldFacts{}}
	for field, ff := range m.ByField {
		next.ByField[field] = fieldFacts{Summary: ff.Summary, SummaryAt: ff.SummaryAt, SummaryDirty: ff.SummaryDirty, Current: append([]fact{}, ff.Current...), Past: append([]fact{}, ff.Past...)}
	}
	var problems []string
	changed := map[string]bool{}
	used := map[string]bool{}
	today := now.In(zone)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone)
	taken := func(id string) bool { _, _, ok := next.find(id); return ok }
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
	// take removes a fact that holds now from its field and returns it.
	take := func(id string) (fact, string, bool) {
		field, current, ok := next.find(id)
		if !ok || !current {
			return fact{}, "", false
		}
		ff := next.ByField[field]
		for i, f := range ff.Current {
			if f.ID == id {
				ff.Current = append(ff.Current[:i:i], ff.Current[i+1:]...)
				next.ByField[field] = ff
				changed[field] = true
				return f, field, true
			}
		}
		return fact{}, "", false
	}
	retire := func(field string, f fact, by string) {
		f.Until, f.By = day, by
		ff := next.ByField[field]
		ff.Past = append(ff.Past, f)
		if len(ff.Past) > maxPastFacts {
			ff.Past = ff.Past[len(ff.Past)-maxPastFacts:]
		}
		next.ByField[field] = ff
	}
	put := func(field string, f fact) {
		ff := next.ByField[field]
		ff.Current = append(ff.Current, f)
		next.ByField[field] = ff
		changed[field] = true
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
	target := func(at, name string) (string, bool) {
		field, ok := fieldNamed(m.Fields, name)
		if !ok {
			problems = append(problems, at+": 分野「"+name+"」はありません（"+strings.Join(m.Fields, " / ")+"）")
		}
		return field, ok
	}
	once := func(at, id string) bool {
		if used[id] {
			problems = append(problems, at+": "+id+" はこの提出ですでに扱っています")
			return false
		}
		used[id] = true
		return true
	}
	for i, op := range ops {
		at := fmt.Sprintf("facts[%d]（%s）", i, op.Op)
		switch op.Op {
		case "add":
			field, ok := target(at, op.Field)
			t, good := text(at, op.Text)
			if ok && good {
				put(field, fact{ID: newFactID(taken), Text: t, From: sources([]string{from}), Since: day})
			}
		case "update":
			if !once(at, op.ID) {
				continue
			}
			old, field, ok := take(op.ID)
			if !ok {
				problems = append(problems, at+": "+op.ID+" はいまの事実にありません")
				continue
			}
			if op.Field != "" {
				if moved, good := target(at, op.Field); good {
					field = moved
				}
			} else if contains(m.Orphans, field) {
				problems = append(problems, at+": 分野の設定から外れた分野の事実を直すときは、移す先の field を書いてください")
			}
			t, good := text(at, op.Text)
			if !good {
				continue
			}
			f := fact{ID: newFactID(taken), Text: t, From: sources(old.From, []string{from}), Since: day}
			if t == old.Text {
				// Only moved, or confirmed again: the same fact, kept as it was.
				f.ID, f.Since = old.ID, old.Since
			} else {
				retire(field, old, f.ID)
			}
			put(field, f)
		case "invalidate":
			if !once(at, op.ID) {
				continue
			}
			old, field, ok := take(op.ID)
			if !ok {
				problems = append(problems, at+": "+op.ID+" はいまの事実にありません")
				continue
			}
			retire(field, old, "")
		case "merge":
			if len(op.IDs) < 2 {
				problems = append(problems, at+": ids に2件以上を書いてください")
				continue
			}
			field, ok := target(at, op.Field)
			t, good := text(at, op.Text)
			var from []string
			var since time.Time
			for _, id := range op.IDs {
				if !once(at, id) {
					continue
				}
				old, _, found := take(id)
				if !found {
					problems = append(problems, at+": "+id+" はいまの事実にありません")
					continue
				}
				from = sources(from, old.From)
				if since.IsZero() || (!old.Since.IsZero() && old.Since.Before(since)) {
					since = old.Since
				}
			}
			if ok && good {
				put(field, fact{ID: newFactID(taken), Text: t, From: from, Since: since})
			}
		default:
			problems = append(problems, at+": op は add / update / invalidate / merge のどれかです")
		}
	}
	for _, field := range next.all() {
		if n := currentRunes(next.ByField[field]); n > maxFieldRunes && changed[field] {
			problems = append(problems, fmt.Sprintf("分野「%s」のいまの事実が合わせて%d字になります。%d字以内になるよう、似た事実を merge でまとめてください", field, n, maxFieldRunes))
		}
	}
	var fields []string
	for field := range changed {
		next.ByField[field] = dirtySummary(next.ByField[field])
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return next, fields, problems
}

// writeFacts writes the fields that changed and returns their paths. A field
// left with no facts at all is deleted.
func (r *Runner) writeFacts(ctx context.Context, personaID string, m memoryFacts, fields []string, zone *time.Location) ([]string, error) {
	var written []string
	for _, field := range fields {
		if err := ctx.Err(); err != nil {
			return written, err // The keeper lost its lock: another may be writing.
		}
		ff := m.ByField[field]
		path := fieldPath(field)
		if ff.empty() {
			if err := r.store.DeleteNote(personaID, path); err != nil {
				return written, err
			}
			continue
		}
		if err := r.store.WriteNote(personaID, path, factsText(field, ff, zone)); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

// dropFactSources takes deleted kakera out of the facts that rest on them. A
// fact left resting on nothing goes; one that rests on another kakera stays.
// Facts that never had a source - folded in from before - are left alone. It
// reports whether anything changed.
func (r *Runner) dropFactSources(ctx context.Context, skill Skill, gone map[string]bool, zone *time.Location) (bool, error) {
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return false, err
	}
	defer release()
	return r.dropFactSourcesHeld(held, skill, gone, zone)
}

// dropFactSourcesHeld is dropFactSources for a caller holding the persona's
// keeper lock.
func (r *Runner) dropFactSourcesHeld(held context.Context, skill Skill, gone map[string]bool, zone *time.Location) (bool, error) {
	if len(gone) == 0 {
		return false, nil
	}
	m := r.loadFacts(skill, zone)
	var fields []string
	keep := func(list []fact) ([]fact, bool) {
		var out []fact
		touched := false
		for _, f := range list {
			if len(f.From) == 0 {
				out = append(out, f)
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
			out = append(out, f)
		}
		return out, touched
	}
	for _, field := range m.all() {
		ff := m.ByField[field]
		current, a := keep(ff.Current)
		past, b := keep(ff.Past)
		if a || b {
			m.ByField[field] = dirtySummary(fieldFacts{Summary: ff.Summary, SummaryAt: ff.SummaryAt, Current: current, Past: past})
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return false, nil
	}
	_, err := r.writeFacts(held, skill.ID, m, fields, zone)
	return true, err
}

// ErrFactNotFound is a fact the user asked to change that the field does not
// hold.
var ErrFactNotFound = fmt.Errorf("その事実は見つかりません")

// EditFact puts right the words of one fact, as the user typed them. It is a
// correction, not a change in what holds: the fact keeps its id, its kakera
// and its day, and nothing moves to the past. The persona is not woken.
func (r *Runner) EditFact(ctx context.Context, personaID, field, id, text string) error {
	text = cleanFact(text)
	if text == "" || len([]rune(text)) > maxFactRunes {
		return fmt.Errorf("事実は1〜%d字で書いてください", maxFactRunes)
	}
	return r.changeFact(ctx, personaID, field, id, func(f *fact) bool { f.Text = text; return true })
}

// ForgetFact throws one fact away, whether it holds now or no longer does.
// The user means it gone, so it does not move to the past.
func (r *Runner) ForgetFact(ctx context.Context, personaID, field, id string) error {
	return r.changeFact(ctx, personaID, field, id, func(*fact) bool { return false })
}

// changeFact finds one fact in a field and keeps it changed, or drops it.
func (r *Runner) changeFact(ctx context.Context, personaID, field, id string, change func(*fact) bool) error {
	ctx = r.withSkills(ctx)
	skill, ok := r.skills(ctx).Find(personaID)
	if !ok {
		return fmt.Errorf("担当が見つかりません: %s", personaID)
	}
	skill = skill.forTurn()
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return err
	}
	defer release()
	_, settings := r.clientOf()
	zone := settings.Location()
	m := r.loadFacts(skill, zone)
	ff, ok := m.ByField[field]
	if !ok {
		return ErrFactNotFound
	}
	found := false
	apply := func(list []fact) []fact {
		var out []fact
		for _, f := range list {
			if f.ID == id {
				found = true
				if !change(&f) {
					continue
				}
			}
			out = append(out, f)
		}
		return out
	}
	ff.Current, ff.Past = apply(ff.Current), apply(ff.Past)
	if !found {
		return ErrFactNotFound
	}
	if n := currentRunes(ff); n > maxFieldRunes {
		return fmt.Errorf("分野「%s」のいまの事実が合わせて%d字になります（%d字まで）", field, n, maxFieldRunes)
	}
	m.ByField[field] = dirtySummary(ff)
	_, err = r.writeFacts(held, skill.ID, m, []string{field}, zone)
	return err
}

// Corrections, merges and removals need a new summary even when the facts'
// historical dates do not change. Removing the last fact also removes prose
// that no longer has any evidence, without asking a model to preserve it.
func dirtySummary(ff fieldFacts) fieldFacts {
	if len(ff.Current) == 0 && len(ff.Past) == 0 {
		return fieldFacts{}
	}
	ff.SummaryDirty = true
	return ff
}
