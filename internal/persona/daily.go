package persona

import (
	"context"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

const dailyInstructions = `

## Check-in（朝・昼・晩の定期起床）
今回はユーザーの投稿ではなく、朝・昼・晩に一度ずつあるシステムの定期起床です。どの時間帯かは対象発言に書かれています。返答の義務はありません。
自分のメモを必要に応じて読み、timeline_search や宣言した外部ツールで確かめてください。
今の時間帯に関係するメモや、この人が最近気にしていることに伝えるべきことがある場合だけ、短く話してください。
時間帯ごとの件数合わせ、一般的なニュース一覧、同じ日のほかの時間帯に言ったことの繰り返し、挨拶だけの投稿は不要です。
伝えることがない場合や根拠を得られない場合は silent。黙ったままメモを更新しても構いません。
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
	if client == nil || client.BaseURL == "" {
		return
	}
	for _, skill := range r.catalogue.Active(settings.Active) {
		schedule := settings.DailyCheckIns[skill.ID]
		key, slot, due := schedule.Due(now)
		if !due || modelFor(skill.Model, settings) == "" {
			continue
		}
		claimed, err := r.store.ClaimDailyCheckIn(skill.ID, key)
		if err != nil {
			r.emit("error", map[string]any{"where": "daily", "message": err.Error()})
			continue
		}
		if !claimed {
			continue
		}
		loc, _ := time.LoadLocation(schedule.TimeZone)
		trigger := timeline.Entry{At: now.In(loc), Speaker: "system", Text: fmt.Sprintf(
			"Check-in: %s (%s), %s (%s). Decide whether there is anything worth sharing for this part of the day.",
			slot.Label, slot.Name, now.In(loc).Format(time.RFC3339), schedule.TimeZone)}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, client, settings, tools.New(r.tools).WithSearch(r.search).WithTavilyKey(settings.Search.APIKey))
		cancel()
	}
}
