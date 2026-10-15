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
const summaryInputLimit = 60000

// A summary is about what the user did and said. The personas' answers and
// the pages and pictures behind posts are clipped to what places them, which
// keeps a month's input - and the summary written from it - inside one call.
const (
	summaryRemarkRunes = 300
	summaryMediaRunes  = 600
)

// summaryOutputTokens is the summary's output allowance, stated rather than
// left to the provider's default, which cut summaries off.
const summaryOutputTokens = 16000

func clipRunes(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n]) + "…（省略）"
	}
	return text
}

// summaryEntry is an entry as the month's summary reads it.
func summaryEntry(entry timeline.Entry) timeline.Entry {
	if entry.Speaker != timeline.User {
		entry.Text = clipRunes(entry.Text, summaryRemarkRunes)
		entry.Sources = nil
	}
	if entry.Media != nil {
		media := *entry.Media
		media.Text = clipRunes(media.Text, summaryMediaRunes)
		entry.Media = &media
	}
	return entry
}

const summaryPrompt = `あなたは kakeratta の本体です。ユーザーのタイムライン（ユーザーの投稿と返信、担当たちの発言）のひと月分を、日ごとのまとめを中心に読み、「今月のまとめ」を書きます。ユーザーが後から読み返し、人物の担当たちが月に一度参照します。
- ユーザーの記録の言語で書きます。
- 主役はユーザーです。ユーザーがしたこと・言ったこと・決めたこと・気にしていること、好みや状況の変化を中心に書きます。担当の回答や調査結果そのものの要約にはしません。
- 担当の説明・助言・提案・一般知識は原則省きます。ユーザーが採用・実行したと明言していない提案を、ユーザーの予定や行動として書いてはいけません。質問しただけなら、その話題を相談したこととして短く残します。
- 担当ごとではなくユーザーの生活や関心の話題ごとに「### 見出し」で分け、各話題は原則1〜3項目、各項目は短い1行の箇条書きにします。
- 同じ話題の似た相談や反復した出来事は、日付順や質問ごとに列挙せず1項目に統合します。例えば金魚について何度も相談していれば、相談のたびの回答を並べず、ユーザーが何を気にし、何を行い、状況がどう変わったかをまとめます。変化がない反復は繰り返しません。
- 人（家族・友人・同僚など）との重要な出来事（会った、話した、贈った・もらった、約束した、貸し借り）、これからの予定、済んでいないことは残します。別の人物・約束・期限や重要な状況の変化を無理に1つに潰さず、必要な場合は項目を増やします。人の名前は記録に書かれたとおりに書きます。
- 各項目の末尾に、根拠にした投稿へのリンクを [→](/?entry=ID) の形で付けます。ID は記録の先頭の角括弧の中のもの、または日ごとのまとめの各項目末尾の [e_...] のものです。
- 記録は、本体が日ごとに書いた内部向けのまとめと、それに入っていない投稿です。内部向けのまとめにも担当の回答が含まれ得るため、ユーザー本人の発言・行動と区別し、ユーザーについての記録を選んで統合します。
- 記録にないことは書きません。推測、助言、感想、挨拶は書きません。
- まとめの本文だけを返します。`

// summarizeAt writes the month's summary when today is its day; dailyAt calls
// it at the account's minute, after the day's summary. It is claimed like a
// check-in, so it is written once.
func (r *Runner) summarizeAt(ctx context.Context, now time.Time, client *llm.Client, settings store.Settings) {
	loc := settings.Location()
	local := now.In(loc)
	if local.Day() != store.SummaryDay(r.store.ID()) {
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
	entries, err := store.SearchTimelineWindow(ctx, r.store, since, now)
	if err != nil {
		r.emit("error", map[string]any{"where": "summary", "message": err.Error()})
		return
	}
	var month []timeline.Entry
	for _, entry := range entries {
		if entry.Deleted || entry.Speaker == timeline.App || !entry.At.After(since) || entry.At.After(now) {
			continue
		}
		month = append(month, summaryEntry(entry))
	}
	if len(month) == 0 {
		return
	}
	// The month is read from the day's summaries, with only the posts none of
	// them holds read in full.
	record := r.monthFromDays(ctx, month, since, now, loc)
	partial := false
	if runes := []rune(record); len(runes) > summaryInputLimit {
		record, partial = string(runes[len(runes)-summaryInputLimit:]), true
	}
	user := "期間: " + since.In(loc).Format("2006-01-02") + " 〜 " + local.Format("2006-01-02") + "\n"
	if partial {
		user += "記録が多いため、期間の後半だけを渡します。まとめの最初に、前半を含まないことを一言書いてください。\n"
	}
	user += "\n記録（古い順）:\n" + record
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	reply, err := client.Complete(ctx, llm.Request{Purpose: "monthly_summary", Model: personaModel(settings), Temperature: 0.3, MaxTokens: summaryOutputTokens, Messages: []llm.Message{
		{Role: "system", Content: summaryPrompt},
		{Role: "user", Content: user},
	}})
	text := strings.TrimSpace(reply.Content)
	if err == nil && text != "" && reply.FinishReason == "length" {
		// Kept rather than thrown away, but the reader is told it stops short.
		text += "\n\n（まとめが長くなりすぎたため、ここで途切れています）"
	}
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
func (r *Runner) monthlyCheckIn(ctx context.Context, skill Skill, since, now time.Time, loc *time.Location) string {
	entries, err := store.SearchTimelineWindow(ctx, r.store, since, now)
	if err != nil {
		return ""
	}
	var summary *timeline.Entry
	var heard []timeline.Entry
	people := People(r.skills(ctx).Active(r.store.Settings().Active))
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
		if len(HeardTerms(skill, search.Text(entry), people...)) > 0 {
			heard = append(heard, entry)
		}
	}
	sort.Slice(heard, func(i, j int) bool { return heard[i].At.Before(heard[j].At) })
	if len(heard) > monthlyHeardLimit {
		heard = heard[len(heard)-monthlyHeardLimit:]
	}
	names := r.SpeakerNames(ctx)
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
