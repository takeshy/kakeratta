package persona

import (
	"context"
	"encoding/json"
	"fmt"
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
	for _, field := range m.Fields {
		ff := m.ByField[field]
		added, gone := changedSince(ff)
		if len(added) == 0 && len(gone) == 0 {
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

func summaryTool(fields []string) llm.Tool {
	return llm.Tool{Name: "write_summaries", Description: "分野ごとのまとめ文を本体に渡す。本体が検証して保存する。",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"summaries": map[string]any{"type": "array", "maxItems": len(fields), "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "enum": fields},
					"text":  stringField(fmt.Sprintf("その分野のまとめ文の新しい全文（%d字以内）", maxFieldRunes)),
				},
				"required": []string{"field", "text"}, "additionalProperties": false,
			}},
		}, "required": []string{"summaries"}, "additionalProperties": false}}
}

func fieldSummaryPrompt(skill Skill) string {
	s := fmt.Sprintf("%s%s\n担当領域: %s\n担当の指示: %s\n", skill.intro(), skill.Tone, skill.Domain, skill.Instructions)
	s += "いまはこの担当のメモ係として、1日1回、分野ごとのまとめ文を書き直す。\n"
	if memory := strings.TrimSpace(skill.forTurn().Memory); memory != "" {
		s += "分野の分け方と残し方: " + memory + "\n"
	}
	s += `write_summaries で、渡された分野それぞれのまとめ文の新しい全文を出す。
- まとめ文は、その分野の事実を読んだあなたの見立て。傾向、移り変わり、まだ済んでいないことが分かるように書く（` + fmt.Sprint(maxFieldRunes) + `字以内）。
- いまのまとめ文を土台に、前回から増えた・変わった・当てはまらなくなった事実を取り込む。変わらない部分は残す。
- 事実にないことを足さない。推測は推測と明記する。事実の id は書かない。
- 事実と同じ言語で書く。
`
	return s
}

// rewriteSummaries rewrites the summaries of a persona's fields that are due,
// under its keeper lock, from what changed among their facts. A field the
// model leaves out is still marked as read today, so it is not asked again
// until something more changes.
func (r *Runner) rewriteSummaries(ctx context.Context, client *llm.Client, model string, skill Skill, now time.Time, zone *time.Location) error {
	held, release, err := r.lockKeeper(ctx, skill.ID)
	if err != nil {
		return err
	}
	defer release()
	today := localDay(now, zone)
	m := r.loadFacts(skill, zone)
	due, _ := staleFields(m, today)
	if len(due) == 0 {
		return nil
	}
	var parts []string
	for _, field := range due {
		ff := m.ByField[field]
		added, gone := changedSince(ff)
		lines := []string{"## " + field, "いまのまとめ文:"}
		if ff.Summary == "" {
			lines = append(lines, "（まだありません）")
		} else {
			lines = append(lines, ff.Summary)
		}
		lines = append(lines, "", "前回のまとめ以降に増えた・変わった事実:")
		for _, f := range added {
			lines = append(lines, "- "+f.Text)
		}
		if len(added) == 0 {
			lines = append(lines, "（なし）")
		}
		lines = append(lines, "", "前回のまとめ以降に当てはまらなくなった事実:")
		for _, f := range gone {
			line := "- " + f.Text
			if f.By != "" {
				line += "（新しい事実に置き換わった）"
			}
			lines = append(lines, line)
		}
		if len(gone) == 0 {
			lines = append(lines, "（なし）")
		}
		lines = append(lines, "", "いまの事実（すべて）:")
		for _, f := range ff.Current {
			lines = append(lines, "- "+f.Text)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	user := "いまの日時: " + now.In(zone).Format("2006-01-02 15:04 MST") + "\n\n" + strings.Join(parts, "\n\n")
	tool := summaryTool(due)
	messages := []llm.Message{{Role: "system", Content: fieldSummaryPrompt(skill)}, {Role: "user", Content: user}}
	var written []summaryWrite
	for attempt := 1; ; attempt++ {
		reply, err := client.Complete(held, llm.Request{Model: model, Messages: messages, Tools: []llm.Tool{tool}, ToolChoice: "required"})
		if err != nil {
			return err
		}
		var submitted struct {
			Summaries []summaryWrite `json:"summaries"`
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
			if _, ok := fieldNamed(due, write.Field); !ok {
				problems = append(problems, "分野「"+write.Field+"」は渡していません")
			}
			if n := len([]rune(text)); n == 0 || n > maxFieldRunes {
				problems = append(problems, fmt.Sprintf("分野「%s」のまとめ文が%d字です。1〜%d字にしてください", write.Field, n, maxFieldRunes))
			}
		}
		if len(problems) == 0 {
			written = submitted.Summaries
			break
		}
		if attempt == maxKeeperAttempts {
			return fmt.Errorf("summaries rejected %d times: %s", attempt, strings.Join(problems, "; "))
		}
		messages = append(messages[:2], llm.Message{Role: "user", Content: "前の提出は本体が差し戻しました。直して " + tool.Name + " でもう一度提出してください。\n- " + strings.Join(problems, "\n- ") + "\n前の提出:\n" + clip(raw, 12000)})
	}
	for _, write := range written {
		field, _ := fieldNamed(due, write.Field)
		ff := m.ByField[field]
		ff.Summary = strings.TrimSpace(repairNewlines(write.Text))
		m.ByField[field] = ff
	}
	for _, field := range due {
		ff := m.ByField[field]
		ff.SummaryAt = today
		if ff.Summary == "" {
			// Nothing to say over facts yet is still a reading of them.
			continue
		}
		m.ByField[field] = ff
	}
	_, err = r.writeFacts(held, skill.ID, m, due, zone)
	logKeeper(keeperRecord{Persona: skill.ID, Model: model, Summaries: due, Error: errorText(err)})
	return err
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
