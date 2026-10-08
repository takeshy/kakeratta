package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
)

// A field's summary is the persona's prose over its facts. It is rewritten at
// the persona's check-in - daily, or monthly for a person looked back on
// monthly - right after the check-in is claimed and before the persona is
// woken for it, so the check-in reads it fresh. Only the fields whose facts
// changed since the summary was written are rewritten; a persona with nothing
// new costs no model call. A rewrite that fails is tried at the next check-in.

// changedSince are a field's facts that came, went or were replaced on or
// after its summary's day: what the summary has not taken in yet. A summary
// never written has everything to take in.
func changedSince(ff fieldFacts) (added, gone []fact) {
	for _, f := range ff.Current {
		if ff.SummaryAt.IsZero() || f.Since.IsZero() || !f.Since.Before(ff.SummaryAt) {
			added = append(added, f)
		}
	}
	for _, f := range ff.Past {
		if !ff.SummaryAt.IsZero() && !f.Until.Before(ff.SummaryAt) {
			gone = append(gone, f)
		}
	}
	return added, gone
}

// staleFields are the fields whose summaries have something to take in, and
// whether each was already written today: one is not rewritten twice a day.
func staleFields(m memoryFacts, today time.Time) (due []string, later bool) {
	for _, field := range m.all() {
		ff := m.ByField[field]
		added, gone := changedSince(ff)
		if !ff.SummaryDirty && len(added) == 0 && len(gone) == 0 {
			continue
		}
		if !ff.SummaryAt.IsZero() && !ff.SummaryAt.Before(today) {
			later = true
			continue
		}
		due = append(due, field)
	}
	return due, later
}

func localDay(t time.Time, zone *time.Location) time.Time {
	local := t.In(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
}

type summaryWrite struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}

// factDates are the days a fact's words name: 2026-10-03, 2026年10月3日,
// 10/3, 10月3日. A day without a year is taken in the year the fact began to
// hold, or this year.
var factDates = regexp.MustCompile(`(?:(\d{4})[-/年])?(\d{1,2})[-/月](\d{1,2})日?`)

// datedPast says whether a fact names a day, and every day it names is before
// today: an appointment or a plan that may no longer be ahead.
func datedPast(f fact, today time.Time) bool {
	found := false
	for _, match := range factDates.FindAllStringSubmatch(f.Text, -1) {
		year := today.Year()
		if !f.Since.IsZero() {
			year = f.Since.Year()
		}
		if match[1] != "" {
			year, _ = strconv.Atoi(match[1])
		}
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		if month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		found = true
		if !time.Date(year, time.Month(month), day, 0, 0, 0, 0, today.Location()).Before(today) {
			return false
		}
	}
	return found
}

// upcomingDated says whether a fact that holds now names a day from today to
// horizon days ahead: a plan the persona may need to prepare for.
func upcomingDated(m memoryFacts, today time.Time, horizon int) bool {
	until := today.AddDate(0, 0, horizon)
	for _, field := range m.all() {
		for _, f := range m.ByField[field].Current {
			for _, match := range factDates.FindAllStringSubmatch(f.Text, -1) {
				year := today.Year()
				if !f.Since.IsZero() {
					year = f.Since.Year()
				}
				if match[1] != "" {
					year, _ = strconv.Atoi(match[1])
				}
				month, _ := strconv.Atoi(match[2])
				day, _ := strconv.Atoi(match[3])
				if month < 1 || month > 12 || day < 1 || day > 31 {
					continue
				}
				at := time.Date(year, time.Month(month), day, 0, 0, 0, 0, today.Location())
				if !at.Before(today) && !at.After(until) {
					return true
				}
			}
		}
	}
	return false
}

// pastDated are the facts that hold now and name only days that have gone.
func pastDated(m memoryFacts, today time.Time) map[string][]fact {
	out := map[string][]fact{}
	for _, field := range m.Fields {
		for _, f := range m.ByField[field].Current {
			if datedPast(f, today) && f.PastReviewed != f.reviewFingerprint(today.Location()) {
				out[field] = append(out[field], f)
			}
		}
	}
	return out
}

func summaryTool(fields []string, passed []string) llm.Tool {
	props := map[string]any{
		"summaries": map[string]any{"type": "array", "maxItems": len(fields), "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"field": map[string]any{"type": "string", "enum": fields},
				"text":  stringField(fmt.Sprintf("その分野のまとめ文の新しい全文（%d字以内）", maxFieldRunes)),
			},
			"required": []string{"field", "text"}, "additionalProperties": false,
		}},
	}
	required := []string{"summaries"}
	if len(passed) > 0 {
		props["invalidate"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": passed},
			"description": "日付が過ぎてもう当てはまらない予定・予約・約束の事実の id。過去の出来事は入れない。なければ []"}
		props["retain"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": passed}, "description": "日付が過ぎても保持する過去の出来事だと確認できた id。迷うものは入れない。なければ []"}
		required = append(required, "invalidate", "retain")
	}
	return llm.Tool{Name: "write_summaries", Description: "分野ごとのまとめ文と、日付の過ぎた事実の扱いを本体に渡す。本体が検証して保存する。",
		Parameters: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
}

func fieldSummaryPrompt(skill Skill) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、1日1回、分野ごとのまとめ文を書き直し、日付の過ぎた事実を見直す。\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "分野の分け方と残し方: " + memory + "\n"
	}
	s += `write_summaries で出す。
- invalidate: 「日付の過ぎた事実」として渡したもののうち、予定・予約・約束・期限のように、その日が過ぎてもう先のことではなくなったものの id。贈った・行った・言ったなどの過去の出来事は、日付が過ぎても事実のまま残すので入れない。迷うものも入れない。
- retain: 「日付の過ぎた事実」のうち、過去の出来事として今後も保持すると確認できた id。これらは本文や開始日が変わるまで日付による再審査をしない。迷うものは invalidate にも retain にも入れない。
- summaries: 「まとめ文を書き直す分野」として渡した分野それぞれのまとめ文の新しい全文。invalidate した事実がある分野も、その分を外して書き直してよい。
- まとめ文は、その分野の事実を読んだあなたの見立て。傾向、移り変わり、まだ済んでいないことが分かるように書く（` + fmt.Sprint(maxFieldRunes) + `字以内）。
- いまのまとめ文を土台に、前回から増えた・変わった・当てはまらなくなった事実を取り込む。変わらない部分は残す。
- 事実にないことを足さない。推測は推測と明記する。事実の id はまとめ文に書かない。
- 事実と同じ言語で書く。
`
	return s
}

// rewriteSummaries rewrites the summaries of a persona's fields that are due,
// and retires the facts whose day has passed, under its keeper lock, in one
// call. A field is handed when its facts changed since its summary, or when a
// fact in it names only days that have gone; with neither, nothing is asked.
// The model decides which dated facts no longer hold - a plan whose day has
// passed does not; an event that happened still does - and they move to the
// past. A due field the model leaves out is still marked as read today, so it
// is not asked again until something more changes.
func (r *Runner) rewriteSummaries(ctx context.Context, client *llm.Client, model string, skill Skill, now time.Time, zone *time.Location) error {
	ctx = llm.WithTrace(ctx, llm.Trace{Persona: skill.ID, Wake: "daily"})
	if !skill.Remembers() {
		return nil
	}
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return err
	}
	defer release()
	today := localDay(now, zone)
	m := r.loadFacts(skill, zone)
	due, _ := staleFields(m, today)
	passed := pastDated(m, today)
	if len(due) == 0 && len(passed) == 0 {
		return nil
	}
	handed := append([]string{}, due...)
	for _, field := range m.Fields {
		if len(passed[field]) > 0 && !contains(handed, field) {
			handed = append(handed, field)
		}
	}
	var passedIDs []string
	var parts []string
	for _, field := range handed {
		ff := m.ByField[field]
		lines := []string{"## " + field, "いまのまとめ文:"}
		if ff.Summary == "" {
			lines = append(lines, "（まだありません）")
		} else {
			lines = append(lines, ff.Summary)
		}
		if ff.SummaryDirty {
			lines = append(lines, "", "事実が訂正・統合・削除されています。日付が古い事実も含め、いま渡した事実だけを根拠にまとめ文を照合し直し、必ずこの分野の新しいまとめ文を提出してください。削除・訂正前の内容をまとめ文から引き継がないでください。")
		}
		added, gone := changedSince(ff)
		addedIDs, goneIDs, expiredIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
		if contains(due, field) {
			lines = append(lines, "", "（まとめ文を書き直す分野）")
			for _, f := range added {
				addedIDs[f.ID] = true
			}
			for _, f := range gone {
				goneIDs[f.ID] = true
			}
		}
		for _, f := range passed[field] {
			expiredIDs[f.ID] = true
			passedIDs = append(passedIDs, f.ID)
		}
		// Only a fact under date review, or one a past fact names as its
		// replacement, carries its id: the others are not to be answered by id.
		named := map[string]bool{}
		for _, f := range ff.Past {
			if (ff.SummaryDirty || goneIDs[f.ID]) && f.By != "" {
				named[f.By] = true
			}
		}
		item := func(id string, attrs []string, text string) string {
			if expiredIDs[id] || named[id] {
				return "- [" + id + "]（" + strings.Join(attrs, "、") + "） " + text
			}
			return "- （" + strings.Join(attrs, "、") + "） " + text
		}
		lines = append(lines, "", "事実一覧（本文は各1回。[ ] は日付の審査対象と置換先の id、属性は前回のまとめからの変化と日付の審査対象）:")
		for _, f := range ff.Current {
			attrs := []string{"現在"}
			if addedIDs[f.ID] {
				attrs = append(attrs, "追加・変更")
			}
			if expiredIDs[f.ID] {
				attrs = append(attrs, "日付の過ぎた事実")
			}
			lines = append(lines, item(f.ID, attrs, f.Text))
		}
		for _, f := range ff.Past {
			if !ff.SummaryDirty && !goneIDs[f.ID] {
				continue
			}
			attrs := []string{"以前の事実"}
			if goneIDs[f.ID] {
				attrs = append(attrs, "当てはまらなくなった")
			}
			if f.By != "" {
				attrs = append(attrs, "置換先:"+f.By)
			}
			lines = append(lines, item(f.ID, attrs, f.Text))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	user := "いまの日時: " + now.In(zone).Format("2006-01-02 15:04 MST") + "\n\n" + strings.Join(parts, "\n\n")
	tool := summaryTool(handed, passedIDs)
	messages := []llm.Message{{Role: "system", Content: fieldSummaryPrompt(skill)}, {Role: "user", Content: user}}
	var written []summaryWrite
	var retire, retain []string
	for attempt := 1; ; attempt++ {
		reply, err := client.Complete(held, llm.Request{Purpose: "memory_summary", Attempt: attempt, Model: model, Messages: messages, Tools: []llm.Tool{tool}, ToolChoice: "required"})
		if err != nil {
			return err
		}
		var submitted struct {
			Summaries  []summaryWrite `json:"summaries"`
			Invalidate []string       `json:"invalidate"`
			Retain     []string       `json:"retain"`
		}
		var problems []string
		raw := ""
		for _, call := range reply.Calls {
			if call.Name == tool.Name {
				raw = call.Args
				if err := json.Unmarshal([]byte(call.Args), &submitted); err != nil {
					problems = append(problems, "引数を JSON として読めません: "+err.Error())
				}
			}
		}
		if raw == "" {
			problems = append(problems, tool.Name+" で提出してください")
		}
		for _, write := range submitted.Summaries {
			text := strings.TrimSpace(repairNewlines(write.Text))
			if _, ok := fieldNamed(handed, write.Field); !ok {
				problems = append(problems, "分野「"+write.Field+"」は渡していません")
			}
			if n := len([]rune(text)); n == 0 || n > maxFieldRunes {
				problems = append(problems, fmt.Sprintf("分野「%s」のまとめ文が%d字です。1〜%d字にしてください", write.Field, n, maxFieldRunes))
			}
		}
		for _, field := range due {
			if !m.ByField[field].SummaryDirty {
				continue
			}
			found := false
			for _, write := range submitted.Summaries {
				if name, ok := fieldNamed(handed, write.Field); ok && name == field {
					found = true
				}
			}
			if !found {
				problems = append(problems, "分野「"+field+"」は訂正・削除を反映したまとめ文が必要です")
			}
		}
		for _, id := range submitted.Invalidate {
			if !contains(passedIDs, id) {
				problems = append(problems, id+" は日付の過ぎた事実として渡していません")
			}
		}
		for _, id := range submitted.Retain {
			if !contains(passedIDs, id) || contains(submitted.Invalidate, id) {
				problems = append(problems, id+" は保持できる審査対象ではありません")
			}
		}
		if len(problems) == 0 {
			written, retire, retain = submitted.Summaries, submitted.Invalidate, submitted.Retain
			break
		}
		if attempt == maxKeeperAttempts {
			return fmt.Errorf("summaries rejected %d times: %s", attempt, strings.Join(problems, "; "))
		}
		messages = append(messages[:2], llm.Message{Role: "user", Content: "前の提出は本体が差し戻しました。直して " + tool.Name + " でもう一度提出してください。\n- " + strings.Join(problems, "\n- ") + "\n前の提出:\n" + clip(raw, 12000)})
	}
	changed := map[string]bool{}
	if len(retire) > 0 {
		var ops []factOp
		for _, id := range retire {
			ops = append(ops, factOp{Op: "invalidate", ID: id})
		}
		next, fields, problems := planFacts(m, ops, "", now, zone)
		if len(problems) > 0 {
			return fmt.Errorf("retiring past facts: %s", strings.Join(problems, "; "))
		}
		m = next
		for _, field := range fields {
			changed[field] = true
		}
	}
	for _, field := range handed {
		ff := m.ByField[field]
		for i := range ff.Current {
			if contains(retain, ff.Current[i].ID) {
				ff.Current[i].PastReviewed = ff.Current[i].reviewFingerprint(zone)
				changed[field] = true
			}
		}
		m.ByField[field] = ff
	}
	for _, write := range written {
		field, _ := fieldNamed(handed, write.Field)
		ff := m.ByField[field]
		ff.Summary, ff.SummaryAt = strings.TrimSpace(repairNewlines(write.Text)), today
		ff.SummaryDirty = false
		m.ByField[field] = ff
		changed[field] = true
	}
	for _, field := range due {
		ff := m.ByField[field]
		if ff.Summary == "" {
			// Nothing to say over facts yet is still a reading of them.
			continue
		}
		ff.SummaryAt = today
		m.ByField[field] = ff
		changed[field] = true
	}
	var fields []string
	for _, field := range m.all() {
		if changed[field] {
			fields = append(fields, field)
		}
	}
	_, err = r.writeFacts(held, skill.ID, m, fields, zone)
	logKeeper(keeperRecord{Persona: skill.ID, Model: model, Summaries: fields, Retired: retire, Error: errorText(err)})
	return err
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
