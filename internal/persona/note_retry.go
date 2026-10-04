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
// timeline entry, so their input must travel with the durable job. Jobs saved
// before memory was kept in fields carry more; what is not read here is
// ignored.
type keeperSnapshot struct {
	Skill    Skill             `json:"skill"`
	Thread   []timeline.Entry  `json:"thread"`
	Trigger  timeline.Entry    `json:"trigger"`
	RemarkID string            `json:"remarkId,omitempty"`
	Answer   string            `json:"answer"`
	Wake     string            `json:"wake"`
	Zone     string            `json:"zone"`
	Names    map[string]string `json:"names"`
}

func snapshotKeeper(k keeperTurn) keeperSnapshot {
	return keeperSnapshot{Skill: k.skill, Thread: k.thread, Trigger: k.trigger, RemarkID: k.replyID,
		Answer: k.answer, Wake: k.wake, Zone: k.here.String(), Names: k.names}
}

func (s keeperSnapshot) turn(settings store.Settings) keeperTurn {
	zone, err := time.LoadLocation(s.Zone)
	if err != nil {
		zone = settings.Location()
	}
	return keeperTurn{skill: s.Skill, thread: s.Thread, trigger: s.Trigger, answer: s.Answer,
		wake: s.Wake, here: zone, names: s.Names, replyID: s.RemarkID}
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

// finishNoteJob removes a job that succeeded and saves a failure for a later
// pass.
func (r *Runner) finishNoteJob(job store.NoteJob, failure error) {
	if failure == nil {
		if err := r.store.DeleteNoteJob(job.ID); err != nil {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
		}
		return
	}
	job.Stage = "notes"
	job.Attempts++
	job.NextAttempt = time.Now().Add(noteRetryDelay(job.Attempts))
	job.LastError = failure.Error()
	if err := r.store.SaveNoteJob(job); err != nil {
		r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
	}
	logKeeper(keeperRecord{Persona: job.Persona, Error: fmt.Sprintf("retry scheduled: %s", failure)})
}

func (r *Runner) runKeeperJob(ctx context.Context, client *llm.Client, settings store.Settings, job store.NoteJob, snapshot keeperSnapshot) []string {
	ctx = llm.WithTrace(ctx, llm.Trace{NoteAttempt: job.Attempts})
	written, err := r.keepNotes(ctx, client, settings, snapshot.turn(settings))
	if snapshot.RemarkID != "" {
		if remark, ok := r.store.Entry(snapshot.RemarkID); ok {
			r.pointAtNotes(&remark, written)
		}
	}
	r.finishNoteJob(job, err)
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
		var snapshot keeperSnapshot
		if err := json.Unmarshal(job.Payload, &snapshot); err != nil {
			r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
			continue
		}
		_, exists := r.skills(ctx).Find(job.Persona)
		// An index pass was all that was left of a job from before memory
		// was kept in fields, and there is no index any more.
		drop := !exists || job.Stage == "index"
		if !drop && snapshot.Trigger.ID != "" {
			current, ok := r.store.Entry(snapshot.Trigger.ID)
			// A deletion or edit has its own turn. Replaying the old input
			// would put back information the user changed or removed.
			drop = !ok || current.Deleted || current.Text != snapshot.Trigger.Text
		}
		if drop {
			if err := r.store.DeleteNoteJob(job.ID); err != nil {
				r.emit("error", map[string]any{"where": "note retry", "message": err.Error()})
			}
			continue
		}
		client, settings := r.clientOf()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		r.runKeeperJob(ctx, client, settings, job, snapshot)
		cancel()
	}
}
