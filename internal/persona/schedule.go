package persona

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

const scheduleInstructions = `

## Schedule（自分が預けた時刻による起床）
今回はユーザーの投稿ではなく、あなたが本体に預けた schedule による起床です。何のためかは対象発言に入っています。
まず自分のメモや timeline_search で、その用が済んでいるかを確かめてください。timeline_search は from と to で期間を指定でき、期間だけなら語句なしでその間の記録を全部返すので、今朝の投稿に済んだ様子があるかは1回で調べられます。
済んでいれば speak の text は空にします。まだなら、そのことに軽く一言添える程度にしてください（「花、買った?」「そういえば朝の薬」）。
催促・叱責はしません。やらなかった理由を尋ねることもしません。
根拠が得られないときも speak の text は空にします。黙ったままメモを更新しても構いません。
済んだと分かったこと、やめたことは、下の schedule 一覧の id を使って schedule の cancel で消してください。時刻がずれてきたら置き直してください。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

const forgottenInstructions = `

## メモが消されました
ユーザーが、あなたのメモを消しました。どれが消えたかは対象発言に入っています。
本体はあなたのメモを書き換えません。整合を取るのはあなたの仕事です。
消えた項目に紐づく schedule が下の一覧に残っていれば、不要なら cancel、続けるなら置き直してください。
index.md や他のメモからの参照も直してください。
これはユーザーの操作の結果です。消したことを責めたり、理由を尋ねたりしないでください。
伝えることがなければ speak の text は空にします。整えたことをわざわざ報告する必要はありません。
`

// registry is the tool set as it exists for this workspace: the user's search
// key, the operator's endpoints, and the app's own wake bookkeeping.
func (r *Runner) registry(settings store.Settings) *tools.Registry {
	return tools.New(r.tools).WithSearch(r.search).WithTavilyKey(settings.Search.APIKey).
		WithSchedules(r.store).WithZone(settings.TimeZone)
}

// CheckSchedules enters the same serial queue as posts and the daily check-in.
func (r *Runner) CheckSchedules() {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped {
		return
	}
	for _, item := range r.queue {
		if item.schedules {
			return
		}
	}
	r.queue = append(r.queue, reaction{schedules: true})
	r.wake.Signal()
}

// NoteForgotten tells a persona that the user deleted one of its notes, so it
// can put the rest of its memory - and the schedules that went with it - back in
// order itself. Deleting several notes in a row is one wake, not one each.
func (r *Runner) NoteForgotten(skill, path string) {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopped || skill == "" || path == "" {
		return
	}
	for _, item := range r.queue {
		if item.forgotten != nil && item.forgotten.skill == skill {
			item.forgotten.paths = append(item.forgotten.paths, path)
			return
		}
	}
	r.queue = append(r.queue, reaction{forgotten: &forgotten{skill: skill, paths: []string{path}}})
	r.wake.Signal()
}

// schedulesAt runs the schedules that are due, one at a time.
//
// A schedule is claimed before the model is called, so a silent or failed wake
// is not repeated, and one that nobody could run in time is dropped rather than
// replayed.
func (r *Runner) schedulesAt(now time.Time) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "schedule", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" {
		return
	}
	schedules, err := r.store.Schedules("")
	if err != nil {
		r.emit("error", map[string]any{"where": "schedule", "message": err.Error()})
		return
	}
	active := map[string]bool{}
	for _, skill := range r.catalogue.Active(settings.Active) {
		active[skill.ID] = true
	}
	cleared := map[string]bool{}
	for _, schedule := range schedules {
		if schedule.At.After(now) {
			continue
		}
		skill, known := r.catalogue.Find(schedule.Skill)
		if !known || !active[schedule.Skill] || personaModel(settings) == "" {
			// A persona that was paused or removed keeps its notes, not its wakes.
			if !cleared[schedule.Skill] {
				cleared[schedule.Skill] = true
				if err := r.store.DeleteSchedules(schedule.Skill); err != nil {
					r.emit("error", map[string]any{"where": "schedule", "message": err.Error()})
				}
			}
			continue
		}
		fired, claimed, err := r.store.ClaimSchedule(schedule.ID, schedule.At, now)
		if err != nil {
			r.emit("error", map[string]any{"where": "schedule", "message": err.Error()})
			continue
		}
		if !claimed || now.Sub(fired.At) > store.ScheduleGrace {
			continue
		}
		when := fired.At
		zone := fired.TimeZone
		if loc, err := time.LoadLocation(zone); err == nil {
			when = fired.At.In(loc)
		} else {
			zone = "UTC"
		}
		trigger := timeline.Entry{At: now, Speaker: "system", Text: fmt.Sprintf(
			"Schedule: %s. Set for %s (%s). Check whether it is already done before saying anything.",
			fired.Note, when.Format(time.RFC3339), zone)}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, false, scheduleInstructions, false, client, settings, r.registry(settings))
		cancel()
	}
}

// forgottenAt hands the persona the paths the user deleted.
func (r *Runner) forgottenAt(gone forgotten) {
	defer func() {
		if problem := recover(); problem != nil {
			r.emit("error", map[string]any{"where": "forgotten", "message": fmt.Sprint(problem)})
		}
	}()
	client, settings := r.clientOf()
	if client == nil || client.BaseURL == "" {
		return
	}
	skill, known := r.catalogue.Find(gone.skill)
	if !known || personaModel(settings) == "" {
		return
	}
	now := time.Now()
	trigger := timeline.Entry{At: now, Speaker: "system", Text: fmt.Sprintf(
		"The user deleted your note(s): %s. Put the rest of your memory and your schedules back in order; nothing was rewritten for you.",
		strings.Join(gone.paths, ", "))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// Silent for the same reason as a deleted post: the user removed a note, and
	// tidying up is not an occasion to say anything.
	r.run(ctx, skill, []timeline.Entry{trigger}, trigger, false, true, forgottenInstructions, false, client, settings, r.registry(settings))
}
