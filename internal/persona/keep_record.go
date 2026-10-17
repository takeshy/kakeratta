package persona

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
)

// A turn keeps one record of the user's kakera and nothing more: a sentence,
// tags to find it by, and its dates, appended to the persona's memory file.
// It reads none of the facts. Folding records into facts
// wait for the daily pass (memory_tidy.go), which reads only what changed.

// maxTagsShown bounds the tags in use a turn is shown, most used first.
const maxTagsShown = 60

// tagsInUse are the tags the persona's records carry, most used first, so a
// turn reuses their spelling.
func (r *Runner) tagsInUse(personaID string, zone *time.Location) []string {
	count := map[string]int{}
	for _, rec := range r.records(personaID, zone) {
		for _, tag := range rec.Tags {
			count[tag]++
		}
	}
	tags := make([]string, 0, len(count))
	for tag := range count {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		if count[tags[i]] != count[tags[j]] {
			return count[tags[i]] > count[tags[j]]
		}
		return tags[i] < tags[j]
	})
	if len(tags) > maxTagsShown {
		tags = tags[:maxTagsShown]
	}
	return tags
}

func recordTool() llm.Tool {
	return llm.Tool{Name: "keep_record", Description: "今回のかけらの記録を本体に渡す。本体が検証して保存する。", Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"remember": map[string]any{"type": "boolean", "description": "残す内容がない挨拶・相づち・一般知識だけの発言なら false"},
			"summary":  stringField(fmt.Sprintf("今回のユーザーのかけらを1文で（%d字以内）。remember が false なら空文字", maxSummaryRunes)),
			"tags":     map[string]any{"type": "array", "maxItems": maxTags, "items": map[string]any{"type": "string"}, "description": "あとで探す手がかりの語を1〜5つ。remember が false なら []"},
			"expires":  dateField("この記録を本体が消す日"),
			"review":   dateField("この記録をユーザーに見直してもらう日"),
		},
		"required": []string{"remember", "summary", "tags", "expires", "review"}, "additionalProperties": false,
	}}
}

// keepRecord keeps the turn's record, in one call. A kakera with nothing
// to look back on is not kept.
func (r *Runner) keepRecord(ctx context.Context, client *llm.Client, model string, k keeperTurn) (keptMemory, int, []string, error) {
	system := k.skill.intro() + "\nいまは記憶担当。今回のユーザーのかけらを記録に残すかを決め、残すなら1文と検索用のタグと期限を付ける。\n"
	if memory := strings.TrimSpace(k.skill.forTurn().Memory); memory != "" {
		system += "残し方: " + memory + "\n"
	}
	system += "相談・出来事・好み・予定・訂正など、あとで振り返る内容があれば remember を true にする。挨拶・相づち・一般知識だけなら false。\n" +
		"summary はユーザーが言ったこと・したことを、ユーザーの言葉に沿って書く。AIの回答・助言や未採用の提案は書かない。事実への整理は本体が1日1回まとめて行うので、ここではしない。\n" +
		"tags は料理の種類・場所・主題・人など。下の「使っているタグ」に同じものがあれば同じ表記を使う。\n" + keeperDates
	user := "いまの日時: " + time.Now().In(k.here).Format(time.RFC3339)
	if tags := r.tagsInUse(k.skill.ID, k.here); len(tags) > 0 {
		user += "\n使っているタグ: " + strings.Join(tags, " ")
	}
	user += "\n\n" + threadContext(k.thread, k.trigger, k.names, k.here)
	kept, attempts, err := submitMemory(ctx, client, model, system, user, recordTool(), func(kept keptMemory) []string {
		if kept.Remember != nil && !*kept.Remember {
			return nil
		}
		problems := checkKept(kept, true, nil, k.answer, time.Now(), k.here)
		if copiesAnswer(k.answer, kept.Summary) {
			problems = append(problems, "summary が Answer の文章をそのまま写しています。ユーザーが言ったこと・聞いたことを書いてください")
		}
		return problems
	})
	if err != nil || (kept.Remember != nil && !*kept.Remember) {
		return kept, attempts, nil, err
	}
	id := k.replyID
	if id == "" {
		id = k.trigger.ID
	}
	rec := record{At: k.trigger.At, ID: id, Of: k.trigger.ID, Summary: cleanSummary(kept.Summary),
		Expires: dateFrom(kept.Expires, k.trigger.At, k.here), Review: dateFrom(kept.Review, k.trigger.At, k.here)}
	for _, tag := range kept.Tags {
		if tag = cleanTag(tag); tag != "" {
			rec.Tags = append(rec.Tags, tag)
		}
	}
	if err := r.appendRecord(ctx, k.skill.ID, rec, k.trigger, k.here); err != nil {
		return kept, attempts, nil, err
	}
	return kept, attempts, []string{MemoPath}, nil
}
