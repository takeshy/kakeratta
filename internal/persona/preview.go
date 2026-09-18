package persona

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// ReplyPreview is a persona's turn on a post done over, with its tone and
// instructions as they are now saved, and kept from touching anything: what
// it would say, or that it would stay quiet.
type ReplyPreview struct {
	Spoke string `json:"spoke"`
	Quiet bool   `json:"quiet"`
	Error string `json:"error,omitempty"`
}

// PreviewReply runs a persona again over the thread as it stood when it made
// one remark, so a person who has just changed how it talks or what it does
// can see the difference on the same post. The persona reads its notes and
// may use its lookup tools, but nothing it does is kept: no remark lands on
// the timeline, no note or schedule is written, no wake is counted.
func (r *Runner) PreviewReply(ctx context.Context, remarkID, skillID string) (ReplyPreview, error) {
	remark, ok := r.store.Entry(remarkID)
	if !ok || remark.Deleted || remark.Kind != timeline.KindRemark {
		return ReplyPreview{}, errors.New("担当の発言が見つかりません")
	}
	if remark.Speaker != skillID {
		return ReplyPreview{}, errors.New("その担当の発言ではありません")
	}
	client, settings := r.clientOf()
	var skill *Skill
	skills := r.catalogue.Active(settings.Active)
	for i := range skills {
		if skills[i].ID == skillID {
			skill = &skills[i]
		}
	}
	if skill == nil {
		return ReplyPreview{}, errors.New("その担当は有効ではありません")
	}
	entries, err := r.store.Timeline()
	if err != nil {
		return ReplyPreview{}, err
	}
	// The thread as the persona saw it: what was there before it spoke.
	var thread []timeline.Entry
	for _, entry := range timeline.Thread(entries, timeline.RootOf(remark)) {
		if entry.At.Before(remark.At) && entry.ID != remark.ID {
			thread = append(thread, entry)
		}
	}
	if len(thread) == 0 {
		return ReplyPreview{}, errors.New("元の投稿が見つかりません")
	}
	trigger := thread[0]
	for _, entry := range thread {
		if entry.ID == remark.ReplyTo {
			trigger = entry
		}
	}
	named := contains(Named(skills, trigger.Text), skill.ID)

	// The same runner code over a workspace that keeps nothing, so the turn
	// is the real thing without any of its consequences.
	sandbox := New(&dryWorkspace{Workspace: r.store}, r.catalogue, r.clientOf)
	sandbox.search, sandbox.tools = r.search, r.tools
	var problems []string
	sandbox.emit = func(name string, data any) {
		if name != "error" {
			return
		}
		if fields, ok := data.(map[string]any); ok {
			problems = append(problems, fmt.Sprint(fields["message"]))
		}
	}
	said := sandbox.run(ctx, *skill, thread, trigger, named, false, "", client, settings, sandbox.registry(settings))
	preview := ReplyPreview{Quiet: said == nil, Error: strings.Join(problems, "; ")}
	if said != nil {
		preview.Spoke = said.Text
	}
	return preview, nil
}

// dryWorkspace reads from the real workspace and writes nowhere. It is what a
// preview runs over: the persona's notes and the timeline are there to read,
// and everything it would have changed is quietly not changed.
type dryWorkspace struct {
	store.Workspace
}

func (d *dryWorkspace) Append(timeline.Entry) error                                 { return nil }
func (d *dryWorkspace) RecordWake(string, string) error                             { return nil }
func (d *dryWorkspace) SaveMemory(store.Memory) error                               { return nil }
func (d *dryWorkspace) WriteNote(string, string, string, ...store.NoteSource) error { return nil }
func (d *dryWorkspace) DeleteNote(string, string) error                             { return nil }
func (d *dryWorkspace) DropNoteSource(string, string) ([]string, error)             { return nil, nil }
func (d *dryWorkspace) SaveRouting(string, store.Routing) error                     { return nil }
func (d *dryWorkspace) ForgetRouting(string) error                                  { return nil }
func (d *dryWorkspace) ForgetWakes(string) error                                    { return nil }
func (d *dryWorkspace) ForgetMemory(string) error                                   { return nil }
func (d *dryWorkspace) SaveSettings(store.Settings) error                           { return nil }
func (d *dryWorkspace) PersistSkill(string, []byte) error                           { return nil }
func (d *dryWorkspace) DeleteSkill(string) error                                    { return nil }
func (d *dryWorkspace) DeleteSchedule(string, string) error                         { return nil }
func (d *dryWorkspace) DeleteSchedules(string) error                                { return nil }
func (d *dryWorkspace) SavePublication(string, store.Publication) error             { return nil }
func (d *dryWorkspace) ClaimPublication(string, store.Publication) (bool, error)    { return false, nil }
func (d *dryWorkspace) ClaimDailyCheckIn(string, string) (bool, error)              { return false, nil }
func (d *dryWorkspace) ClaimSchedule(string, time.Time, time.Time) (store.Schedule, bool, error) {
	return store.Schedule{}, false, nil
}
func (d *dryWorkspace) SwapSocialValue(context.Context, string, string, store.SocialValue) (bool, error) {
	return false, nil
}
func (d *dryWorkspace) CreateEntry(entry timeline.Entry) (timeline.Entry, bool, error) {
	return entry, false, nil
}
func (d *dryWorkspace) UpdateText(id, _ string) (timeline.Entry, error) {
	entry, _ := d.Workspace.Entry(id)
	return entry, nil
}
func (d *dryWorkspace) UpdateMedia(id string, _ *timeline.Media) (timeline.Entry, error) {
	entry, _ := d.Workspace.Entry(id)
	return entry, nil
}
func (d *dryWorkspace) PutBlob([]byte, string) (string, error) { return "preview", nil }

// SaveSchedule hands back the schedule as if placed, so a persona planning a
// wake in a preview is told it worked and the plan runs to its remark.
func (d *dryWorkspace) SaveSchedule(schedule store.Schedule) (store.Schedule, error) {
	if schedule.ID == "" {
		schedule.ID = "preview"
	}
	return schedule, nil
}
