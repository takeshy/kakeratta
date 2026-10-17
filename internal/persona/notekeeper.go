package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The note keeper looks after a persona's memory once the turn has run and its
// remark is out, so a memory that fails to come out costs the memory and never
// the answer.
//
// An ordinary post keeps only its record (keep_record.go); facts wait for the
// daily pass. An edit of a kakera, and a check-in handed records due for
// review, are handed the whole memory, so facts that rested on the kakera can
// be put right and the records settled. The app checks the returned fact
// operations, writes the memory file, and appends the record with the entry to
// go back to - the remark when the persona replied, the kakera when it did not.
//
// After the user deletes a post, the keeper is woken to tidy: the app has
// taken the post's records out, and the keeper takes out of the fields what
// rested on it. No record is made for a tidy-up.

// maxKeeperAttempts is how many submissions the keeper may have sent back
// before the turn's memory is given up for a later retry.
const maxKeeperAttempts = 3

// keeperTurn is what the keeper is given about the turn that just ran.
type keeperTurn struct {
	skill   Skill
	thread  []timeline.Entry
	trigger timeline.Entry
	answer  string
	wake    string
	here    *time.Location
	names   map[string]string
	// replyID is the persona's remark on the kakera, if it made one.
	replyID string
}

// recordable says whether the turn is about a kakera of the user's, which is
// what a record is made for.
func (k keeperTurn) recordable() bool {
	return k.trigger.ID != "" && k.trigger.Speaker == timeline.User && !tidiesUp(k.wake)
}

// keptMemory is what the keeper hands back.
type keptMemory struct {
	// Remember is a turn's own record only: false when the kakera holds
	// nothing to look back on.
	Remember *bool          `json:"remember,omitempty"`
	Summary  string         `json:"summary"`
	Tags     []string       `json:"tags"`
	Expires  string         `json:"expires"`
	Review   string         `json:"review"`
	Facts    []factOp       `json:"facts,omitempty"`
	Records  []recordChange `json:"records,omitempty"`
}

// recordChange is the keeper's decision on a record it was handed: its dates
// as they are to be ("" for none), or that it is forgotten.
type recordChange struct {
	ID      string `json:"id"`
	Expires string `json:"expires"`
	Review  string `json:"review"`
	Forget  bool   `json:"forget"`
}

func dateField(description string) map[string]any {
	return stringField(description + "。2026-11-01 の形。付けないなら空文字")
}

// keepMemoryTool is keep_memory for this turn: a record is asked for only
// with a kakera, and decisions on records only when some were handed.
func keepMemoryTool(recording bool, handed []record) llm.Tool {
	props := map[string]any{
		"facts": map[string]any{"type": "array", "maxItems": 12, "description": "事実の変更だけ。変えないなら空配列", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op":   map[string]any{"type": "string", "enum": []string{"add", "update", "invalidate", "merge"}},
				"id":   stringField("update・invalidate する事実の id（f_...）。ほかは空文字"),
				"ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "merge する事実の id を2件以上。ほかは []"},
				"text": stringField(fmt.Sprintf("add・update・merge の事実の文（1件、%d字以内）。invalidate は空文字", maxFactRunes)),
			},
			"required": []string{"op", "id", "ids", "text"}, "additionalProperties": false,
		}},
	}
	required := []string{"facts"}
	if recording {
		props["summary"] = stringField(fmt.Sprintf("今回のユーザーのかけらを1文で（%d字以内）", maxSummaryRunes))
		props["tags"] = map[string]any{"type": "array", "minItems": 1, "maxItems": maxTags, "items": map[string]any{"type": "string"}, "description": "あとで探す手がかりの語"}
		props["expires"] = dateField("この記録を本体が消す日")
		props["review"] = dateField("この記録をユーザーに見直してもらう日")
		required = append(required, "summary", "tags", "expires", "review")
	}
	if len(handed) > 0 {
		var ids []string
		for _, rec := range handed {
			ids = append(ids, rec.ID)
		}
		props["records"] = map[string]any{"type": "array", "maxItems": len(ids), "description": "渡された記録のうち、日付を変える・忘れるものだけ", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "enum": ids},
				"expires": dateField("変えたあとの expires（変えないならいまの日付をそのまま）"),
				"review":  dateField("変えたあとの review（変えないならいまの日付をそのまま）"),
				"forget":  map[string]any{"type": "boolean", "description": "記録を忘れるなら true"},
			},
			"required": []string{"id", "expires", "review", "forget"}, "additionalProperties": false,
		}}
		required = append(required, "records")
	}
	return llm.Tool{Name: "keep_memory", Description: "覚えておくことを本体に渡す。本体が検証して保存する。",
		Parameters: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
}

// keeperDates is how a record's dates are chosen, as the keeper is told.
const keeperDates = `記録の日付（expires / review）の規則:
- expires は、その場限りのかけら（「今日のランチどこがいい？」「渋谷までの行き方」のような、その日のうちに用が済む相談や質問）の記録に付ける。翌日の日付でよい。過ぎたら本体が記録を消す。あとまで残る事実・好み・予定・行きたい場所などには付けない。
- review は、ユーザーから聞いたことのうち、いつまで要るか分からないものに付ける。内容で日付を決める。行きたい店なら数週間後、関心のある話題なら数か月後。好みや習慣には付けない。過ぎても本体は消さず、チェックインのときにあなたがユーザーに聞く。
- どちらも、あとで担当が records でその記録を読むと、付けた長さの分だけ先へ延びる。
- どちらも要らなければ空文字。
`

// keeperPrompt opens the way phasePrompt does, so a persona is one voice across
// its turn and a log keyed by the opening line stays whole.
func keeperPrompt(skill Skill, recording bool, handed []record) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、記憶だけを整える。返事や予定はすでに決まっていて、ここでは変えない。保存は本体が行う。\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "残し方: " + memory + "\n"
	}
	if recording {
		s += `keep_memory で次を1回で提出する。
- summary: 今回のユーザーのかけらを1文で。ユーザーが何を言った・聞いたかを、ユーザーの言葉に沿って書く（` + fmt.Sprint(maxSummaryRunes) + `字以内）。本体がタグと、たどれる投稿のIDを付けて記録に残す。
- tags: そのかけらをあとで探す手がかりの語を1〜` + fmt.Sprint(maxTags) + `つ（料理の種類・場所・主題・人など）。記録で使っているタグがあれば同じ表記を使う。
- expires / review: この記録の日付（下の規則）。
- facts: 今回のかけらで分かった・変わった事実の変更だけ（下の規則）。変わらなければ空配列。
`
	} else {
		s += "いまはチェックインで、見直しの日が来た記録を渡された。keep_memory の records で、返事で聞いた記録の review を1週間先に書き直す。聞かなかった記録は records に入れない。事実を変える必要がなければ facts は空配列。\n"
	}
	if recording && len(handed) > 0 {
		s += "今回のかけらは、あなたが「そろそろ忘れていい？」と聞いた投稿への返事。keep_memory の records で、聞いた記録を返事のとおりに扱う。残すなら review を空に、延ばすなら先の日付に、もういいなら forget を true に。\n"
	}
	return s + keeperDates + keeperFacts
}

// keeperFacts is how the facts are changed, as the keeper is told.
var keeperFacts = `事実の規則:
- 記憶は事実の一覧。あなたは全文を書かず、変更だけを出す。本体が今回のかけらを根拠として付けて保存する。
  - add: 新しく分かった事実を1件足す（text）。
  - update: いまの事実が変わった・詳しくなったとき、その id と新しい text。前の文は残らない。
  - invalidate: もう当てはまらなくなった事実の id（「もう行った」「やめた」など）。事実は消える。
  - merge: 似た事実をまとめる。ids と、まとめた text。
- 1件は1つのこと、` + fmt.Sprint(maxFactRunes) + `字以内。すでにある事実と同じことを add しない。言い換えるだけの update もしない。
- いまの事実は合わせて` + fmt.Sprint(maxFactsRunes) + `字まで。収まらないときは merge で傾向にまとめ、個々の出来事は記録に任せる。
- 中心はユーザーが言ったこと・したこと（事実・好み・予定・関係の変化・覚えてと頼まれたこと。贈った・行ったなどの出来事は日付とともに）。あなたや他の担当が調べた事実・候補一覧・解説は事実にしない。
- 引用、本人の意見、推測を区別し、推測は推測と明記する。Answer の文章を写さない。予定の id（s_...）を書くなら、一覧にあるものだけ。
`

// lockKeeper serialises one persona's keeper: two turns finishing together -
// here or in another process serving the workspace - must not each rewrite the
// memory file from what they saw.
//
// The context it returns ends if the lock is lost; the keeper works under it.
func (r *Runner) lockKeeper(ctx context.Context, personaID string) (context.Context, func(), error) {
	return r.store.Lock(ctx, "keeper:"+personaID)
}

// keeperRecord is one keeper run, for the log.
type keeperRecord struct {
	Severity  string      `json:"severity"`
	Message   string      `json:"message"`
	Persona   string      `json:"persona"`
	Trigger   string      `json:"trigger,omitempty"`
	Model     string      `json:"model,omitempty"`
	Attempts  int         `json:"attempts,omitempty"`
	Skipped   bool        `json:"skipped,omitempty"`
	Kept      *keptMemory `json:"kept,omitempty"`
	Summaries []string    `json:"summaries,omitempty"`
	Retired   []string    `json:"retired,omitempty"`
	Dropped   []string    `json:"dropped,omitempty"`
	Reviewed  []string    `json:"reviewed,omitempty"`
	Suggested string      `json:"suggested,omitempty"`
	Error     string      `json:"error,omitempty"`
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

// keepNotes keeps what the turn leaves behind and returns the fields it wrote.
// A failure never retracts the answer; the caller persists it for retry.
func (r *Runner) keepNotes(ctx context.Context, client *llm.Client, settings store.Settings, k keeperTurn) (written []string, failure error) {
	ctx = llm.WithTrace(ctx, llm.Trace{Persona: k.skill.ID, Trigger: k.trigger.ID, Wake: wakeKind(k.trigger, false, k.wake)})
	if !k.skill.Remembers() {
		return nil, nil
	}
	model := personaModel(settings)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil || client.BaseURL == "" || model == "" {
		return nil, fmt.Errorf("note keeper has no model connection")
	}
	ctx, release, err := r.lockKeeper(ctx, k.skill.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	logged := keeperRecord{Persona: k.skill.ID, Trigger: k.trigger.ID, Model: model}
	defer func() {
		if problem := recover(); problem != nil {
			failure = fmt.Errorf("note keeper panic: %v", problem)
		}
		if failure != nil {
			logged.Error = failure.Error()
		}
		logKeeper(logged)
	}()

	recordable := k.recordable()
	handed := r.handedRecords(k)
	if recordable && len(handed) == 0 && !strings.Contains(k.wake, editedInstructions) {
		var kept keptMemory
		kept, logged.Attempts, written, err = r.keepRecord(ctx, client, model, k)
		if err == nil {
			logged.Kept = &kept
			logged.Skipped = kept.Remember != nil && !*kept.Remember
		}
		return written, err
	}
	// An edit can correct facts that rested on the kakera, and a due review
	// settles the specific records supplied by the host.
	memory := r.loadMemo(k.skill.ID, k.here)
	user := strings.Join([]string{
		"いまの日時: " + time.Now().In(k.here).Format("2006-01-02 15:04 MST"),
		"",
		"いまの記憶:",
		renderFacts(memory, k.here),
		"",
		threadContext(k.thread, k.trigger, k.names, k.here),
	}, "\n")
	if len(handed) > 0 {
		user += "\n\n見直しの対象として渡された記録（records で扱えるのはこれだけ）:\n" + listRecords(handed, k.here)
		if !recordable {
			// The review keeper must know which record the check-in actually
			// asked about before moving its review date.
			user += "\n\n今回の見直しの質問:\n" + k.answer
		}
	}
	now := time.Now()
	// The facts rest on the kakera itself, never on a reply to it: deleting
	// the kakera is what takes them away.
	from := ""
	if recordable {
		from = k.trigger.ID
	}
	var planned memo
	var changed bool
	system := keeperPrompt(k.skill, recordable, handed)
	kept, attempts, err := submitMemory(ctx, client, model, system, user, keepMemoryTool(recordable, handed), func(kept keptMemory) []string {
		problems := checkKept(kept, recordable, handed, k.answer, now, k.here)
		var factProblems []string
		planned, changed, factProblems = planFacts(memory, kept.Facts, from, now, k.here)
		return append(problems, factProblems...)
	})
	logged.Attempts = attempts
	if err != nil {
		return nil, err
	}
	logged.Kept = &kept
	if changed {
		if err := r.saveMemo(ctx, k.skill.ID, planned, k.here); err != nil {
			return nil, err
		}
		written = []string{MemoPath}
	}
	if recordable {
		id := k.replyID
		if id == "" {
			id = k.trigger.ID
		}
		rec := record{At: k.trigger.At, ID: id, Summary: cleanSummary(kept.Summary), Of: k.trigger.ID,
			Expires: dateFrom(kept.Expires, k.trigger.At, k.here), Review: dateFrom(kept.Review, k.trigger.At, k.here)}
		for _, tag := range kept.Tags {
			if tag = cleanTag(tag); tag != "" {
				rec.Tags = append(rec.Tags, tag)
			}
		}
		if err := r.appendRecord(ctx, k.skill.ID, rec, k.trigger, k.here); err != nil {
			return written, err
		}
		written = []string{MemoPath}
	}
	if len(kept.Records) > 0 {
		// A check-in that asked about a record marks it with the post that
		// asked, so the reply to that post is known to be about it; the
		// reply settles it and the mark goes.
		asked := ""
		if !recordable {
			asked = k.replyID
		}
		forgotten, err := r.changeRecords(ctx, k.skill.ID, kept.Records, asked, now, k.here)
		if err != nil {
			return written, err
		}
		// A kakera the user let go is let go of in the facts too: those that
		// rested on it alone go with its record.
		if _, err := r.dropFactSourcesHeld(ctx, k.skill.ID, forgotten, k.here); err != nil {
			return written, err
		}
	}
	return written, nil
}

// changeRecords applies the keeper's decisions on the records it was handed.
// A date left as it was keeps its span; a new one is counted from now.
func (r *Runner) changeRecords(ctx context.Context, personaID string, changes []recordChange, asked string, now time.Time, zone *time.Location) (map[string]bool, error) {
	byID := map[string]recordChange{}
	for _, change := range changes {
		byID[change.ID] = change
	}
	forgotten := map[string]bool{}
	err := r.eachRecord(ctx, personaID, zone, func(rec *record) (bool, bool) {
		change, ok := byID[rec.ID]
		if !ok {
			return true, false
		}
		if change.Forget {
			forgotten[r.kakeraOf(*rec)] = true
			return false, true
		}
		rec.Expires = redated(rec.Expires, change.Expires, now, zone)
		rec.Review = redated(rec.Review, change.Review, now, zone)
		rec.Asked = asked
		return true, true
	})
	return forgotten, err
}

// kakeraOf is the kakera a record is of: its own id, unless that is a reply of
// the persona's, in which case the kakera the reply answered.
func (r *Runner) kakeraOf(rec record) string {
	if rec.Of != "" {
		return rec.Of
	}
	if entry, ok := r.store.Entry(rec.ID); ok && entry.Speaker != timeline.User && entry.ReplyTo != "" {
		return entry.ReplyTo
	}
	return rec.ID
}

// redated is a record date as the keeper wrote it: the same day keeps its
// span, another day starts one from now, and "" clears it.
func redated(current recordDate, raw string, now time.Time, zone *time.Location) recordDate {
	next := dateFrom(raw, now, zone)
	if current.set() && next.set() && next.Day.Equal(current.Day) {
		return current
	}
	return next
}

// submitMemory asks for keep_memory until a submission passes check, or the
// attempts run out.
func submitMemory(ctx context.Context, client *llm.Client, model, system, user string, tool llm.Tool, check func(keptMemory) []string) (keptMemory, int, error) {
	messages := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	var last, lastProblems string
	for attempt := 1; attempt <= maxKeeperAttempts; attempt++ {
		reply, err := client.Complete(ctx, llm.Request{Purpose: "memory_update", Attempt: attempt, Model: model, Messages: messages, Tools: []llm.Tool{tool}, ToolChoice: "required"})
		if err != nil {
			return keptMemory{}, attempt, err
		}
		var kept keptMemory
		var problems []string
		found := false
		for _, call := range reply.Calls {
			if call.Name != tool.Name {
				continue
			}
			found = true
			if err := json.Unmarshal([]byte(call.Args), &kept); err != nil {
				problems = append(problems, "引数を JSON として読めません: "+err.Error())
			} else {
				problems = check(kept)
			}
			last = call.Args
			break
		}
		if !found {
			problems = append(problems, tool.Name+" で提出してください")
		}
		if len(problems) == 0 {
			return kept, attempt, nil
		}
		lastProblems = strings.Join(problems, "; ")
		messages = append(messages[:2], llm.Message{Role: "user", Content: "前の提出は本体が差し戻しました。直して " + tool.Name + " でもう一度提出してください。\n" +
			"- " + strings.Join(problems, "\n- ") + "\n前の提出:\n" + clip(last, 12000)})
	}
	return keptMemory{}, maxKeeperAttempts, fmt.Errorf("memory submission rejected %d times: %s", maxKeeperAttempts, lastProblems)
}

// checkKept says what is wrong with a submission.
func checkKept(kept keptMemory, recording bool, handed []record, answer string, now time.Time, zone *time.Location) []string {
	var problems []string
	today := now.In(zone)
	tomorrow := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, zone)
	date := func(name, raw string, current recordDate) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		day, err := time.ParseInLocation("2006-01-02", raw, zone)
		switch {
		case err != nil:
			problems = append(problems, name+" は 2026-11-01 の形の日付か空文字にしてください: "+raw)
		case current.set() && day.Equal(current.Day):
		case day.Before(tomorrow):
			problems = append(problems, name+" は明日以降の日付にしてください: "+raw)
		}
	}
	if recording {
		date("expires", kept.Expires, recordDate{})
		date("review", kept.Review, recordDate{})
	}
	known := map[string]record{}
	for _, rec := range handed {
		known[rec.ID] = rec
	}
	changed := map[string]bool{}
	for _, change := range kept.Records {
		rec, ok := known[change.ID]
		switch {
		case !ok:
			problems = append(problems, "記録 "+change.ID+" は渡されていません。渡された記録の id だけを使ってください")
			continue
		case changed[change.ID]:
			problems = append(problems, "記録 "+change.ID+" が2回あります")
		}
		changed[change.ID] = true
		if !change.Forget {
			date("記録 "+change.ID+" の expires", change.Expires, rec.Expires)
			date("記録 "+change.ID+" の review", change.Review, rec.Review)
		}
	}
	if recording {
		summary := cleanSummary(kept.Summary)
		switch {
		case summary == "":
			problems = append(problems, "summary がありません")
		case len([]rune(summary)) > maxSummaryRunes:
			problems = append(problems, fmt.Sprintf("summary が長すぎます（%d字。%d字以内の1文に）", len([]rune(summary)), maxSummaryRunes))
		}
		tags := 0
		for _, tag := range kept.Tags {
			tag = cleanTag(tag)
			if tag == "" {
				continue
			}
			tags++
			if len([]rune(tag)) > maxTagRunes {
				problems = append(problems, "タグ「"+tag+"」が長すぎます。短い語にしてください")
			}
		}
		if tags == 0 || tags > maxTags {
			problems = append(problems, fmt.Sprintf("tags は1〜%dつにしてください", maxTags))
		}
	}
	for i, op := range kept.Facts {
		if copiesAnswer(answer, op.Text) {
			problems = append(problems, fmt.Sprintf("facts[%d] に Answer の文章をそのまま写しています。ユーザーが言ったことを事実にしてください", i))
		}
	}
	return problems
}

// copiesAnswer reports a body that carries two or more of the answer's own
// sentences. The answer stays in the thread; a body holding it is a cache of
// what was researched, not what the user said.
func copiesAnswer(answer, text string) bool {
	body := squash(text)
	copied := 0
	for _, sentence := range strings.FieldsFunc(answer, func(r rune) bool {
		return r == '。' || r == '！' || r == '？' || r == '\n' || r == '.' || r == '!' || r == '?'
	}) {
		sentence = squash(sentence)
		if len([]rune(sentence)) >= copiedSentenceRunes && strings.Contains(body, sentence) {
			copied++
		}
	}
	return copied >= copiedSentences
}

// appendRecord adds rec to the memory file. A kakera has one record: one
// already kept for it - from before the user edited it, or a retry that got
// this far - is replaced, and one that said otherwise has the daily pass write
// the summary again.
func (r *Runner) appendRecord(ctx context.Context, personaID string, rec record, trigger timeline.Entry, zone *time.Location) error {
	m := r.loadMemo(personaID, zone)
	var kept []record
	for _, old := range m.Records {
		if !r.recordOf(old, trigger, personaID) {
			kept = append(kept, old)
		} else if old.Summary != rec.Summary {
			m.SummaryDirty = true
		}
	}
	m.Records = append(kept, rec)
	sort.SliceStable(m.Records, func(i, j int) bool { return m.Records[i].At.Before(m.Records[j].At) })
	return r.saveMemo(ctx, personaID, m, zone)
}

// recordOf says whether old is a record of trigger: kept under the kakera's
// own ID, or under a remark of the persona's on it.
func (r *Runner) recordOf(old record, trigger timeline.Entry, personaID string) bool {
	if old.ID == trigger.ID || old.Of == trigger.ID {
		return true
	}
	if old.At.Before(trigger.At.Truncate(time.Minute)) || old.At.After(trigger.At) {
		return false
	}
	entry, ok := r.store.Entry(old.ID)
	return ok && entry.Speaker == personaID && entry.ReplyTo == trigger.ID
}

// dropRecords takes the records kept under the deleted entries out of the
// months they could be in, and reports whether any were. A reply of the
// persona's deleted on its own leaves the kakera it answered: its record is
// kept under the kakera instead, which is not reported as a drop.
func (r *Runner) dropRecords(ctx context.Context, personaID string, gone []timeline.Entry, zone *time.Location) (bool, error) {
	ctx, release, err := r.lockKeeper(ctx, personaID)
	if err != nil {
		return false, err
	}
	defer release()
	ids := map[string]bool{}
	for _, entry := range gone {
		ids[entry.ID] = true
	}
	rekey := map[string]string{}
	for _, entry := range gone {
		if entry.Speaker != timeline.User && entry.ReplyTo != "" && !ids[entry.ReplyTo] {
			rekey[entry.ID] = entry.ReplyTo
			delete(ids, entry.ID)
		}
	}
	dropped := false
	err = r.eachRecord(ctx, personaID, zone, func(rec *record) (bool, bool) {
		if ids[rec.ID] || ids[rec.Of] {
			dropped = true
			return false, true
		}
		if id := rekey[rec.ID]; id != "" {
			rec.ID = id
			return true, true
		}
		return true, false
	})
	return dropped, err
}
