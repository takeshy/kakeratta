package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Schedules is the app's wake bookkeeping, kept as an interface so this package
// does not depend on how a workspace persists it.
type Schedules interface {
	Schedules(skill string) ([]store.Schedule, error)
	SaveSchedule(store.Schedule) (store.Schedule, error)
	UpdateSchedule(string, string, store.SchedulePatch) (store.Schedule, error)
	DeleteSchedule(skill, id string) error
}

// ownerKey carries which persona asked. Schedules can be read across personas
// in the workspace, but only their owner can cancel them.
type ownerKey struct{}

func ownerOf(ctx context.Context) (string, error) {
	owner, _ := ctx.Value(ownerKey{}).(string)
	if owner == "" {
		return "", errors.New("schedule を使えるのは担当だけです")
	}
	return owner, nil
}

var scheduleTool = Tool{
	Name: "schedule",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"action":         map[string]any{"type": "string", "description": "set / list / update / cancel", "enum": []any{"set", "list", "update", "cancel"}},
		"at":             map[string]any{"type": "string", "description": "起こしてほしい時刻。RFC3339（2026-09-17T20:00:00+09:00）"},
		"in_minutes":     map[string]any{"type": "number", "description": "at の代わりに、いまから何分後か"},
		"note":           map[string]any{"type": "string", "description": "起きたときに自分が読む、何のためかの覚書"},
		"user_requested": map[string]any{"type": "boolean", "description": "ユーザーが日時・繰り返しを指定して実行を依頼した予定ならtrue。担当が自発的に置く確認はfalse"},
		"notify":         map[string]any{"type": "boolean", "description": "Push通知を送るか。新規で省略するとtrue。falseでも予定は実行してタイムラインに投稿します"},
		"daily":          map[string]any{"type": "boolean", "description": "毎日繰り返すか"},
		"time_zone":      map[string]any{"type": "string", "description": "IANAのタイムゾーン。毎日繰り返すときは必須"},
		"id":             map[string]any{"type": "string", "description": "update / cancel するときの s_ から始まるid"},
	}},
	Usage: `schedule — 自分を起こす時刻を本体に預ける。カレンダーや目覚ましではなく、あなたの担当の用のためのものです。atの代わりにin_minutesでも置けます。noteは起きたときに自分が読む「何のためか」です。時刻はユーザーに尋ねず、書かれた言葉と過去の記録から決めます。済んだと分かったこと、やめたことは cancel してください。未来の時刻を設定できます。list は他の担当の予定も含めて返します。skill は予定を置いた担当ID、owner は担当名（ID）です。自分の予定は起動時の一覧で、他の担当の予定は調査フェーズの schedules_list で同じ用の予定を確認し、重複を避けてください。update / cancel できるのは自分の予定だけです。update は id と変更する at または notify を指定し、ほかの属性を維持します。notify:false はPush通知だけを止め、実行とタイムラインへの投稿は続けます。新規予定の通知は既定でONです。担当ごとに20件まで。発火すると1回きりのものは消え、daily:true のものは翌日の同じ時刻に進みます。user_requested:true はユーザーが時刻や繰り返しを指定して依頼した実行です（例:毎朝ニュースをまとめる）。担当が自発的に置く用事の確認はfalse。起床時、trueなら依頼内容に必要な調査と発言を行い、一言に制限しません。falseなら従来どおり必要なときだけ軽く一言。`,
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
			return listSchedules(r)
		case "update":
			patch := store.SchedulePatch{}
			if at := args.str("at"); at != "" {
				parsed, err := time.Parse(time.RFC3339, at)
				if err != nil {
					return Result{}, store.ErrScheduleTime
				}
				patch.At = &parsed
			}
			if value, ok := args["notify"].(bool); ok {
				patch.Notify = &value
			}
			if patch.At == nil && patch.Notify == nil {
				return Result{}, errors.New("at または notify を指定してください")
			}
			saved, err := r.schedules.UpdateSchedule(owner, args.str("id"), patch)
			if err != nil {
				return Result{}, err
			}
			raw, _ := json.Marshal(scheduleRow(r, saved))
			return Result{Text: string(raw)}, nil
		case "cancel":
			id := args.str("id")
			if err := r.schedules.DeleteSchedule(owner, id); err != nil {
				return Result{}, err
			}
			return Result{Text: "schedule " + id + " を消しました。"}, nil
		default:
			return Result{}, fmt.Errorf("action は set / list / update / cancel です: %q", action)
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
	if notify, ok := args["notify"].(bool); ok {
		schedule.Notify = &notify
	}
	if requested, ok := args["user_requested"].(bool); ok {
		schedule.UserRequested = requested
	}
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

func listSchedules(r *Registry) (Result, error) {
	schedules, err := r.schedules.Schedules("")
	if err != nil {
		return Result{}, err
	}
	if len(schedules) == 0 {
		return Result{Text: "置いてある schedule はありません。"}, nil
	}
	rows := make([]map[string]any, 0, len(schedules))
	for _, schedule := range schedules {
		rows = append(rows, scheduleRow(r, schedule))
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

// schedulesListTool exposes a bounded, read-only view of current workspace schedules.
var schedulesListTool = Tool{
	Name:     "schedules_list",
	ReadOnly: true,
	Parameters: map[string]any{"type": "object", "properties": map[string]any{
		"skill": map[string]any{"type": "string", "description": "担当ID。省略すると全担当"},
		"query": map[string]any{"type": "string", "description": "予定の覚書・担当名から探す語句（部分一致）"},
		"from":  map[string]any{"type": "string", "description": "通知日時の開始。RFC3339 またはユーザーのタイムゾーンの YYYY-MM-DD HH:MM / YYYY-MM-DD"},
		"to":    map[string]any{"type": "string", "description": "通知日時の終了。日付のみはその日の終わりまで"},
		"limit": map[string]any{"type": "number", "description": "件数。1〜30、既定20"},
		"after": map[string]any{"type": "string", "description": "続きの取得には前の結果の next_after を指定。同じ絞り込みを使う"},
	}},
	Usage: `schedules_list — 現在のスケジュールを閲覧する専用ツール。自分の予定は最初に渡されます。他の担当と通知が重複しそうなときは、このツールで確認してから回答してください。skill（担当ID）、query（覚書・担当名の部分一致）、from/to（通知日時）で絞れます。担当名（ID）、予定ID、日時、繰り返し、覚書を返します。1回20件、最大30件。next_after があれば同じ条件と after で続きを確認できます。他の担当の予定は変更・取消できません。`,
	run: func(ctx context.Context, r *Registry, args Args, _ *timeline.Place) (Result, error) {
		if r.schedules == nil {
			return Result{}, errors.New("schedule が設定されていません")
		}
		if _, err := ownerOf(ctx); err != nil {
			return Result{}, err
		}
		window, err := searchWindow(args.str("from"), args.str("to"), r.location())
		if err != nil {
			return Result{}, err
		}
		schedules, err := r.schedules.Schedules(args.str("skill"))
		if err != nil {
			return Result{}, err
		}
		query := strings.ToLower(args.str("query"))
		filtered := make([]store.Schedule, 0, len(schedules))
		for _, schedule := range schedules {
			if !window.From.IsZero() && schedule.At.Before(window.From) || !window.To.IsZero() && schedule.At.After(window.To) {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(schedule.Note+" "+r.personaNames[schedule.Skill]+" "+schedule.Skill), query) {
				continue
			}
			filtered = append(filtered, schedule)
		}
		start := 0
		if after := args.str("after"); after != "" {
			found := false
			for i, schedule := range filtered {
				if schedule.ID == after {
					start = i + 1
					found = true
					break
				}
			}
			if !found {
				return Result{}, errors.New("after の予定が見つかりません。after を省略して再取得してください")
			}
		}
		limit := max(1, min(30, int(args.num("limit", 20))))
		end := min(len(filtered), start+limit)
		rows := make([]map[string]any, 0, end-start)
		for _, schedule := range filtered[start:end] {
			rows = append(rows, scheduleRow(r, schedule))
		}
		next := ""
		if end < len(filtered) {
			next = filtered[end-1].ID
		}
		raw, err := json.Marshal(map[string]any{"schedules": rows, "total": len(filtered), "next_after": next})
		if err != nil {
			return Result{}, err
		}
		return Result{Text: string(raw)}, nil
	},
}

func scheduleRow(r *Registry, schedule store.Schedule) map[string]any {
	owner := schedule.Skill
	if name := r.personaNames[schedule.Skill]; name != "" {
		owner = fmt.Sprintf("%s（%s）", name, schedule.Skill)
	}
	annual := schedule.PersonEvent != nil && schedule.PersonEvent.Annual
	return map[string]any{"id": schedule.ID, "skill": schedule.Skill, "owner": owner, "at": localTime(schedule).Format(time.RFC3339), "daily": schedule.Daily, "user_requested": schedule.UserRequested, "notify": schedule.NotificationsEnabled(), "annual": annual, "note": schedule.Note}
}
