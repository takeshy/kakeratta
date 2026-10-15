package persona

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The day's summary.
//
// Every persona's daily check-in looks at what happened since its previous
// one, including topics it was not part of. Rather than each persona reading
// the same posts, the app summarises them once, at the account's minute, and
// every persona then checks in reading that summary (daily.go). It is
// internal: never posted, kept beside the monthly summary's claim under
// store.SummaryID, and pruned after a month: the month's summary is written
// from them (summary.go).
//
// A summary covers the span since the previous day's. Deleting or editing a
// post drops the summaries whose span holds it; a check-in is then handed
// the posts they covered as they are, as it is when a summary fails.

const dailySummaryDir = "daily/"

// dailySummaryOutputTokens bounds one summary.
const dailySummaryOutputTokens = 4000

const dailySummaryPrompt = `あなたは kakeratta の本体です。前回のまとめ以降のタイムライン（ユーザーの投稿と返信、担当たちの発言）を読み、担当たちが1日1回の定期チェックインで読む内部向けのまとめを書きます。ユーザーには表示されません。
- ユーザーの記録の言語で書きます。
- 話題ごとに短い箇条書きにし、各項目の末尾に根拠にした投稿IDを [e_...] の形で付けます。IDは記録の先頭の角括弧の中のものです。担当はこのIDで元の投稿を読み直せます。
- ユーザーが言ったこと・したこと、人との出来事、これからの予定と日付、済んでいないこと、好みや状況の変化を中心に残します。担当の説明・一般知識・助言の再録は避け、会話の理解に必要な回答だけをユーザーの事実と区別して短く書きます。ユーザーが採用・実行したと明言していない提案は、ユーザーの予定や行動として書いてはいけません。
- 同じ話題の似た相談や反復した出来事は1項目にまとめ、重要な日付・約束・状況の変化と根拠の投稿IDを残します。
- 記録にないことは書きません。推測、助言、感想、挨拶は書きません。
- まとめの本文だけを返します。`

var dailySummaryHead = regexp.MustCompile(`^<!-- kakeratta:daily from=(\S+) to=(\S+) -->\n`)

type dailySummary struct {
	path     string
	from, to time.Time
	text     string
}

func (s dailySummary) covers(at time.Time) bool { return at.After(s.from) && !at.After(s.to) }

// dailySummaries are the summaries kept, oldest first.
func (r *Runner) dailySummaries() []dailySummary {
	paths, _ := r.store.NotePaths(store.SummaryID)
	var out []dailySummary
	for _, path := range paths {
		if !strings.HasPrefix(path, dailySummaryDir) {
			continue
		}
		note, ok := r.store.Note(store.SummaryID, path)
		if !ok {
			continue
		}
		match := dailySummaryHead.FindStringSubmatch(note.Text)
		if match == nil {
			continue
		}
		from, err1 := time.Parse(time.RFC3339Nano, match[1])
		to, err2 := time.Parse(time.RFC3339Nano, match[2])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, dailySummary{path: path, from: from, to: to, text: strings.TrimSpace(note.Text[len(match[0]):])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].to.Before(out[j].to) })
	return out
}

// checkInDigest is what a daily check-in is handed of the timeline since its
// previous run: the day's summaries covering it, and in full only the posts
// none of them holds. It also says how many posts since then are the user's.
func (r *Runner) checkInDigest(ctx context.Context, since, now time.Time, loc *time.Location) (string, int) {
	entries, err := store.SearchTimelineWindow(ctx, r.store, since, now)
	if err != nil {
		// Unread is not "nothing new": the persona reads the span itself.
		return "\n\nRecords since the previous check-in could not be read; use timeline_search with that span.", 1
	}
	summaries := r.dailySummaries()
	fresh := 0
	var unsummarised []timeline.Entry
	for _, entry := range liveSpan(entries, since, now) {
		if entry.Speaker == timeline.User {
			fresh++
		}
		covered := false
		for _, summary := range summaries {
			if summary.covers(entry.At) {
				covered = true
			}
		}
		if !covered {
			unsummarised = append(unsummarised, summaryEntry(entry))
		}
	}
	var handed []string
	for _, summary := range summaries {
		if summary.to.After(since) && !summary.from.After(now) {
			handed = append(handed, summary.text)
		}
	}
	var b strings.Builder
	if len(handed) > 0 {
		b.WriteString("\n\nSummary of the timeline since the previous check-in, written by the app (posts in [ ] can be read with entries):\n" + strings.Join(handed, "\n\n"))
	}
	switch {
	case len(unsummarised) > 0:
		head := "\n\nRecords since the previous check-in not in the summary, oldest first:\n"
		if len(unsummarised) > checkInWindowLimit {
			head = fmt.Sprintf("\n\nRecords since the previous check-in not in the summary, oldest first (only the newest %d of %d; search for earlier ones):\n", checkInWindowLimit, len(unsummarised))
			unsummarised = unsummarised[len(unsummarised)-checkInWindowLimit:]
		}
		b.WriteString(head + strings.TrimSpace(timeline.Render(unsummarised, r.personaNames(ctx), loc)))
	case len(handed) == 0:
		b.WriteString("\n\nRecords since the previous check-in: none.")
	}
	return b.String(), fresh
}

// liveSpan is the entries in (since, now] a summary or a check-in reads,
// oldest first: not deleted, and not the app's own posts.
func liveSpan(entries []timeline.Entry, since, now time.Time) []timeline.Entry {
	var out []timeline.Entry
	for _, entry := range entries {
		if !entry.Deleted && entry.At.After(since) && !entry.At.After(now) && entry.Speaker != timeline.App {
			out = append(out, entry)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// summarizeDay writes the day's summary of (since, now], once the account's
// day is claimed. A span without a user post needs none. A summary that fails
// or is cut off is not kept; the check-ins are handed the posts instead.
func (r *Runner) summarizeDay(ctx context.Context, client *llm.Client, settings store.Settings, since, now time.Time, loc *time.Location) {
	entries, err := store.SearchTimelineWindow(ctx, r.store, since, now)
	if err != nil {
		r.emit("error", map[string]any{"where": "daily summary", "message": err.Error()})
		return
	}
	span := liveSpan(entries, since, now)
	users := 0
	clipped := make([]timeline.Entry, 0, len(span))
	for _, entry := range span {
		if entry.Speaker == timeline.User {
			users++
		}
		clipped = append(clipped, summaryEntry(entry))
	}
	if users == 0 {
		return
	}
	record := timeline.Render(clipped, r.personaNames(ctx), loc)
	if runes := []rune(record); len(runes) > summaryInputLimit {
		record = string(runes[len(runes)-summaryInputLimit:])
	}
	reply, err := client.Complete(ctx, llm.Request{Purpose: "daily_summary", Model: personaModel(settings), Temperature: 0.3, MaxTokens: dailySummaryOutputTokens, Messages: []llm.Message{
		{Role: "system", Content: dailySummaryPrompt},
		{Role: "user", Content: "記録（古い順）:\n" + record},
	}})
	text := strings.TrimSpace(reply.Content)
	if err != nil || text == "" || reply.FinishReason == "length" {
		message := "empty or cut-off daily summary"
		if err != nil {
			message = err.Error()
		}
		r.emit("error", map[string]any{"where": "daily summary", "message": message})
		return
	}
	path := dailySummaryDir + now.UTC().Format("20060102T150405.000000000Z") + ".md"
	head := fmt.Sprintf("<!-- kakeratta:daily from=%s to=%s -->\n", since.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	if err := r.store.WriteNote(store.SummaryID, path, head+text); err != nil {
		r.emit("error", map[string]any{"where": "daily summary", "message": err.Error()})
		return
	}
	for _, old := range r.dailySummaries() {
		// The month's summary is written from these, so a month is kept.
		if now.Sub(old.to) > summaryLookback+24*time.Hour {
			_ = r.store.DeleteNote(store.SummaryID, old.path)
		}
	}
}

// forgetDailySummaries drops the summaries whose span holds a post that was
// deleted or edited, so nothing of its old text is handed on. The posts they
// covered are handed to the check-ins as they are instead.
func (r *Runner) forgetDailySummaries(at ...time.Time) {
	for _, summary := range r.dailySummaries() {
		for _, t := range at {
			if summary.covers(t) {
				if err := r.store.DeleteNote(store.SummaryID, summary.path); err != nil {
					r.emit("error", map[string]any{"where": "daily summary", "message": err.Error()})
				}
				break
			}
		}
	}
}

func (r *Runner) personaNames(ctx context.Context) map[string]string {
	return r.SpeakerNames(ctx)
}

// monthFromDays is the month as the month's summary reads it: the day's
// summaries within it, then the posts no day's summary holds - written before
// the first one, after the last, or in the span of one dropped by a deletion.
func (r *Runner) monthFromDays(ctx context.Context, month []timeline.Entry, since, now time.Time, loc *time.Location) string {
	var days []dailySummary
	for _, summary := range r.dailySummaries() {
		if summary.to.After(since) && !summary.from.After(now) {
			days = append(days, summary)
		}
	}
	var rest []timeline.Entry
	for _, entry := range month {
		covered := false
		for _, day := range days {
			if day.covers(entry.At) {
				covered = true
			}
		}
		if !covered {
			rest = append(rest, entry)
		}
	}
	var b strings.Builder
	for _, day := range days {
		b.WriteString(fmt.Sprintf("## %s 〜 %s の日ごとのまとめ\n%s\n\n", day.from.In(loc).Format("2006-01-02 15:04"), day.to.In(loc).Format("2006-01-02 15:04"), day.text))
	}
	if len(rest) > 0 {
		b.WriteString("## 日ごとのまとめに入っていない記録（古い順）\n" + timeline.Render(rest, r.personaNames(ctx), loc))
	}
	return strings.TrimSpace(b.String())
}
