package persona

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The month's summary.
//
// Once a month the app reads the timeline since the last summary and posts
// what happened, topic by topic, each line linked to the post it came from.
// The user reads it as a look back and can set it right by editing it; a
// person's persona heard from monthly reads it instead of the whole month, so
// many people cost one reading of the timeline rather than one each.

// summaryLookback is the most a summary reads: a month and a day, so a
// summary after a missed month does not try to read everything.
const summaryLookback = 32 * 24 * time.Hour

// summaryInputLimit bounds the month handed to the model. A busy month keeps
// its newest part; the summary says it is partial rather than the call failing.
const summaryInputLimit = 120000

const summaryPrompt = `あなたは kakeratta の本体です。ユーザーのタイムライン（ユーザーの投稿と返信、担当たちの発言）のひと月分を読み、「今月のまとめ」を書きます。ユーザーが後から読み返し、人物の担当たちが月に一度参照します。
- ユーザーの記録の言語で書きます。
- 話題ごとに「### 見出し」で分け、各項目は1行の箇条書きにします。
- 人（家族・友人・同僚など）ごとの出来事（会った、話した、贈った・もらった、約束した、貸し借り）、これからの予定、済んでいないことは必ず残します。人の名前は記録に書かれたとおりに書きます。
- 各項目の末尾に、根拠にした投稿へのリンクを [→](/?entry=ID) の形で付けます。ID は記録の先頭の角括弧の中のものです。
- 記録にないことは書きません。推測、助言、感想、挨拶は書きません。
- まとめの本文だけを返します。`

// summarizeAt writes the month's summary when today is its day and its
// minute has come. It is claimed like a check-in, so it is written once.
func (r *Runner) summarizeAt(now time.Time, client *llm.Client, settings store.Settings) {
	loc := settings.Location()
	local := now.In(loc)
	root := r.store.Root()
	if local.Day() != store.SummaryDay(root) || local.Before(store.CheckInAt(root, store.SummaryID, local)) || !local.Before(store.CheckInCloses(local)) {
		return
	}
	claimed, previous, err := r.store.ClaimDailyCheckInAt(store.SummaryID, local.Format("2006-01-02"), now)
	if err != nil {
		r.emit("error", map[string]any{"where": "summary", "message": err.Error()})
		return
	}
	if !claimed {
		return
	}
	since := previous
	if since.IsZero() || now.Sub(since) > summaryLookback {
		since = now.Add(-summaryLookback)
	}
	entries, err := store.SearchTimelineWindow(context.Background(), r.store, since, now)
	if err != nil {
		r.emit("error", map[string]any{"where": "summary", "message": err.Error()})
		return
	}
	var month []timeline.Entry
	for _, entry := range entries {
		if entry.Deleted || entry.Speaker == timeline.App || !entry.At.After(since) || entry.At.After(now) {
			continue
		}
		month = append(month, entry)
	}
	if len(month) == 0 {
		return
	}
	names := map[string]string{}
	for _, skill := range r.catalogue.All() {
		names[skill.ID] = skill.Name
	}
	record := timeline.Render(month, names, loc)
	partial := false
	if runes := []rune(record); len(runes) > summaryInputLimit {
		record, partial = string(runes[len(runes)-summaryInputLimit:]), true
	}
	user := "期間: " + since.In(loc).Format("2006-01-02") + " 〜 " + local.Format("2006-01-02") + "\n"
	if partial {
		user += "記録が多いため、期間の後半だけを渡します。まとめの最初に、前半を含まないことを一言書いてください。\n"
	}
	user += "\n記録（古い順）:\n" + record
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	text, err := client.Chat(ctx, llm.Request{Model: personaModel(settings), Temperature: 0.3, Messages: []llm.Message{
		{Role: "system", Content: summaryPrompt},
		{Role: "user", Content: user},
	}})
	text = strings.TrimSpace(text)
	if err != nil || text == "" {
		message := "empty summary"
		if err != nil {
			message = err.Error()
		}
		r.emit("error", map[string]any{"where": "summary", "message": message})
		return
	}
	post := timeline.Entry{ID: store.NewID("e"), Kind: timeline.KindPost, Speaker: timeline.App, Text: text, At: now, Summary: timeline.SummaryMonth}
	post.Root = post.ID
	if err := r.store.Append(post); err != nil {
		r.emit("error", map[string]any{"where": "summary", "message": err.Error()})
		return
	}
	r.emit("entry", post)
}

// monthlyCheckIn is what a person heard from monthly is woken with: the
// month's summary, as the user may have set it right, and the posts since its
// previous check-in that name it, which a summary may have left out.
func (r *Runner) monthlyCheckIn(skill Skill, since, now time.Time, loc *time.Location) string {
	entries, err := store.SearchTimelineWindow(context.Background(), r.store, since, now)
	if err != nil {
		return ""
	}
	var summary *timeline.Entry
	var heard []timeline.Entry
	for i := range entries {
		entry := entries[i]
		if entry.Deleted || !entry.At.After(since) || entry.At.After(now) {
			continue
		}
		if entry.Speaker == timeline.App && entry.Summary == timeline.SummaryMonth {
			if summary == nil || entry.At.After(summary.At) {
				summary = &entry
			}
			continue
		}
		if len(HeardTerms(skill, search.Text(entry))) > 0 {
			heard = append(heard, entry)
		}
	}
	sort.Slice(heard, func(i, j int) bool { return heard[i].At.Before(heard[j].At) })
	if len(heard) > monthlyHeardLimit {
		heard = heard[len(heard)-monthlyHeardLimit:]
	}
	names := map[string]string{}
	for _, other := range r.catalogue.All() {
		names[other.ID] = other.Name
	}
	var b strings.Builder
	if summary != nil {
		b.WriteString("\n\nThis month's summary, written by the app (the user may have edited it):\n[" + summary.ID + "]\n" + summary.Text)
	} else {
		b.WriteString("\n\nNo summary was written this month.")
	}
	if len(heard) > 0 {
		b.WriteString("\n\nPosts since the previous check-in that mention " + skill.Name + ", oldest first:\n" + timeline.Render(heard, names, loc))
	} else {
		b.WriteString("\n\nNo post since the previous check-in mentions " + skill.Name + ".")
	}
	return b.String()
}

// monthlyHeardLimit bounds the posts naming a person handed to its monthly
// check-in; the newest are kept.
const monthlyHeardLimit = 40
