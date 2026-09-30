package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// keeperSnapshot is the context of a completed turn. System wakes have no
// timeline entry, so their input must travel with the durable job.
type keeperSnapshot struct {
	Skill     Skill               `json:"skill"`
	Thread    []timeline.Entry    `json:"thread"`
	Trigger   timeline.Entry      `json:"trigger"`
	Post      timeline.Entry      `json:"post"`
	RemarkID  string              `json:"remarkId,omitempty"`
	Answer    string              `json:"answer"`
	Wake      string              `json:"wake"`
	Zone      string              `json:"zone"`
	Names     map[string]string   `json:"names"`
	Own       []store.NoteSource  `json:"own"`
	Layout    string              `json:"layout"`
	Schedules []ScheduleOperation `json:"schedules"`
	Preread   []string            `json:"preread"`
	Handed    []string            `json:"handed"`
	Named     bool                `json:"named"`
	Listening bool                `json:"listening"`
}

func snapshotKeeper(k keeperTurn, remark *timeline.Entry) keeperSnapshot {
	s := keeperSnapshot{Skill: k.skill, Thread: k.thread, Trigger: k.trigger, Post: k.post,
		Answer: k.answer, Wake: k.wake, Zone: k.here.String(), Names: k.names,
		Own: k.own, Layout: k.layout, Schedules: k.schedules, Preread: k.preread,
		Handed: k.handed, Named: k.rules.named, Listening: k.rules.listening}
	if remark != nil {
		s.RemarkID = remark.ID
	}
	return s
}

func (s keeperSnapshot) turn(r *Runner, settings store.Settings) keeperTurn {
	zone, err := time.LoadLocation(s.Zone)
	if err != nil {
		zone = settings.Location()
	}
	skill := s.Skill
	set := r.registry(settings).For(skill.Tools).As(skill.ID)
	return keeperTurn{skill: skill, set: set, thread: s.Thread, trigger: s.Trigger,
		post: s.Post, answer: s.Answer, wake: s.Wake, here: zone, names: s.Names,
		own: s.Own, layout: s.Layout, schedules: s.Schedules, preread: s.Preread,
		handed: s.Handed, rules: turnRules{named: s.Named, listening: s.Listening,
			noteRequired: s.Trigger.ID != "" && !s.Listening, set: set, memory: skill.Memory}}
}

func noteRetryDelay(attempts int) time.Duration {
	delay := time.Minute
	for i := 1; i < attempts && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// finishNoteJob saves a failure for a later pass. A successful job is removed;
// a stale successful job only needs an index pass if deletion fails.
func (r *Runner) finishNoteJob(job store.NoteJob, stage string, failure error) {
	if failure == nil {
		job.Stage = "index"
		job.NextAttempt = time.Time{}
		if err := r.store.SaveNoteJob(job); err != nil {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
			return
		}
		if err := r.store.DeleteNoteJob(job.ID); err != nil {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
		}
		return
	}
	job.Stage = stage
	job.Attempts++
	job.NextAttempt = time.Now().Add(noteRetryDelay(job.Attempts))
	job.LastError = failure.Error()
	if err := r.store.SaveNoteJob(job); err != nil {
		r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
	}
	logKeeper(keeperRecord{Persona: job.Persona, Error: fmt.Sprintf("retry scheduled: %s", failure)})
}

func (r *Runner) runKeeperJob(ctx context.Context, client *llm.Client, settings store.Settings, job store.NoteJob, snapshot keeperSnapshot) []string {
	if job.Stage == "index" {
		held, release, err := r.lockKeeper(ctx, job.Persona)
		if err == nil {
			_, err = r.repairIndex(held, job.Persona)
			if err == nil {
				// A lock lost meanwhile means another may have written too.
				err = held.Err()
			}
			release()
		}
		r.finishNoteJob(job, "index", err)
		return nil
	}
	written, stage, err := r.keepNotes(ctx, client, settings, snapshot.turn(r, settings))
	if snapshot.RemarkID != "" {
		if remark, ok := r.store.Entry(snapshot.RemarkID); ok {
			r.pointAtNotes(&remark, written)
		}
	}
	r.finishNoteJob(job, stage, err)
	return written
}

func (r *Runner) retryNoteJobs(ctx context.Context, now time.Time) {
	jobs, err := r.store.NoteJobs()
	if err != nil {
		r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
		return
	}
	for _, job := range jobs {
		if job.NextAttempt.After(now) {
			continue
		}
		exists := false
		for _, skill := range r.skills(ctx).All() {
			if skill.ID == job.Persona {
				exists = true
				break
			}
		}
		if !exists {
			if err := r.store.DeleteNoteJob(job.ID); err != nil {
				r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
			}
			continue
		}
		var snapshot keeperSnapshot
		if err := json.Unmarshal(job.Payload, &snapshot); err != nil {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
			continue
		}
		if job.Stage == "notes" && snapshot.Trigger.ID != "" {
			current, ok := r.store.Entry(snapshot.Trigger.ID)
			root, rootOK := r.store.Entry(snapshot.Post.ID)
			if !ok || current.Deleted || current.Text != snapshot.Trigger.Text ||
				(snapshot.Post.ID != "" && (!rootOK || root.Deleted)) {
				// A deletion or edit has its own tidy-up turn. Replaying the old
				// input would put back information the user changed or removed.
				job.Stage = "index"
				r.runKeeperJob(context.Background(), nil, store.Settings{}, job, snapshot)
				continue
			}
		}
		client, settings := r.clientOf()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		r.runKeeperJob(ctx, client, settings, job, snapshot)
		cancel()
	}
}
