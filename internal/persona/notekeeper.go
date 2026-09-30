package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// The note keeper writes a persona's notes once the turn has run and its remark
// is out. Notes used to be a phase of the turn itself, and a phase that could
// not finish - a model reading the same notes over and over - took the finished
// answer with it: the user saw a persona that had stayed quiet. Kept apart, a
// note that fails to come out costs the note and nothing else.
//
// The keeper is the persona in its own voice, on the persona model, with the OKF
// skill. It reads in two steps that cannot loop: it picks up to maxKeeperReads
// notes from the index, then is handed their full text and proposes the changes.
// The app decides what has to be true afterwards - one note per thing, and every
// note linked from index.md under a title - and sends a proposal back when it
// is not; what the notes say, and how index.md is grouped, stay the persona's.

// maxKeeperReads is how many existing notes the keeper may ask to see whole.
const maxKeeperReads = 4

// maxHints caps the lines of "needs tidying" the keeper is shown.
const maxHints = 12

// keeperTurn is what the keeper is given about the turn that just ran.
type keeperTurn struct {
	skill   Skill
	set     *tools.Set
	rules   turnRules
	thread  []timeline.Entry
	trigger timeline.Entry
	post    timeline.Entry
	answer  string
	wake    string
	here    *time.Location
	names   map[string]string
	own     []store.NoteSource
	layout  string
	// schedules are the ones the turn placed: a schedule with its background
	// note is itself a record of what was asked.
	schedules []ScheduleOperation
	// preread are the notes the turn's own reading opened. The persona has
	// them in mind already, so the keeper is handed them without choosing.
	preread []string
	// handed are the notes the app knows the keeper needs - due for review,
	// left or linking to one taken away by a deletion, or what a post of the
	// persona's own was about (handedNotes).
	handed []string
}

var chooseNotesTool = llm.Tool{
	Name:        "choose_notes",
	Description: "書き足す・直す・まとめるために全文を読みたい既存メモを選ぶ。読みたいものがなければ空配列。",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"paths": map[string]any{"type": "array", "maxItems": maxKeeperReads, "items": map[string]any{"type": "string"}, "description": "index.md や一覧にあるメモの path"},
	}, "required": []string{"paths"}, "additionalProperties": false},
}

// keeperPrompt opens the way phasePrompt does, so a persona is one voice across
// its turn and a log keyed by the opening line stays whole.
func keeperPrompt(skill Skill, layout, wake string) string {
	s := fmt.Sprintf("あなたは「%s」。%s\n担当領域: %s\n担当の指示: %s\n", skill.Name, skill.Tone, skill.Domain, skill.Instructions)
	s += `いまはこの担当のメモ係として、メモだけを整える。返事や予定はすでに決まっていて、ここでは変えない。提案は本体の検証後に実行される。
このターンで何を覚えておくかを判断する。
- **ユーザーの投稿や返信に回答したターンは、必ず記録を残す。** 書くのは、ユーザーが何を言った・聞いたか（1〜2行、ユーザーの言葉で）と、どう答えたかの要点1行（例: 「〜という見方で答えた（不確実）」「A店とB店を挙げた」）。メモには元の投稿へのリンクが付くので、詳しくはそこからたどれる。同じ話題のメモがすでにあれば、新しく作らず、全文を読んだうえで日付付きの行を書き足す。
- ユーザーが言ったこと（事実・好み・予定・関係の変化・明示的に覚えてと頼まれたこと）は、それ自体を残す。返事をしないターンでも、自分の領域のことなら残す。
- AI（あなたや他の担当、外部エージェント）が調べた事実・候補一覧・背景説明・解説の本文は保存しない。必要になったら、その時点の最新情報で調べ直す方が正確。残すのは「どう答えたか」の1行だけ。
- 回答の結論をユーザーが受け入れた（選んだ・同意した・行った）、またはその後の会話で前提になったら、その結論を事実として残してよい。今回の Answer にはまだ誰も反応していないので、「そう答えた」とだけ書く。
- 後から取り直せない情報（ユーザーが渡した画像やリンクの中身など）は例外的に残してよい。
- 本体が「整理が必要」と示したもの（索引にないメモ、題名が同じ・よく似たメモ）は、このターンの用事のついでに直してよい。統合するときは、残す1つに内容を移し、ほかは forget する。
- index.md の「# 未整理」の下にあるメモは、本体が仮に置いたもの。ついでに、合う見出しへ移して分類し直してよい（その見出しは空になれば消す）。
Answer の文章を写さない。本体は Answer の文を2文以上含むメモを差し戻す。メモの分類の型（見方・好み・質問など）は残すものの分け方で、解説を書く枠ではない。
予定の背景メモは予定に属するので書かない。writeは全文、forgetはpath。

index.md の規則（本体が確かめる）:
- index.md は、そのままメモの目次として読める。担当の領域に合う見出しでメモを分類し、見出しごとに「* [メモの題名](/path/to/note.md) - 一言の説明」を並べる。
- **メモを write するときは section に見出しを1つ書く**（既存の見出しがあればその文字のまま。関係のあるメモは同じ見出しに）。本体がその見出しの下に、メモの title と description から1行を入れ、別の見出しにあった行は移す。見出しがなければ作る。
- forget したメモの行は本体が消す。index.md 自身を書くのは、見出しの名前を変える・分け直すなど、分類そのものを直すときだけ。
- どのメモも、index.md から必ず1回リンクされる。section を空にした新しいメモは、本体が「未整理」の見出しに置く。
- ここでは notes_list / notes_read / notes_write / notes_forget は使えない。読むメモは choose_notes で選び、変更は operations（write / forget）で出す。
`
	s += keeperNoteRules + "メモの分類（型はここにあるものだけ）: " + layout + "\n" + okfHost + okfSkill
	return s + "\n" + wake
}

// keeperNoteRules are how every persona keeps its notes, as the keeper is told.
const keeperNoteRules = `メモの規則:
- 既存メモを更新する前に、その全文を確認し、保持すべき内容を消さない。全文を渡されていない既存メモの上書きは本体が差し戻す。
- frontmatter の tags に、探すときの手がかりになる語を2〜5つ（産地・品種・店の種類・主題など）。
- 同じものにメモは1つ。題名が同じ・よく似たメモがあれば、新しく作らずその path を書き直す。綴りの揺れや型の違い、ディレクトリの違いは新しいメモの理由にならない。型はメモの分類にあるものだけ。
- 引用内容、本人の意見、他の担当の推測を区別し、推測は推測と明記する。統合しても代表的な根拠と例外を少し残す。
- 回答の文章を写さない。回答はスレッドに残り、メモは回答のキャッシュではない。どう答えたかは1行の要点で書く。
`

// lockKeeper serialises one persona's keeper: two turns finishing together -
// here or in another process serving the workspace - must not each rewrite the
// same note or the same index.md from what they saw.
func (r *Runner) lockKeeper(ctx context.Context, personaID string) (func(), error) {
	return r.store.Lock(ctx, "keeper:"+personaID)
}

// allNotes reads every note a persona keeps, in name order.
func (r *Runner) allNotes(personaID string) []store.Note {
	notes, _ := r.allNotesChecked(personaID)
	return notes
}

func (r *Runner) allNotesChecked(personaID string) ([]store.Note, error) {
	paths, err := r.store.NotePaths(personaID)
	if err != nil {
		return nil, err
	}
	return r.store.Notes(personaID, paths)
}

// keeperRecord is one keeper run, for the log.
type keeperRecord struct {
	Severity string          `json:"severity"`
	Message  string          `json:"message"`
	Persona  string          `json:"persona"`
	Trigger  string          `json:"trigger,omitempty"`
	Model    string          `json:"model"`
	Chose    []string        `json:"chose,omitempty"`
	Phase    *phaseRecord    `json:"phase,omitempty"`
	Plan     []NoteOperation `json:"plan,omitempty"`
	Steps    []stepRecord    `json:"steps,omitempty"`
	Repaired bool            `json:"indexRepaired,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func logKeeper(record keeperRecord) {
	record.Message = "notes " + record.Persona
	record.Severity = "INFO"
	if record.Error != "" {
		record.Severity = "WARNING"
	}
	if encoded, err := json.Marshal(record); err == nil {
		fmt.Fprintln(os.Stdout, string(encoded))
	}
}

// keepNotes writes down what the turn should leave behind. A failure never
// retracts the answer; the caller persists it for retry. Index repair is
// attempted even when note work fails, and a failed repair is returned too.
func (r *Runner) keepNotes(ctx context.Context, client *llm.Client, settings store.Settings, k keeperTurn) (written []string, stage string, failure error) {
	stage = "notes"
	model := personaModel(settings)
	if err := ctx.Err(); err != nil {
		return nil, stage, err
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return nil, stage, fmt.Errorf("note keeper has no model connection")
	}
	release, err := r.lockKeeper(ctx, k.skill.ID)
	if err != nil {
		return nil, stage, err
	}
	defer release()
	record := keeperRecord{Persona: k.skill.ID, Trigger: k.trigger.ID, Model: model}
	defer func() {
		if problem := recover(); problem != nil {
			failure = fmt.Errorf("note keeper panic: %v", problem)
			record.Error = failure.Error()
		}
	}()

	defer func() {
		if repaired, err := r.repairIndex(k.skill.ID); err != nil {
			if failure == nil {
				failure = err
				stage = "index"
			}
			record.Error = failure.Error()
		} else if repaired {
			record.Repaired = true
		}
		logKeeper(record)
	}()

	notes, err := r.allNotesChecked(k.skill.ID)
	if err != nil {
		record.Error = err.Error()
		return nil, stage, err
	}
	index := "（まだありません）"
	if note, ok := r.store.Note(k.skill.ID, "index.md"); ok {
		index = note.Text
	}
	system := keeperPrompt(k.skill, k.layout, k.wake)
	base := keeperUser(k, notes, index)

	// Step one: which notes to see whole. Nothing to pick from, nothing to ask.
	prov := newProvenance(k.own...)
	referenced := map[string]bool{"index.md": true}
	var seen []store.Note
	if len(notes) > 0 {
		chosen := append(append([]string{}, k.handed...), k.preread...)
		chosen = append(chosen, r.chooseNotes(ctx, client, model, system, base, notes)...)
		taken := map[string]bool{}
		for _, path := range chosen {
			if taken[path] {
				continue
			}
			taken[path] = true
			for _, note := range notes {
				if note.Path == path {
					seen = append(seen, note)
					referenced[note.Path] = true
					prov.noteRead(note.Path, note.Text)
					record.Chose = append(record.Chose, note.Path)
				}
			}
		}
	}

	// Step two: the proposal, made with those notes in front of it and with no
	// way to read more.
	user := base
	if len(seen) > 0 {
		user += "\n\n渡された既存メモ（全文。選んだもの、このターンで読んだもの、本体が必要と判断したもの）:"
		for _, note := range seen {
			user += "\n=== " + note.Path + " ===\n" + note.Text
		}
	}
	rules := k.rules
	rules.handed = true
	rules.notes = func(path string) []store.Note { return withoutPath(notes, path) }
	rules.readState = func(path string) (bool, bool) {
		path = strings.TrimPrefix(strings.TrimSpace(path), "/")
		for _, note := range notes {
			if note.Path == path {
				return true, referenced[path]
			}
		}
		return false, false
	}
	rules.cited = map[string]string{}
	answered := Workflow{Answer: Answer{Text: k.answer}, Schedules: k.schedules}
	indexAsked := false
	next, phase, err := proposePhase(ctx, client, model, "note", system, user, k.set, nil, func(candidate Workflow) []string {
		// A note thrown away in the same proposal is not one it can duplicate:
		// merging two notes writes one and forgets the other.
		gone := map[string]bool{}
		for _, op := range candidate.Notes {
			if op.Type == "forget" {
				gone[strings.TrimPrefix(strings.TrimSpace(op.Path), "/")] = true
			}
		}
		checked := rules
		checked.notes = func(path string) []store.Note {
			var kept []store.Note
			for _, note := range withoutPath(notes, path) {
				if !gone[note.Path] {
					kept = append(kept, note)
				}
			}
			return kept
		}
		problems := checkBatch(Workflow{Notes: candidate.Notes}.batch(), checked)
		problems = append(problems, copiedAnswer(k.answer, candidate.Notes)...)
		if why := unrecorded(answered, candidate.Notes, rules); why != "" {
			problems = append(problems, why)
		}
		// The index is asked for once. A persona that will not place a note in it
		// is not held to it: repairIndex files the note under a heading of its
		// own, so no note is lost to index.md's layout.
		if !indexAsked {
			indexAsked = true
			problems = append(problems, indexProblems(notes, index, candidate.Notes)...)
		}
		return problems
	})
	record.Phase = phase
	if err != nil {
		// Logged, not emitted: a note that did not come out is not an error the
		// turn, or a preview of it, should report.
		record.Error = err.Error()
		return nil, stage, err
	}
	record.Plan = next.Notes
	if len(next.Notes) == 0 {
		return nil, "index", nil
	}
	_, steps := r.execute(ctx, k.skill, Workflow{Notes: next.Notes}.batch(), k.set, rules, k.post, prov)
	record.Steps = steps
	written = notesWritten(steps)
	if len(steps) > 0 && steps[len(steps)-1].Error != "" {
		record.Error = fmt.Sprintf("stopped at %s: %s", steps[len(steps)-1].Type, steps[len(steps)-1].Error)
		failure = fmt.Errorf("%s", record.Error)
	}
	if err := r.placeInIndex(k.skill.ID, next.Notes, written); err != nil && failure == nil {
		failure = err
		stage = "index"
	}
	if failure != nil {
		return written, stage, failure
	}
	return written, "index", nil
}

// placeInIndex puts a line for each note written with a section under that
// heading of index.md, and refreshes the line of a listed note written without
// one. Only notes that were actually written are placed.
func (r *Runner) placeInIndex(personaID string, ops []NoteOperation, written []string) error {
	index := ""
	if note, ok := r.store.Note(personaID, "index.md"); ok {
		index = note.Text
	}
	updated := index
	for _, op := range ops {
		path := strings.TrimPrefix(strings.TrimSpace(op.Path), "/")
		if op.Type != "write" || reservedNote(path) || !contains(written, path) {
			continue
		}
		note, ok := r.store.Note(personaID, path)
		if !ok {
			continue
		}
		updated = placeNoteInIndex(updated, strings.TrimSpace(op.Section), note)
	}
	if updated != index {
		return r.store.WriteNote(personaID, "index.md", updated)
	}
	return nil
}

// indexLine is how a note is listed: its title linked, then its description.
func indexLine(note store.Note) string {
	title := noteTitle(note.Text)
	if title == "" {
		title = strings.TrimSuffix(pathBase(note.Path), ".md")
	}
	line := "* [" + title + "](/" + note.Path + ")"
	if description := noteDescription(note.Text); description != "" {
		line += " - " + description
	}
	return line
}

// headingName is a Markdown heading's text, without its #s.
func headingName(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(trimmed, "#")), true
}

// linksTo says whether an index line is a list item linking to path.
func linksTo(line, path string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "-") {
		return false
	}
	for _, match := range indexLink.FindAllStringSubmatch(line, -1) {
		if linkTarget(match[2]) == path {
			return true
		}
	}
	return false
}

// placeLine lists note under section in index. With a section, any line for it
// elsewhere is moved there, and the heading is made if missing. Without one, a
// line already there is brought up to date in place, and a note not listed is
// left for repairIndex to file as unsorted.
func placeNoteInIndex(index, section string, note store.Note) string {
	line := indexLine(note)
	lines := strings.Split(strings.TrimRight(index, "\n"), "\n")
	if index == "" {
		lines = nil
	}
	if section == "" {
		for i, existing := range lines {
			if linksTo(existing, note.Path) {
				lines[i] = line
				return joinLines(lines)
			}
		}
		return index
	}
	var kept []string
	for _, existing := range lines {
		if !linksTo(existing, note.Path) {
			kept = append(kept, existing)
		}
	}
	lines = kept
	at := -1
	for i, existing := range lines {
		if name, ok := headingName(existing); ok && strings.EqualFold(name, section) {
			// After the heading's last item, before the next heading.
			at = i + 1
			for j := i + 1; j < len(lines); j++ {
				if _, next := headingName(lines[j]); next {
					break
				}
				if strings.TrimSpace(lines[j]) != "" {
					at = j + 1
				}
			}
			break
		}
	}
	if at < 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "# "+section, "", line)
	} else {
		if _, heading := headingName(lines[at-1]); heading {
			lines = append(lines[:at], append([]string{"", line}, lines[at:]...)...)
		} else {
			lines = append(lines[:at], append([]string{line}, lines[at:]...)...)
		}
	}
	return joinLines(lines)
}

// joinLines ends a Markdown text with one newline, and no blank lines before it.
func joinLines(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// chooseNotes asks which existing notes to read whole. A reply that cannot be
// read, or a model that errors, chooses nothing.
func (r *Runner) chooseNotes(ctx context.Context, client *llm.Client, model, system, base string, notes []store.Note) []string {
	messages := []llm.Message{
		{Role: "system", Content: system + "\n\nまず choose_notes で、このターンのメモに関係する既存メモのうち、全文を読みたいものを最大" + fmt.Sprint(maxKeeperReads) + "件選ぶ。index.md の題と説明から選ぶ。なければ空配列。"},
		{Role: "user", Content: base},
	}
	reply, err := client.Complete(ctx, llm.Request{Model: model, Messages: messages, Tools: []llm.Tool{chooseNotesTool}, ToolChoice: "required"})
	if err != nil {
		return nil
	}
	var chosen struct {
		Paths []string `json:"paths"`
	}
	for _, call := range reply.Calls {
		if call.Name == chooseNotesTool.Name {
			_ = json.Unmarshal([]byte(call.Args), &chosen)
			break
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, path := range chosen.Paths {
		path = strings.TrimPrefix(strings.TrimSpace(path), "/")
		if path == "" || seen[path] || len(out) == maxKeeperReads {
			continue
		}
		for _, note := range notes {
			if note.Path == path && !reservedNote(path) {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	return out
}

func withoutPath(notes []store.Note, path string) []store.Note {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	var out []store.Note
	for _, note := range notes {
		if note.Path != path {
			out = append(out, note)
		}
	}
	return out
}

// keeperUser is what the keeper is shown: the index whole, what needs tidying,
// the thread and the answer.
func keeperUser(k keeperTurn, notes []store.Note, index string) string {
	unlinked, groups := tidyHints(notes, index)
	lines := []string{
		"いまの日時: " + time.Now().In(k.here).Format("2006-01-02 15:04 MST"),
		"",
		fmt.Sprintf("あなたのメモ: %d件", countNotes(notes)),
		"",
		"あなたの index.md（全文）:",
		index,
	}
	if strings.Contains(index, unsortedHeading) {
		lines = append(lines, "", "index.md の「"+strings.TrimPrefix(unsortedHeading, "# ")+"」の見出しにあるメモは、本体が仮に置いたものです。合う見出しへ移せるものは移してください。")
	}
	if len(unlinked) > 0 || len(groups) > 0 {
		lines = append(lines, "", "整理が必要（本体が見つけたもの）:")
		for i, note := range unlinked {
			if i == maxHints {
				lines = append(lines, "- ほか"+fmt.Sprint(len(unlinked)-maxHints)+"件が索引にありません")
				break
			}
			lines = append(lines, "- 索引にありません: "+noteLine(note))
		}
		for i, group := range groups {
			if i == maxHints {
				break
			}
			var parts []string
			for _, note := range group {
				parts = append(parts, noteLine(note))
			}
			lines = append(lines, "- 同じ・よく似た題名: "+strings.Join(parts, " ／ "))
		}
	}
	answer := strings.TrimSpace(k.answer)
	if answer == "" {
		answer = "（返事はしていません）"
	}
	lines = append(lines,
		"",
		"スレッド全体（古い順）:",
		timeline.Render(k.thread, k.names, k.here),
		"",
		"今回の対象発言:",
		k.trigger.Render(k.names, k.here),
		"",
		"完成した Answer（すでに返事として出ています。メモに写さない）:",
		answer,
	)
	return strings.Join(lines, "\n")
}

func countNotes(notes []store.Note) int {
	n := 0
	for _, note := range notes {
		if !reservedNote(note.Path) {
			n++
		}
	}
	return n
}

// noteLine names a note the way an index line would: path, title, description.
func noteLine(note store.Note) string {
	line := note.Path
	if title := noteTitle(note.Text); title != "" {
		line += "（" + title + "）"
	}
	if description := noteDescription(note.Text); description != "" {
		line += " " + description
	}
	return line
}

var descriptionLine = regexp.MustCompile(`(?m)^description:[ \t]*["']?([^"'\n]+?)["']?[ \t]*$`)

// noteDescription is the description in a note's front matter, or "".
func noteDescription(text string) string {
	front, ok := frontMatter(text)
	if !ok {
		return ""
	}
	if match := descriptionLine.FindStringSubmatch(front); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// indexLink is a markdown link in the index: its text and where it points.
var indexLink = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)

// linkTarget reduces a link to a note path inside the bundle, or "" for one that
// points somewhere else.
func linkTarget(target string) string {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
		return ""
	}
	if hash := strings.Index(target, "#"); hash >= 0 {
		target = target[:hash]
	}
	target = strings.TrimPrefix(strings.TrimPrefix(target, "./"), "/")
	if target == "" {
		return ""
	}
	if !strings.HasSuffix(target, ".md") {
		target += ".md"
	}
	return target
}

// linkedPaths are the notes an index links to.
func linkedPaths(index string) map[string]bool {
	out := map[string]bool{}
	for _, match := range indexLink.FindAllStringSubmatch(index, -1) {
		if path := linkTarget(match[2]); path != "" {
			out[path] = true
		}
	}
	return out
}

// tidyHints finds what the keeper should put right while it is there: notes the
// index does not link, and groups of notes that carry one title under different
// paths.
func tidyHints(notes []store.Note, index string) (unlinked []store.Note, groups [][]store.Note) {
	linked := linkedPaths(index)
	var real []store.Note
	for _, note := range notes {
		if reservedNote(note.Path) {
			continue
		}
		real = append(real, note)
		if !linked[note.Path] {
			unlinked = append(unlinked, note)
		}
	}
	used := make([]bool, len(real))
	for i := range real {
		if used[i] {
			continue
		}
		key := sameTitleKey(noteTitle(real[i].Text))
		if key == "" {
			continue
		}
		group := []store.Note{real[i]}
		for j := i + 1; j < len(real); j++ {
			if !used[j] && nearTitles(key, sameTitleKey(noteTitle(real[j].Text))) {
				used[j] = true
				group = append(group, real[j])
			}
		}
		if len(group) > 1 {
			groups = append(groups, group)
		}
	}
	return unlinked, groups
}

// indexProblems says what a proposal would leave wrong in index.md for the notes
// it touches: a note it writes that has no section and is not listed, or a link
// without a title. The line of a forgotten note is taken out by the app. What
// was already wrong elsewhere is not held against it - repairIndex sees to that
// - and a proposal that changes no notes is not asked anything.
func indexProblems(notes []store.Note, index string, ops []NoteOperation) []string {
	if len(ops) == 0 {
		return nil
	}
	text := index
	written := map[string]store.Note{}
	placed := map[string]bool{}
	for _, op := range ops {
		path := strings.TrimPrefix(strings.TrimSpace(op.Path), "/")
		if op.Type != "write" {
			continue
		}
		if path == "index.md" {
			text = op.Text
		} else if !reservedNote(path) {
			written[path] = store.Note{Path: path, Text: op.Text}
			placed[path] = strings.TrimSpace(op.Section) != ""
		}
	}
	var problems []string
	linked := linkedPaths(text)
	var missing []string
	for path, note := range written {
		if !linked[path] && !placed[path] {
			missing = append(missing, "  - "+noteLine(note))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		problems = append(problems, "index.md のどの見出しに載せるかが決まっていないメモがあります。section に見出しを書いてください（既存の見出しか、新しい見出し）:\n"+strings.Join(missing, "\n"))
	}
	for _, match := range indexLink.FindAllStringSubmatch(text, -1) {
		if path := linkTarget(match[2]); path != "" && strings.TrimSpace(match[1]) == "" && written[path].Path != "" {
			problems = append(problems, "index.md のリンク "+match[2]+" に題名がありません。[題名](path) の形で書いてください")
			break
		}
	}
	return problems
}

// repairIndex makes index.md complete whatever the keeper did: links to notes
// that are gone are taken out, and a note it does not link is added under a
// heading of its own. It reports whether it wrote. Grouping stays the persona's;
// this only guarantees nothing is missing or dead.
func (r *Runner) repairIndex(personaID string) (bool, error) {
	notes, err := r.allNotesChecked(personaID)
	if err != nil {
		return false, err
	}
	index, hasIndex := "", false
	var real []store.Note
	for _, note := range notes {
		if note.Path == "index.md" {
			index, hasIndex = note.Text, true
		} else if !reservedNote(note.Path) {
			real = append(real, note)
		}
	}
	if len(real) == 0 && !hasIndex {
		return false, nil
	}
	fixed := repairedIndex(index, real)
	if fixed == index {
		return false, nil
	}
	if err := r.store.WriteNote(personaID, "index.md", fixed); err != nil {
		return false, err
	}
	return true, nil
}

// unsortedHeading is where a note the keeper did not place is put.
const unsortedHeading = "# 未整理"

func repairedIndex(index string, notes []store.Note) string {
	exists := map[string]bool{"index.md": true, "log.md": true}
	for _, note := range notes {
		exists[note.Path] = true
	}
	var kept []string
	listed := map[string]bool{}
	for _, line := range strings.Split(index, "\n") {
		dead := false
		for _, match := range indexLink.FindAllStringSubmatch(line, -1) {
			if path := linkTarget(match[2]); path != "" {
				if !exists[path] || (linksTo(line, path) && listed[path]) {
					dead = true
				}
			}
		}
		if !dead {
			kept = append(kept, line)
			for _, match := range indexLink.FindAllStringSubmatch(line, -1) {
				if path := linkTarget(match[2]); path != "" && linksTo(line, path) {
					listed[path] = true
				}
			}
		}
	}
	text := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	linked := linkedPaths(text)
	for _, note := range notes {
		if !linked[note.Path] {
			// Under the one unsorted heading, wherever it already is.
			text = strings.TrimRight(placeNoteInIndex(text, strings.TrimPrefix(unsortedHeading, "# "), note), "\n")
		}
	}
	if text == "" {
		return ""
	}
	return text + "\n"
}

func pathBase(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
