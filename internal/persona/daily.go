package persona

import (
	"context"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

const dailyInstructions = `

## Daily check-in（1日1回の定期起床）
今回はユーザーの投稿ではなく、システムによる定期起床です。返答の義務はありません。
最初に timeline_search を query なし・from/to だけで1回使い、起床時刻から直近24時間の記録を読んでください。特に、ユーザーや他の担当が発言したスレッドのうち、自分が返答していないものから、自分の担当分野と関係する変化やつながりを探します。
次に notes_list で、最近更新されたメモによく現れる tags と同じ tags を持つのに、更新日時が長く前のメモがないか確認してください。候補があれば notes_read で中身を読み、直近24時間の記録と今も具体的な関係があるか確かめます。古いという理由だけでは話しません。
直近24時間の未参加の話題から役立つ関係を発見した場合や、最近よく使う tag と古いメモの間に今伝える価値のあるつながりを発見した場合だけ、短く話してください。
毎日の件数合わせ、一般的なニュース一覧、同じ助言の繰り返し、挨拶だけの投稿は不要です。
伝えることがない場合や根拠を得られない場合は speak の text を空にします。黙ったままメモを更新しても構いません。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

// CheckDaily enters the same serial queue as fragment-triggered execution.
func (r *Runner) CheckDaily() {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped {
		return
	}
	for _, item := range r.queue {
		if item.daily {
			return
		}
	}
	r.queue = append(r.queue, reaction{daily: true})
	r.wake.Signal()
}

func (r *Runner) dailyAt(now time.Time) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "daily", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	for _, skill := range r.catalogue.Active(settings.Active) {
		if skill.ID != "hermes" && (client == nil || client.BaseURL == "") {
			continue
		}
		if skill.ID != "hermes" && personaModel(settings) == "" {
			continue
		}
		schedule := settings.DailyCheckIns[skill.ID]
		day, due := schedule.Due(now)
		if !due {
			if skill.ID != "hermes" {
				r.reviewAt(now, skill, schedule, client, settings)
			}
			continue
		}
		claimed, err := r.store.ClaimDailyCheckIn(skill.ID, day)
		if err != nil {
			r.emit("error", map[string]any{"where": "daily", "message": err.Error()})
			continue
		}
		if !claimed {
			continue
		}
		loc, _ := time.LoadLocation(schedule.TimeZone)
		text := "Daily check-in: " + now.In(loc).Format(time.RFC3339) + " (" + schedule.TimeZone + "). Decide whether there is anything worth sharing today."
		instructions := dailyInstructions
		// Reviews ride inside the check-in, so the persona posts once.
		if skill.ID != "hermes" {
			if due := r.reviewsDue(skill, now, loc); len(due) > 0 {
				text += "\n\nReview due, oldest first:\n" + reviewList(due, loc)
				instructions += reviewInstructions
			}
		}
		trigger := timeline.Entry{At: now.In(loc), Speaker: "system", Text: text}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, instructions, false, client, settings, r.registry(settings))
		cancel()
	}
}

// reviewAt wakes a persona that has no daily check-in for its reviews, once
// a day and only when a note is due. An enabled check-in that is not yet due
// today is left alone: the reviews ride with it later. The day is claimed
// with the check-in's own key, which a disabled check-in never takes, and
// only once something is due, so a check-in the user turns on later the same
// day is not lost to an empty morning.
func (r *Runner) reviewAt(now time.Time, skill Skill, schedule store.DailyCheckIn, client *llm.Client, settings store.Settings) {
	if schedule.Enabled && schedule.Validate() == nil {
		return
	}
	day, zone, ready := reviewSlot(now, schedule, settings)
	if !ready || r.reviewChecked[skill.ID] == day {
		return
	}
	due := r.reviewsDue(skill, now, zone)
	if len(due) == 0 {
		r.reviewChecked[skill.ID] = day
		return
	}
	claimed, err := r.store.ClaimDailyCheckIn(skill.ID, day)
	if err != nil {
		r.emit("error", map[string]any{"where": "review", "message": err.Error()})
		return
	}
	if !claimed {
		return
	}
	trigger := timeline.Entry{At: now.In(zone), Speaker: "system", Text: "Review: " + now.In(zone).Format(time.RFC3339) + " (" + zone.String() + "). These notes reached their review date, oldest first:\n" + reviewList(due, zone) + "\nAsk the user about one of them."}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, reviewInstructions, false, client, settings, r.registry(settings))
}
