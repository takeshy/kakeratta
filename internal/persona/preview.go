package persona

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	Token string `json:"token,omitempty"`
	// Notes are the paths it would have written, had this been a real turn.
	Notes []string `json:"notes"`
	Error string   `json:"error,omitempty"`
}

// PreviewReply runs a persona again over the thread as it stood at one entry,
// so a person who has just changed how it talks or what it does can see the
// difference on the same post. The entry is either a remark the persona made
// - the thread as it was before it spoke - or a post or line of the user's
// that the persona stayed quiet on - the thread up to and including it. The
// persona reads its notes and may use its lookup tools, but nothing it does
// is kept: no remark lands on the timeline, no note or schedule is written,
// no wake is counted.
func (r *Runner) PreviewReply(ctx context.Context, entryID, skillID string) (ReplyPreview, error) {
	ctx = r.withSkills(ctx)
	at, ok := r.store.Entry(entryID)
	if !ok || at.Deleted {
		return ReplyPreview{}, errors.New("投稿が見つかりません")
	}
	switch {
	case at.Kind == timeline.KindRemark && at.Speaker != skillID:
		return ReplyPreview{}, errors.New("その担当の発言ではありません")
	case at.Kind != timeline.KindRemark && at.Speaker != timeline.User:
		return ReplyPreview{}, errors.New("担当の発言か、自分の投稿を指定してください")
	}
	remark := at
	client, settings := r.clientOf()
	var skill *Skill
	skills := r.skills(ctx).Active(settings.Active)
	for i := range skills {
		if skills[i].ID == skillID {
			skill = &skills[i]
		}
	}
	if skill == nil {
		return ReplyPreview{}, errors.New("その担当は有効ではありません")
	}
	entries, err := store.SearchThread(ctx, r.store, at.ID)
	if err != nil {
		return ReplyPreview{}, err
	}
	// The thread as the persona saw it: what was there before it spoke, or
	// up to the line it was given and stayed quiet on.
	quiet := remark.Kind != timeline.KindRemark
	var thread []timeline.Entry
	for _, entry := range timeline.Thread(entries, timeline.RootOf(remark)) {
		if entry.ID == remark.ID {
			if quiet {
				thread = append(thread, entry)
			}
			continue
		}
		if entry.At.Before(remark.At) {
			thread = append(thread, entry)
		}
	}
	if len(thread) == 0 {
		return ReplyPreview{}, errors.New("元の投稿が見つかりません")
	}
	trigger := thread[0]
	for _, entry := range thread {
		if entry.ID == remark.ReplyTo || (quiet && entry.ID == remark.ID) {
			trigger = entry
		}
	}
	named := contains(Named(skills, trigger.Text), skill.ID)

	// The same runner code over a workspace that keeps nothing, so the turn
	// is the real thing without any of its consequences.
	notes := []string{}
	workspace := &dryWorkspace{Workspace: r.store, notes: &notes}
	sandbox := New(workspace, r.catalogue, r.clientOf)
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
	// A person is waiting on the preview; it is planned in-process.
	said := sandbox.run(withBuiltinPlanner(ctx), *skill, thread, trigger, named, false, "", false, client, settings, sandbox.registry(settings))
	preview := ReplyPreview{Quiet: said == nil, Notes: notes, Error: strings.Join(problems, "; ")}
	if said != nil {
		preview.Spoke = said.Text
	}
	if said != nil && preview.Error == "" && workspace.remark != nil {
		preview.Token = store.NewID("trial")
		if err := r.saveTrial(ctx, preview.Token, trialRecord{Reply: &replyTrial{
			EntryID: entryID, PersonaID: skillID, Original: remark.Text, Remark: workspace.remark, Notes: workspace.changes,
		}}); err != nil {
			return ReplyPreview{}, err
		}
	}
	return preview, nil
}

// dryWorkspace reads from the real workspace and writes nowhere. It is what a
// preview runs over: the persona's notes and the timeline are there to read,
// and everything it would have changed is quietly not changed.
type dryWorkspace struct {
	store.Workspace
	// notes collects the paths a preview would have written.
	notes   *[]string
	remark  *timeline.Entry
	changes []noteChange
}

func (d *dryWorkspace) Append(entry timeline.Entry) error      { d.remark = &entry; return nil }
func (d *dryWorkspace) UpdateEntry(entry timeline.Entry) error { return d.Append(entry) }

// Entry sees the remark this preview produced, so the note keeper can point it
// at the notes it wrote, as it does a real remark.
func (d *dryWorkspace) Entry(id string) (timeline.Entry, bool) {
	if d.remark != nil && d.remark.ID == id {
		return *d.remark, true
	}
	return d.Workspace.Entry(id)
}

// latest is the last change this preview made to a note, if any.
func (d *dryWorkspace) latest(path string) (noteChange, bool) {
	for i := len(d.changes) - 1; i >= 0; i-- {
		if d.changes[i].Path == path {
			return d.changes[i], true
		}
	}
	return noteChange{}, false
}

// Note, Notes and NotePaths read the notes as this preview left them, so what
// runs after a write - the index repair above all - builds on the write rather
// than on the notes as they were.
func (d *dryWorkspace) Note(personaID, path string) (store.Note, bool) {
	if change, ok := d.latest(path); ok {
		if change.Delete {
			return store.Note{}, false
		}
		return store.Note{Path: path, Text: change.Text}, true
	}
	return d.Workspace.Note(personaID, path)
}

func (d *dryWorkspace) Notes(personaID string, paths []string) ([]store.Note, error) {
	var out []store.Note
	for _, path := range paths {
		if note, ok := d.Note(personaID, path); ok {
			out = append(out, note)
		}
	}
	return out, nil
}

func (d *dryWorkspace) NotePaths(personaID string) ([]string, error) {
	paths, err := d.Workspace.NotePaths(personaID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		seen[path] = true
		if _, ok := d.Note(personaID, path); ok {
			out = append(out, path)
		}
	}
	for _, change := range d.changes {
		if !seen[change.Path] {
			seen[change.Path] = true
			if _, ok := d.Note(personaID, change.Path); ok {
				out = append(out, change.Path)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
func (d *dryWorkspace) RecordWake(string, string) error   { return nil }
func (d *dryWorkspace) SaveMemory(store.Memory) error     { return nil }
func (d *dryWorkspace) SaveReaction(store.Reaction) error { return nil }
func (d *dryWorkspace) Reactions(context.Context) ([]store.Reaction, error) {
	return nil, nil
}
func (d *dryWorkspace) FinishReaction(context.Context, string, string, ...string) (bool, error) {
	return true, nil
}
func (d *dryWorkspace) CreateReaction(store.Reaction) error { return nil }
func (d *dryWorkspace) ClaimReaction(context.Context, string, time.Duration) (store.Reaction, []store.Reaction, bool, error) {
	return store.Reaction{}, nil, false, nil
}
func (d *dryWorkspace) RenewReaction(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}
func (d *dryWorkspace) ReleaseReaction(context.Context, string, string) error { return nil }
func (d *dryWorkspace) DeferReaction(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}
func (d *dryWorkspace) UpdateRouting(string, func(*store.Routing) (*store.Routing, error)) error {
	return nil
}
func (d *dryWorkspace) UpdateRecord(context.Context, string, string, time.Time, store.RecordChange) error {
	return nil
}

// A preview holds no lock: it writes nothing another turn could overwrite.
func (d *dryWorkspace) Lock(ctx context.Context, _ string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}
func (d *dryWorkspace) SetClosing(context.Context, bool) error { return nil }
func (d *dryWorkspace) NoteJobs() ([]store.NoteJob, error)     { return nil, nil }
func (d *dryWorkspace) SaveNoteJob(store.NoteJob) error        { return nil }
func (d *dryWorkspace) DeleteNoteJob(string) error             { return nil }
func (d *dryWorkspace) WriteNote(_, path, content string, sources ...store.NoteSource) error {
	// The index and the log are housekeeping; what a person is shown is the notes
	// that carry what was said.
	if d.notes != nil && !isRecordsPath(path) {
		*d.notes = append(*d.notes, path)
	}
	d.changes = append(d.changes, noteChange{Path: path, Text: content, Sources: append([]store.NoteSource(nil), sources...)})
	return nil
}
func (d *dryWorkspace) DeleteNote(_, path string) error {
	d.changes = append(d.changes, noteChange{Path: path, Delete: true})
	return nil
}
func (d *dryWorkspace) NotesBySource(string, string) ([]string, error)           { return nil, nil }
func (d *dryWorkspace) DropNoteSource(string, string) ([]string, error)          { return nil, nil }
func (d *dryWorkspace) SaveRouting(string, store.Routing) error                  { return nil }
func (d *dryWorkspace) ForgetRouting(string) error                               { return nil }
func (d *dryWorkspace) ForgetWakes(string) error                                 { return nil }
func (d *dryWorkspace) ForgetMemory(string) error                                { return nil }
func (d *dryWorkspace) SaveSettings(store.Settings) error                        { return nil }
func (d *dryWorkspace) PersistSkill(string, []byte) error                        { return nil }
func (d *dryWorkspace) DeleteSkill(string) error                                 { return nil }
func (d *dryWorkspace) DeleteSchedule(string, string) error                      { return nil }
func (d *dryWorkspace) DeleteSchedules(string) error                             { return nil }
func (d *dryWorkspace) SavePublication(string, store.Publication) error          { return nil }
func (d *dryWorkspace) ClaimPublication(string, store.Publication) (bool, error) { return false, nil }
func (d *dryWorkspace) ClaimDailyCheckInAt(string, string, time.Time) (bool, time.Time, error) {
	return false, time.Time{}, nil
}
func (d *dryWorkspace) ClaimSchedule(string, time.Time, time.Time) (store.Schedule, bool, error) {
	return store.Schedule{}, false, nil
}
func (d *dryWorkspace) SwapSocialValue(context.Context, string, string, store.SocialValue) (bool, error) {
	return false, nil
}
func (d *dryWorkspace) CreateEntry(entry timeline.Entry, _ ...store.Reaction) (timeline.Entry, bool, error) {
	return entry, false, nil
}
func (d *dryWorkspace) UpdateText(id, _ string, _ ...store.Reaction) (timeline.Entry, error) {
	entry, _ := d.Workspace.Entry(id)
	return entry, nil
}
func (d *dryWorkspace) UpdateMedia(id string, _ *timeline.Media) (timeline.Entry, error) {
	entry, _ := d.Workspace.Entry(id)
	return entry, nil
}
func (d *dryWorkspace) PutBlob([]byte, string) (string, error) { return "preview", nil }
func (d *dryWorkspace) DeleteBlob(string) error                { return nil }

// SaveSchedule hands back the schedule as if placed, so a persona planning a
// wake in a preview is told it worked and the plan runs to its remark.
func (d *dryWorkspace) SaveSchedule(schedule store.Schedule) (store.Schedule, error) {
	if schedule.ID == "" {
		schedule.ID = "preview"
	}
	return schedule, nil
}
