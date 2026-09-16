package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Schedules is the app's wake bookkeeping, kept as an interface so this package
// does not depend on how a workspace persists it.
type Schedules interface {
	Schedules(skill string) ([]store.Schedule, error)
	SaveSchedule(store.Schedule) (store.Schedule, error)
	DeleteSchedule(skill, id string) error
}

// ownerKey carries which persona asked, so a schedule belongs to its setter and
// cannot be listed or cancelled by another one.
type ownerKey struct{}

func ownerOf(ctx context.Context) (string, error) {
	owner, _ := ctx.Value(ownerKey{}).(string)
	if owner == "" {
		return "", errors.New("schedule を使えるのは担当だけです")
	}
	return owner, nil
}

var scheduleTool = Tool{
	Name:  "schedule",
	Usage: `schedule — 自分を起こす時刻を本体に預ける。カレンダーや目覚ましではなく、あなたの担当の用のためのものです。{"tool":"schedule","args":{"action":"set","at":"2026-09-17T20:00:00+09:00","note":"花を買う予定だった","daily":false,"time_zone":"Asia/Tokyo"}}。atの代わりに {"in_minutes":90} でも置けます。action は set / list / cancel（cancelは {"id":"s_..."}）。noteは起きたときに自分が読む「何のためか」です。メモを書いたら、同じ返答の中で schedule も置くか直してください（メモと本体の予定は別の場所にあります）。時刻はユーザーに尋ねず、書かれた言葉と過去の記録から決めます。済んだと分かったこと、やめたことは cancel してください。設定できるのは30日以内、担当ごとに20件まで。発火すると1回きりのものは消え、daily:true のものは翌日の同じ時刻に進みます。鳴っても言うことがなければ silent。`,
	run: func(ctx context.Context, r *Registry, args Args, _ *timeline.Place) (Result, error) {
		if r.schedules == nil {
			return Result{}, errors.New("schedule が設定されていません")
		}
		owner, err := ownerOf(ctx)
		if err != nil {
			return Result{}, err
		}
		switch action := args.str("action"); action {
		case "", "set":
			return setSchedule(r, owner, args)
		case "list":
			return listSchedules(r, owner)
		case "cancel":
			id := args.str("id")
			if err := r.schedules.DeleteSchedule(owner, id); err != nil {
				return Result{}, err
			}
			return Result{Text: "schedule " + id + " を消しました。"}, nil
		default:
			return Result{}, fmt.Errorf("action は set / list / cancel です: %q", action)
		}
	},
}

func setSchedule(r *Registry, owner string, args Args) (Result, error) {
	now := time.Now()
	zone := args.str("time_zone")
	if zone == "" || zone == "Local" {
		zone = r.zone
	}
	schedule := store.Schedule{Skill: owner, Note: args.str("note"), TimeZone: zone}
	if daily, ok := args["daily"].(bool); ok {
		schedule.Daily = daily
	}
	switch {
	case args.str("at") != "":
		at, err := time.Parse(time.RFC3339, args.str("at"))
		if err != nil {
			return Result{}, errors.New(`at は 2026-09-17T20:00:00+09:00 のような RFC3339 で指定してください`)
		}
		schedule.At = at.UTC()
	case args.num("in_minutes", 0) > 0:
		schedule.At = now.Add(time.Duration(args.num("in_minutes", 0)) * time.Minute).UTC()
	default:
		return Result{}, errors.New("at か in_minutes のどちらかを指定してください")
	}
	// A repeating wake has to keep its local clock time, so the zone has to be a
	// real one rather than whatever the server happens to run in.
	if schedule.Daily && (schedule.TimeZone == "" || schedule.TimeZone == "Local") {
		return Result{}, store.ErrScheduleZone
	}
	if err := schedule.Validate(now); err != nil {
		return Result{}, err
	}
	saved, err := r.schedules.SaveSchedule(schedule)
	if err != nil {
		return Result{}, err
	}
	repeat := "1回だけ"
	if saved.Daily {
		repeat = "毎日"
	}
	return Result{Text: fmt.Sprintf("schedule %s を置きました（%s・%s）: %s",
		saved.ID, localTime(saved).Format("2006-01-02 15:04 MST"), repeat, saved.Note)}, nil
}

func listSchedules(r *Registry, owner string) (Result, error) {
	schedules, err := r.schedules.Schedules(owner)
	if err != nil {
		return Result{}, err
	}
	if len(schedules) == 0 {
		return Result{Text: "置いてある schedule はありません。"}, nil
	}
	rows := make([]map[string]any, 0, len(schedules))
	for _, schedule := range schedules {
		rows = append(rows, map[string]any{
			"id": schedule.ID, "at": localTime(schedule).Format(time.RFC3339),
			"daily": schedule.Daily, "note": schedule.Note,
		})
	}
	raw, _ := json.Marshal(rows)
	return Result{Text: string(raw)}, nil
}

// localTime reads a schedule back in the zone it was set in, which is how the
// persona thinks about it.
func localTime(schedule store.Schedule) time.Time {
	if loc, err := time.LoadLocation(schedule.TimeZone); err == nil {
		return schedule.At.In(loc)
	}
	return schedule.At
}
