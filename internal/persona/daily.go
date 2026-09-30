package persona

import (
	"context"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// dailyHeader opens every check-in's instructions, whichever way it talks.
const dailyHeader = `

## Daily check-in（1日1回の定期起床）
今回はユーザーの投稿ではなく、システムによる定期起床です。返答の義務はありません。
`

// dailyInstructions is the minimal check-in: it speaks only about what needs
// the user's decision.
const dailyInstructions = dailyHeader + `このチェックインで話してよいのは、ユーザーの判断が必要なことだけです。見直し時期が来たメモ（そろそろ忘れてよいかの確認）など、対象発言で本体から渡されたものがあるときだけ、それについて短く一言話してください。
それ以外の発見・提案・近況の報告・挨拶はしません。伝えたいことがあっても、ユーザーが次に投稿したときに話してください。
メモの整理（index.md の更新、同じ対象のメモの統合など）は黙って行って構いません。
話すことがなければ Answer の text を空にします。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// dailyShareInstructions is the check-in that also looks for something worth
// bringing back from the last day and the persona's older notes.
const dailyShareInstructions = dailyHeader + `最初に timeline_search を query なし・from/to だけで1回使い、前回のチェックインから今回までの記録を読んでください。期間は対象発言の「Since the previous check-in」に書いてあるので、その from と to をそのまま渡します。特に、ユーザーや他の担当が発言したスレッドのうち、自分が返答していないものから、自分の担当分野と関係する変化やつながりを探します。
次に notes_list で、最近更新されたメモによく現れる tags と同じ tags を持つのに、更新日時が長く前のメモがないか確認してください。候補があれば notes_read で中身を読み、前回のチェックインからの記録と今も具体的な関係があるか確かめます。古いという理由だけでは話しません。
前回のチェックインからの未参加の話題から役立つ関係を発見した場合や、最近よく使う tag と古いメモの間に今伝える価値のあるつながりを発見した場合だけ、短く話してください。
毎日の件数合わせ、一般的なニュース一覧、同じ助言の繰り返し、挨拶だけの投稿は不要です。
伝えることがない場合や根拠を得られない場合は Answer の text を空にします。黙ったままメモを更新しても構いません。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// monthlyHeader opens the check-in of a person heard from once a month.
const monthlyHeader = `

## Monthly check-in（月1回の定期起床）
今回はユーザーの投稿ではなく、システムによる月1回の定期起床です。返答の義務はありません。
この月のタイムラインは本体がまとめを書いています。対象発言に、そのまとめと、前回のチェックインから自分の名前やキーワードが出た投稿があります。timeline_search で期間全体を読み直す必要はありません。細部を確かめたいときだけ、語句や短い期間を指定して検索してください。
`

// monthlyInstructions is a monthly check-in that speaks only about what needs
// the user's decision.
const monthlyInstructions = monthlyHeader + `話してよいのは、ユーザーの判断が必要なことだけです。見直し時期が来たメモの確認など、対象発言で本体から渡されたものがあるときだけ、それについて短く一言話してください。
まとめと投稿から分かった、その人についての事実や出来事は黙ってメモに残し、誕生日や記念日、会う予定が近ければ予定を置いてください。
話すことがなければ Answer の text を空にします。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// monthlyShareInstructions is a monthly check-in that may also bring back
// something worth saying.
const monthlyShareInstructions = monthlyHeader + `まず、まとめと投稿のうち自分の担当する人に関わることを読み、メモにない事実や出来事があれば残してください。誕生日や記念日、会う予定が近ければ予定を置きます。
次に自分のメモを見直し、済んでいない約束や貸し借り、しばらく連絡していないことなど、今伝える価値があることがあれば短く話してください。
挨拶だけ、まとめの繰り返し、一般的な助言は不要です。伝えることがなければ Answer の text を空にします。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// CheckDaily enters the same serial queue as fragment-triggered execution.
func (r *Runner) CheckDaily() {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped {
		return
	}
	if r.queuedLocked(func(item reaction) bool { return item.daily }) {
		return
	}
	r.queue = append(r.queue, reaction{daily: true})
	r.wake.Signal()
}

// checkInLookback is the furthest back a sharing check-in reads: a persona
// back after weeks away catches up on a week, not on everything.
const checkInLookback = 7 * 24 * time.Hour

// dailyAt checks in every active persona once a day, at a minute of the
// user's 07:00-21:00 the app picks for it (see store.CheckInAt). A
// minimal check-in speaks only for what needs the user's decision - a note
// due for review is the one the app hands it; a sharing one may also bring
// back something worth saying.
func (r *Runner) dailyAt(now time.Time) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "daily", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" || personaModel(settings) == "" {
		return
	}
	// The month's summary comes first: a person checking in monthly reads it.
	r.summarizeAt(now, client, settings)
	for _, skill := range r.catalogue.Active(settings.Active) {
		schedule := settings.CheckInFor(skill.ID)
		loc := settings.Location()
		local := now.In(loc)
		root := r.store.Root()
		// A person heard from monthly checks in on the summary's day, once the
		// summary is written. A daily check-in skips the day its minute
		// wrapped, so two do not come hours apart; a monthly one has only its
		// day.
		monthly := schedule.Every == store.CheckInMonthly && skill.IsPerson()
		if monthly {
			if local.Day() != store.SummaryDay(root) || local.Before(store.CheckInAt(root, store.SummaryID, local)) {
				continue
			}
		} else if store.CheckInSkipped(root, skill.ID, local) {
			continue
		}
		if local.Before(store.CheckInAt(root, skill.ID, local)) || !local.Before(store.CheckInCloses(local)) {
			continue
		}
		claimed, previous, err := r.store.ClaimDailyCheckInAt(skill.ID, local.Format("2006-01-02"), now)
		if err != nil {
			r.emit("error", map[string]any{"where": "daily", "message": err.Error()})
			continue
		}
		if !claimed {
			continue
		}
		// What happened since the previous check-in actually ran, read as one
		// span: the minute moves, and a day can be skipped or missed, so "the
		// last 24 hours" would leave stretches unread. With no previous run on
		// record, or one long ago, it reads a day, or a week at most.
		since := previous
		lookback := checkInLookback
		if monthly {
			lookback = summaryLookback
		}
		if since.IsZero() {
			// A first check-in reads a day - or, heard from monthly, the month.
			since = now.Add(-24 * time.Hour)
			if monthly {
				since = now.Add(-lookback)
			}
		} else if now.Sub(since) > lookback {
			since = now.Add(-lookback)
		}
		text := "Daily check-in: " + local.Format(time.RFC3339) + " (" + loc.String() + "). Decide whether there is anything worth sharing today." +
			"\nSince the previous check-in: from \"" + since.In(loc).Format("2006-01-02 15:04") + "\" to \"" + local.Format("2006-01-02 15:04") + "\""
		instructions := dailyShareInstructions
		if schedule.Minimal() {
			text = "Daily check-in: " + local.Format(time.RFC3339) + " (" + loc.String() + "). Speak only about what needs the user's decision."
			instructions = dailyInstructions
		}
		if monthly {
			text = "Monthly check-in: " + local.Format(time.RFC3339) + " (" + loc.String() + ")." +
				"\nSince the previous check-in: from \"" + since.In(loc).Format("2006-01-02 15:04") + "\" to \"" + local.Format("2006-01-02 15:04") + "\"" +
				r.monthlyCheckIn(skill, since, now, loc)
			instructions = monthlyShareInstructions
			if schedule.Minimal() {
				instructions = monthlyInstructions
			}
		}
		// Reviews ride inside the check-in, so the persona posts once.
		if due := r.reviewsDue(skill, now, loc); len(due) > 0 {
			text += "\n\nReview due, oldest first:\n" + reviewList(due, loc)
			instructions += reviewInstructions
		} else if schedule.Minimal() {
			text += "\n\nNothing needs the user's decision today."
		}
		trigger := timeline.Entry{At: local, Speaker: "system", Text: text}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, instructions, false, client, settings, r.registry(settings))
		cancel()
	}
}
