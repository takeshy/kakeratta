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

// The daily pass tidies a persona's memory at its check-in - daily, or
// monthly for a person looked back on monthly - right after the check-in is
// claimed and before the persona is woken for it, so the check-in reads it
// fresh. A turn only appends a record (keep_record.go); here the persona, in
// one call, folds the records kept since the pass last read them into facts,
// merges facts that say the same, drops facts that no longer hold, deletes the
// records it folded away, hands records that look done to the user's review,
// and enriches the facts. With nothing new it costs no model call. A pass
// that fails is tried at the next check-in.
//
// Memory is not split by subject. When what a persona keeps plainly falls
// apart into a subject of its own, the pass suggests a new persona, and the
// check-in asks the user (personaSuggestInstructions).

// maxTidyRecords is how many records a pass is handed, newest first. Older
// ones wait for a later pass.
const maxTidyRecords = 150

// maxReviewRecords is how many records one pass may hand to the user's review.
const maxReviewRecords = 5

// suggestEvery is how long after one suggestion of a new persona the next may
// come.
const suggestEvery = 30 * 24 * time.Hour

// tidyRecords are the records the daily pass may fold or drop: those not set
// to expire, which the sweep deletes, newest first.
func tidyRecords(m memo) []record {
	var out []record
	for i := len(m.Records) - 1; i >= 0 && len(out) < maxTidyRecords; i-- {
		if !m.Records[i].Expires.set() {
			out = append(out, m.Records[i])
		}
	}
	return out
}

// isNew says whether a record was kept since the daily pass last read them.
func (m memo) isNew(rec record) bool {
	return m.CheckedAt.IsZero() || rec.At.After(m.CheckedAt)
}

// tidyDue says whether the daily pass has something to read: records kept
// since it last read them, or facts corrected, merged or removed since.
func tidyDue(m memo) bool {
	if m.TidyDirty {
		return true
	}
	for _, rec := range tidyRecords(m) {
		if m.isNew(rec) {
			return true
		}
	}
	return false
}

func localDay(t time.Time, zone *time.Location) time.Time {
	local := t.In(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
}

// factDates are the days a fact's words name: 2026-10-03, 2026年10月3日,
// 10/3, 10月3日. A day without a year is taken in the year the fact began to
// hold, or this year.
var factDates = regexp.MustCompile(`(?:(\d{4})[-/年])?(\d{1,2})[-/月](\d{1,2})日?`)

// factDays are the days a text names, read in the year since began, or this
// year.
func factDays(text string, since, today time.Time) []time.Time {
	var out []time.Time
	for _, match := range factDates.FindAllStringSubmatch(text, -1) {
		year := today.Year()
		if !since.IsZero() {
			year = since.Year()
		}
		if match[1] != "" {
			year, _ = strconv.Atoi(match[1])
		}
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		if month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		out = append(out, time.Date(year, time.Month(month), day, 0, 0, 0, 0, today.Location()))
	}
	return out
}

// datedPast says whether a fact names a day, and every day it names is before
// today: an appointment or a plan that may no longer be ahead.
func datedPast(f fact, today time.Time) bool {
	days := factDays(f.Text, f.Since, today)
	for _, day := range days {
		if !day.Before(today) {
			return false
		}
	}
	return len(days) > 0
}

// upcomingDated says whether memory names a day from today to horizon days
// ahead: a plan the persona may need to prepare for.
func upcomingDated(m memo, today time.Time, horizon int) bool {
	until := today.AddDate(0, 0, horizon)
	evidence := append([]fact(nil), m.Facts...)
	for _, rec := range m.Records {
		evidence = append(evidence, fact{Text: rec.Summary, Since: rec.At})
	}
	for _, f := range evidence {
		for _, day := range factDays(f.Text, f.Since, today) {
			if !day.Before(today) && !day.After(until) {
				return true
			}
		}
	}
	return false
}

// pastDated are the facts that name only days that have gone, and were not
// already kept as past events.
func pastDated(m memo, today time.Time) []fact {
	var out []fact
	for _, f := range m.Facts {
		if datedPast(f, today) && f.PastReviewed != f.reviewFingerprint(today.Location()) {
			out = append(out, f)
		}
	}
	return out
}

// tidyOp is a fact change on the daily pass, with the records it rests on.
type tidyOp struct {
	factOp
	Records []string `json:"records"`
}

// personaSuggestion is what the daily pass found could be a persona of its
// own.
type personaSuggestion struct {
	Topic  string `json:"topic"`
	Reason string `json:"reason"`
}

func tidyTool(recordIDs, passed []string, suggest bool) llm.Tool {
	ids := map[string]any{"type": "string"}
	if len(recordIDs) > 0 {
		ids = map[string]any{"type": "string", "enum": recordIDs}
	}
	props := map[string]any{
		"facts": map[string]any{"type": "array", "maxItems": 30, "description": "事実の変更だけ。変えないなら []", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op":      map[string]any{"type": "string", "enum": []string{"add", "update", "invalidate", "merge"}},
				"id":      stringField("update・invalidate する事実の id（f_...）。ほかは空文字"),
				"ids":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "merge する事実の id を2件以上。ほかは []"},
				"text":    stringField(fmt.Sprintf("add・update・merge の事実の文（1件、%d字以内）。invalidate は空文字", maxFactRunes)),
				"records": map[string]any{"type": "array", "items": ids, "description": "この事実の根拠にした記録の id。なければ []"},
			},
			"required": []string{"op", "id", "ids", "text", "records"}, "additionalProperties": false,
		}},
	}
	required := []string{"facts"}
	if len(recordIDs) > 0 {
		props["drop_records"] = map[string]any{"type": "array", "items": ids, "description": "事実に取り込んだので要らなくなった記録の id。なければ []"}
		props["review_records"] = map[string]any{"type": "array", "maxItems": maxReviewRecords, "items": ids, "description": "事実にはしないが、もう要らなさそうな記録の id。ユーザーに聞く。なければ []"}
		required = append(required, "drop_records", "review_records")
	}
	if len(passed) > 0 {
		props["invalidate"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": passed},
			"description": "日付が過ぎてもう当てはまらない予定・予約・約束の事実の id。過去の出来事は入れない。なければ []"}
		props["retain"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": passed}, "description": "日付が過ぎても保持する過去の出来事だと確認できた id。迷うものは入れない。なければ []"}
		required = append(required, "invalidate", "retain")
	}
	if suggest {
		props["suggest_persona"] = map[string]any{"type": []string{"object", "null"}, "description": "新しい担当に分けるとよい主題。ふだんは null",
			"properties": map[string]any{
				"topic":  stringField("新しい担当に任せるとよい主題（短く）"),
				"reason": stringField("そう考えた理由（記録や事実の傾向、1文）"),
			},
			"required": []string{"topic", "reason"}, "additionalProperties": false}
		required = append(required, "suggest_persona")
	}
	return llm.Tool{Name: "tidy_memory", Description: "記憶の整理を本体に渡す。本体が検証して保存する。",
		Parameters: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
}

func tidyPrompt(skill Skill, suggest bool) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、1日1回、記憶を整理する。\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "残し方: " + memory + "\n"
	}
	s += `記憶は「いまの事実」「かけらの記録」。記録は会話のたびに本体が1行ずつ足したもの。事実はいま当てはまることだけを持ち、当てはまらなくなった事実は消す（前の文は残らない。元の投稿はタイムラインに残る）。
tidy_memory で1回で出す。
- facts: 記録から分かった・変わった事実の変更。根拠にした記録の id を records に書く。
  - add: 新しい事実を1件（text）。
  - update: 事実が変わった・詳しくなった（id と新しい text）。
  - invalidate: もう当てはまらない事実の id（「もう行った」「やめた」など）。
  - merge: 似た・重なる事実をまとめる（ids と、まとめた text）。
  - 1件は1つのこと、` + fmt.Sprint(maxFactRunes) + `字以内。すでにある事実と同じことを add しない。事実は合わせて` + fmt.Sprint(maxFactsRunes) + `字まで。収まらないときは merge で傾向にまとめる。
  - 事実にするのはユーザーが言ったこと・したこと（好み・習慣・予定・関係・頼まれたこと。贈った・行ったなどの出来事は日付とともに）。担当が調べたこと・提案したことは事実にしない。推測は推測と明記する。
  - その場限りの相談や一度きりの質問は事実にしない。
  - 事実だけを読んで、その人の状況を理解できるようにする。好み・習慣の傾向、理由、条件や例外、関係、未完了の予定を、根拠がある範囲で具体的に残す。単に短くするために大切な違いを落とさない。
  - 好みや状況の変化自体が今後も役立つなら、以前と現在、分かる時期を1件の事実に含める（例: 「以前は豚骨を好んだが、10月からは塩を選ぶことが多い」）。日付・理由・因果関係を推測で補わない。以前の好みを現在の好みとして別に残さない。
  - 傾向は反復した記録や既存の事実を根拠にする。1回の出来事から習慣を断定しない。訂正・削除された内容を変化の履歴として復活させない。
  - add には根拠の記録の id が必須。既存の事実を詳しくするなら update、複数の事実を結び付けるなら merge を使い、元の投稿への根拠を引き継ぐ。
- drop_records: 事実に取り込んだので要らなくなった記録の id。本体は、残る事実の根拠（records）になった記録だけを消す。
- review_records: 事実にはしないが、もう要らなさそうな古い記録の id（最大` + fmt.Sprint(maxReviewRecords) + `件）。本体は消さず、ユーザーに「忘れていい？」と聞く対象にする。迷うものは入れない。
- invalidate: 「日付の審査対象」の事実のうち、予定・予約・約束・期限のように、その日が過ぎてもう先のことではなくなったものの id。贈った・行った・言ったなどの過去の出来事は入れない。
- retain: 「日付の審査対象」のうち、過去の出来事として今後も保持すると確認できた id。迷うものはどちらにも入れない。
`
	if suggest {
		s += `- suggest_persona: 記憶の中に、この担当の領域から外れる、またははっきり別の主題がまとまってたまっているとき（例: 食べもの担当に料理の作り方の記録が何度も続く）だけ、その主題と理由。記憶を分ける代わりに、ユーザーに新しい担当を作ることを提案する。ふだんは null。
`
	}
	return s
}

// tidyMemory is the daily pass over one persona's memory, under its
// keeper lock, in one call. It runs when records were kept since the last
// pass, facts were changed since, or a fact names only days that have gone.
// It returns what the pass suggests could be a persona of its own, or nil.
func (r *Runner) tidyMemory(ctx context.Context, client *llm.Client, model string, skill Skill, now time.Time, zone *time.Location) (*personaSuggestion, error) {
	ctx = llm.WithTrace(ctx, llm.Trace{Persona: skill.ID, Wake: "daily"})
	if !skill.Remembers() {
		return nil, nil
	}
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	today := localDay(now, zone)
	m := r.loadMemo(skill.ID, zone)
	passed := pastDated(m, today)
	if !tidyDue(m) && len(passed) == 0 {
		return nil, nil
	}
	suggest := !skill.IsPerson() && (m.SuggestedAt.IsZero() || now.Sub(m.SuggestedAt) >= suggestEvery)

	expired := map[string]bool{}
	var passedIDs []string
	for _, f := range passed {
		expired[f.ID] = true
		passedIDs = append(passedIDs, f.ID)
	}
	lines := []string{"いまの日時: " + now.In(zone).Format("2006-01-02 15:04 MST")}
	if m.TidyDirty {
		lines = append(lines, "", "（事実や記録が変更されています。いま渡した内容だけを根拠に整理し、削除・訂正前の内容を復活させないでください）")
	}
	lines = append(lines, "", "いまの事実:")
	if len(m.Facts) == 0 {
		lines = append(lines, "（まだありません）")
	}
	for _, f := range m.Facts {
		line := "- [" + f.ID + "] " + f.Text
		if !f.Since.IsZero() {
			line += "（" + f.Since.In(zone).Format("2006-01-02") + "〜）"
		}
		if expired[f.ID] {
			line += "（日付の審査対象）"
		}
		lines = append(lines, line)
	}
	records := map[string]record{}
	var recordIDs []string
	lines = append(lines, "", "かけらの記録（新しい順。（新規）は前回の整理のあとの記録）:")
	list := tidyRecords(m)
	if len(list) == 0 {
		lines = append(lines, "（ありません）")
	}
	for _, rec := range list {
		records[rec.ID] = rec
		recordIDs = append(recordIDs, rec.ID)
		line := rec.line(zone)
		if m.isNew(rec) {
			line += "（新規）"
		}
		lines = append(lines, line)
	}
	tool := tidyTool(recordIDs, passedIDs, suggest)
	// Facts are shown to the user, in the language they write in.
	messages := []llm.Message{{Role: "system", Content: tidyPrompt(skill, suggest) + factLanguageRule(r.backgroundLanguage(ctx))}, {Role: "user", Content: strings.Join(lines, "\n")}}

	type submission struct {
		Facts      []tidyOp           `json:"facts"`
		Drop       []string           `json:"drop_records"`
		Review     []string           `json:"review_records"`
		Invalidate []string           `json:"invalidate"`
		Retain     []string           `json:"retain"`
		Suggest    *personaSuggestion `json:"suggest_persona"`
	}
	var sub submission
	var next memo
	for attempt := 1; ; attempt++ {
		reply, err := client.Complete(held, llm.Request{Purpose: "memory_tidy", Attempt: attempt, Model: model, Messages: messages, Tools: []llm.Tool{tool}, ToolChoice: "required"})
		if err != nil {
			return nil, err
		}
		sub = submission{}
		var problems []string
		raw := ""
		for _, call := range reply.Calls {
			if call.Name == tool.Name {
				raw = call.Args
				if err := json.Unmarshal([]byte(call.Args), &sub); err != nil {
					problems = append(problems, "引数を JSON として読めません: "+err.Error())
				}
			}
		}
		if raw == "" {
			problems = append(problems, tool.Name+" で提出してください")
		}
		var ops []factOp
		for i, op := range sub.Facts {
			if op.Op == "add" && len(op.Records) == 0 {
				problems = append(problems, fmt.Sprintf("facts[%d]: add には根拠の記録の id を records に書いてください", i))
			}
			for _, id := range op.Records {
				rec, ok := records[id]
				if !ok {
					problems = append(problems, fmt.Sprintf("facts[%d]: 記録 %s は渡していません", i, id))
					continue
				}
				op.From = append(op.From, r.kakeraOf(rec))
			}
			ops = append(ops, op.factOp)
		}
		for _, id := range sub.Invalidate {
			if !contains(passedIDs, id) {
				problems = append(problems, id+" は日付の審査対象として渡していません")
				continue
			}
			ops = append(ops, factOp{Op: "invalidate", ID: id})
		}
		for _, id := range sub.Retain {
			if !contains(passedIDs, id) || contains(sub.Invalidate, id) {
				problems = append(problems, id+" は保持できる審査対象ではありません")
			}
		}
		var factProblems []string
		next, _, factProblems = planFacts(m, ops, "", now, zone)
		problems = append(problems, factProblems...)
		// A record goes only once a fact rests on what it was of.
		resting := map[string]bool{}
		for _, f := range next.Facts {
			for _, id := range f.From {
				resting[id] = true
			}
		}
		for _, id := range sub.Drop {
			rec, ok := records[id]
			switch {
			case !ok:
				problems = append(problems, "記録 "+id+" は渡していません")
			case !resting[r.kakeraOf(rec)]:
				problems = append(problems, "記録 "+id+" はどの事実の根拠（records）にもなっていません。事実に取り込むか、drop_records から外してください")
			}
		}
		if len(sub.Review) > maxReviewRecords {
			problems = append(problems, fmt.Sprintf("review_records は%d件までにしてください", maxReviewRecords))
		}
		for _, id := range sub.Review {
			if _, ok := records[id]; !ok || contains(sub.Drop, id) {
				problems = append(problems, "記録 "+id+" は見直しに回せません")
			}
		}
		if sub.Suggest != nil && (strings.TrimSpace(sub.Suggest.Topic) == "" || strings.TrimSpace(sub.Suggest.Reason) == "") {
			problems = append(problems, "suggest_persona は topic と reason を書くか、null にしてください")
		}
		if len(problems) == 0 {
			break
		}
		if attempt == maxKeeperAttempts {
			return nil, fmt.Errorf("memory tidy rejected %d times: %s", attempt, strings.Join(problems, "; "))
		}
		messages = append(messages[:2], llm.Message{Role: "user", Content: "前の提出は本体が差し戻しました。直して " + tool.Name + " でもう一度提出してください。\n- " + strings.Join(problems, "\n- ") + "\n前の提出:\n" + clip(raw, 12000)})
	}

	var kept []record
	for _, rec := range next.Records {
		if contains(sub.Drop, rec.ID) {
			continue
		}
		if contains(sub.Review, rec.ID) {
			rec.Review = recordDate{Day: today, Days: 7}
		}
		kept = append(kept, rec)
	}
	next.Records = kept
	for i := range next.Facts {
		if contains(sub.Retain, next.Facts[i].ID) {
			next.Facts[i].PastReviewed = next.Facts[i].reviewFingerprint(zone)
		}
	}
	next.TidyDirty = false
	next.CheckedAt = now
	var suggestion *personaSuggestion
	logged := ""
	if suggest && sub.Suggest != nil {
		suggestion = &personaSuggestion{Topic: strings.TrimSpace(sub.Suggest.Topic), Reason: strings.TrimSpace(sub.Suggest.Reason)}
		logged = suggestion.Topic + "（" + suggestion.Reason + "）"
		next.SuggestedAt = today
	}
	err = r.saveMemo(held, skill.ID, next, zone)
	logKeeper(keeperRecord{Persona: skill.ID, Model: model, Retired: sub.Invalidate, Dropped: sub.Drop, Reviewed: sub.Review, Suggested: logged, Error: errorText(err)})
	if err != nil {
		return nil, err
	}
	return suggestion, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
