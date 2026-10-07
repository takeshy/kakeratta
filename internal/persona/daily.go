package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// dailyHeader opens daily check-in instructions.
const dailyHeader = `

## Daily check-in（1日1回の定期起床）
今回はユーザーの投稿ではなく、システムによる定期起床です。返答の義務はありません。
`

// dailyShareInstructions is the check-in that also looks for something worth
// bringing back from the last day and the persona's older notes.
const dailyShareInstructions = dailyHeader + `前回のチェックインから今回までの出来事は、本体が書いたまとめ（Summary of the timeline since the previous check-in）と、まだまとめに入っていない記録（Records since the previous check-in）として対象発言に入れてあります。timeline_search で期間全体を読み直す必要はありません。細部を確かめたいときだけ、まとめにある投稿IDを read の entries で読むか、語句や短い期間を指定して検索してください。特に、ユーザーや他の担当が発言したスレッドのうち、自分が返答していないものから、自分の担当分野と関係する変化やつながりを探します。
次に、前回のチェックインからの投稿に自分の分野に関係する変化があり、過去の記録と照合する具体的な候補がある場合だけ、最初に渡した最近の記録によく現れるタグで read の records を使い（args に {"tag":"…","before":"…"}）、同じタグを持つ前の記録がないか確認してください。候補があれば、前回のチェックインからのかけらと今も具体的な関係があるか確かめます。古いという理由だけでは話しません。関係する変化・近い予定・見直し対象がなければ、古い記録を順番に探し続けず調査を終えます。読み取りに失敗したことを「変化がなかった」とは扱いません。
最近の投稿と自分の記憶から、これからの予定や準備が必要な場面も確認してください。schedule を使える担当は schedules_list で他の担当の予定も確認し、同じ通知を重ねません。必要な場面だけ担当分野の外部情報を調べ、予定・習慣・過去の経験と照合して、今声をかける理由があるか判断します。まだ声をかける時刻でなければ、根拠のある時刻に schedule を置き、今は黙ります。
前回のチェックインからの未参加の話題から役立つ関係を発見した場合や、最近のかけらと前の記録の間に今伝える価値のあるつながり、またはこれからの予定に向けて今役立つ準備を発見した場合だけ、短く話してください。
話す量や口調は、自分の担当のキャラクターに合わせてください。
毎日の件数合わせ、一般的なニュース一覧、同じ助言の繰り返し、挨拶だけの投稿は不要です。
伝えることがない場合や根拠を得られない場合は Answer の text を空にします。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// monthlyHeader opens the check-in of a person heard from once a month.
const monthlyHeader = `

## Monthly check-in（月1回の定期起床）
今回はユーザーの投稿ではなく、システムによる月1回の定期起床です。返答の義務はありません。
この月のタイムラインは本体がまとめを書いています。対象発言に、そのまとめと、前回のチェックインから自分の名前やキーワードが出た投稿があります。timeline_search で期間全体を読み直す必要はありません。細部を確かめたいときだけ、語句や短い期間を指定して検索してください。
`

// monthlyShareInstructions is a monthly check-in that may also bring back
// something worth saying.
const monthlyShareInstructions = monthlyHeader + `まず、まとめと投稿のうち自分の担当する人に関わることを読んでください。誕生日や記念日、会う予定が近ければ予定を置きます。
次に自分の記憶を見直し、済んでいない約束や貸し借り、しばらく連絡していないことなど、今伝える価値があることがあれば短く話してください。
話す量や口調は、自分の担当のキャラクターに合わせてください。
挨拶だけ、まとめの繰り返し、一般的な助言は不要です。伝えることがなければ Answer の text を空にします。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// checkInLookback is the furthest back a sharing check-in reads: a persona
// back after weeks away catches up on a week, not on everything.
const checkInLookback = 7 * 24 * time.Hour

// dailyAt is the account's day, once a day at the minute of the user's
// 07:00-21:00 the app picks for the account (store.DailyID; see
// store.CheckInAt). The app first summarises the timeline since the previous
// day (daily_summary.go) and, on its day, writes the month's summary; then
// every active persona checks in, one after another, reading the same summary.
// A check-in follows the persona's character and may bring back something
// worth saying, including a record due for review.
func (r *Runner) dailyAt(ctx context.Context, now time.Time) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "daily", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" || personaModel(settings) == "" {
		return
	}
	loc := settings.Location()
	local := now.In(loc)
	root := r.store.Root()
	day := local.Format("2006-01-02")
	summaryDay := local.Day() == store.SummaryDay(root)
	// At the time the user set, or the app's own minute (store.DailyAt).
	at, closes, ok := settings.DailyAt(root, local)
	if !ok || local.Before(at) || !local.Before(closes) {
		return
	}
	claimed, previous, err := r.store.ClaimDailyCheckInAt(store.DailyID, day, now)
	if err != nil {
		r.emit("error", map[string]any{"where": "daily", "message": err.Error()})
		return
	}
	if claimed {
		r.summarizeDay(ctx, client, settings, checkInSince(previous, now, checkInLookback, 24*time.Hour), now, loc)
	}
	// The month's summary comes next, read from the day's: a person checking
	// in monthly reads it. Its own claim writes it once.
	if summaryDay {
		r.summarizeAt(ctx, now, client, settings)
	}
	// Each persona still claims its own check-in, so one interrupted part way
	// through the list is taken up again, and none runs twice.
	for _, skill := range r.skills(ctx).Active(settings.Active) {
		if !skill.Remembers() {
			continue
		}
		monthly := settings.CheckInFor(skill.ID).Every == store.CheckInMonthly && skill.IsPerson()
		if monthly && !summaryDay {
			continue
		}
		claimed, previous, err := r.store.ClaimDailyCheckInAt(skill.ID, day, now)
		if err != nil {
			r.emit("error", map[string]any{"where": "daily", "message": err.Error()})
			continue
		}
		if !claimed {
			continue
		}
		// The summaries are rewritten at the check-in, before it reads them.
		if err := r.rewriteSummaries(ctx, client, personaModel(settings), skill.forTurn(), now, loc); err != nil {
			r.emit("error", map[string]any{"where": "summaries", "persona": skill.ID, "message": err.Error()})
		}
		// What happened since the previous check-in actually ran, read as one
		// span. With no previous run on record, or one long ago, it reads a
		// day, or a week at most - or, heard from monthly, the month.
		since := checkInSince(previous, now, checkInLookback, 24*time.Hour)
		if monthly {
			since = checkInSince(previous, now, summaryLookback, summaryLookback)
		}
		text := "Daily check-in: " + local.Format(time.RFC3339) + " (" + loc.String() + "). Decide whether there is anything worth sharing today." +
			"\nSince the previous check-in: from \"" + since.In(loc).Format("2006-01-02 15:04") + "\" to \"" + local.Format("2006-01-02 15:04") + "\""
		instructions := dailyShareInstructions
		if monthly {
			text = "Monthly check-in: " + local.Format(time.RFC3339) + " (" + loc.String() + ")." +
				"\nSince the previous check-in: from \"" + since.In(loc).Format("2006-01-02 15:04") + "\" to \"" + local.Format("2006-01-02 15:04") + "\"" +
				r.monthlyCheckIn(ctx, skill, since, now, loc)
			instructions = monthlyShareInstructions
		}
		due := r.reviewsDue(skill.ID, now, loc)
		if !monthly {
			// The day's summary is handed over, so the persona does not spend
			// a round reading the span; with nothing new from the user, no
			// record due and no dated plan coming up, the check-in costs no call.
			window, fresh := r.checkInDigest(ctx, since, now, loc)
			if !previous.IsZero() && fresh == 0 && len(due) == 0 && !upcomingDated(r.loadFacts(skill.forTurn(), loc), localDay(now, loc), checkInHorizon) {
				logCheckInSkipped(skill.ID, since, now)
				continue
			}
			text += window
		}
		// Reviews ride inside the check-in, so the persona posts once.
		if len(due) > 0 {
			text += "\n\nReview due, oldest first:\n" + reviewList(due, loc)
			instructions += reviewInstructions
		}
		trigger := timeline.Entry{At: local, Speaker: "system", Text: text}
		turn, cancel := context.WithTimeout(ctx, 10*time.Minute)
		r.run(turn, skill, []timeline.Entry{trigger}, trigger, false, false, instructions, false, client, settings, r.registry(settings))
		cancel()
	}
}

// checkInSince is where a span read since the previous run starts: there, or
// first back when there was none, and never further back than lookback.
func checkInSince(previous, now time.Time, lookback, first time.Duration) time.Time {
	switch {
	case previous.IsZero():
		return now.Add(-first)
	case now.Sub(previous) > lookback:
		return now.Add(-lookback)
	}
	return previous
}

// checkInWindowLimit is how many posts no day's summary holds - after a
// failed summary or a deletion - a daily check-in is handed in full.
const checkInWindowLimit = 30

// checkInHorizon is how many days ahead a dated plan in the persona's facts
// keeps a quiet day's check-in: a plan coming up may need preparing.
const checkInHorizon = 7

func logCheckInSkipped(persona string, since, now time.Time) {
	record := map[string]any{"severity": "INFO", "message": "check-in skipped " + persona, "persona": persona,
		"reason": "no user posts, due reviews or upcoming dated facts", "since": since.UTC(), "until": now.UTC()}
	if encoded, err := json.Marshal(record); err == nil {
		fmt.Fprintln(os.Stdout, string(encoded))
	}
}
