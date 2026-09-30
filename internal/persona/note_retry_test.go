package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

func retryReply(fail *bool) func(string, string, map[string]bool) string {
	return func(system, user string, offered map[string]bool) string {
		switch {
		case offered["finish_read"]:
			return "read_done"
		case offered["submit_answer"]:
			return `{"text":"ラーメンの話ですね。","sources":[]}`
		case offered["submit_schedule"]:
			return `{"operations":[],"consistent":true,"reason":"約束なし"}`
		}
		if *fail {
			return `{"operations":[{"type":"write","path":"../outside.md","text":"bad"}]}`
		}
		return `{"operations":[{"type":"write","path":"tastes/ramen.md","text":"---\ntype: Preference\ntitle: ラーメン\n---\nラーメンについて聞かれた。短く答えた。","section":"好み"}]}`
	}
}

func TestFailedNoteJobSurvivesRestartAndRetries(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	fail := true
	f.phaseReply = retryReply(&fail)
	r.react(post(t, s, "ラーメンについて教えて").ID)
	if got := remarks(t, s); len(got) != 1 {
		t.Fatalf("answer was lost: %+v", got)
	}
	jobs, err := s.NoteJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Stage != "notes" {
		t.Fatalf("jobs = %d, %v", len(jobs), err)
	}
	var snapshot keeperSnapshot
	if err := json.Unmarshal(jobs[0].Payload, &snapshot); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if snapshot.Trigger.ID == "" {
		t.Fatalf("missing trigger: %+v", snapshot.Trigger)
	}
	// The first process is gone before the second one starts. Left running, it
	// would take up the same due job from its own copy of the queue.
	r.Stop()
	fail = false
	jobs[0].NextAttempt = time.Time{}
	if err := s.SaveNoteJob(jobs[0]); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	// Today's check-in has run, as far as this test is concerned: whether it is
	// in its window depends on the hour and on the temporary directory, and its
	// own note phase would fail on the note this test is about.
	if _, _, err := reopened.ClaimDailyCheckInAt("gourmet", time.Now().In(reopened.Settings().Location()).Format("2006-01-02"), time.Now()); err != nil {
		t.Fatal(err)
	}
	restarted := New(reopened, r.catalogue, r.clientOf)
	// What is due is retried when the workspace wakes.
	restarted.Due()
	restarted.Stop()
	if jobs, err := s.NoteJobs(); err != nil || len(jobs) != 0 {
		if len(jobs) > 0 {
			t.Fatalf("jobs after retry = %d, stage=%s, attempts=%d, error=%s", len(jobs), jobs[0].Stage, jobs[0].Attempts, jobs[0].LastError)
		}
		t.Fatalf("jobs after retry = %d, %v", len(jobs), err)
	}
	if note, ok := s.Note("gourmet", "tastes/ramen.md"); !ok || !strings.Contains(note.Text, "聞かれた") {
		t.Fatalf("retried note = %+v", note)
	}
	if index, ok := s.Note("gourmet", "index.md"); !ok || !strings.Contains(index.Text, "/tastes/ramen.md") {
		t.Fatalf("index after retry = %+v", index)
	}
}

func TestRetryDoesNotRestoreDeletedPost(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	fail := true
	f.phaseReply = retryReply(&fail)
	entry := post(t, s, "ラーメンについて教えて")
	r.react(entry.ID)
	entry.Deleted = true
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	fail = false
	r.retryNoteJobs(context.Background(), time.Now().Add(time.Hour))
	if jobs, err := s.NoteJobs(); err != nil || len(jobs) != 0 {
		t.Fatalf("pending jobs after deletion = %d, %v", len(jobs), err)
	}
	if _, ok := s.Note("gourmet", "tastes/ramen.md"); ok {
		t.Fatal("a deleted post was written back into memory")
	}
}

type failingIndexWorkspace struct {
	store.Workspace
	fail bool
}

func (w *failingIndexWorkspace) WriteNote(persona, path, text string, sources ...store.NoteSource) error {
	if w.fail && path == "index.md" {
		return errIndexUnavailable
	}
	return w.Workspace.WriteNote(persona, path, text, sources...)
}

var errIndexUnavailable = &indexUnavailable{}

type indexUnavailable struct{}

func (*indexUnavailable) Error() string { return "index unavailable" }

func TestIndexWriteFailureRetriesWithoutRewritingNote(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	failNotes := false
	f.phaseReply = retryReply(&failNotes)
	w := &failingIndexWorkspace{Workspace: s, fail: true}
	r = New(w, r.catalogue, r.clientOf)
	r.react(post(t, s, "ラーメンについて教えて").ID)
	jobs, err := s.NoteJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Stage != "index" {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
	w.fail = false
	r.retryNoteJobs(context.Background(), time.Now().Add(time.Hour))
	if jobs, err := s.NoteJobs(); err != nil || len(jobs) != 0 {
		t.Fatalf("jobs after index retry = %+v, %v", jobs, err)
	}
	if index, ok := s.Note("gourmet", "index.md"); !ok || !strings.Contains(index.Text, "/tastes/ramen.md") {
		t.Fatalf("repaired index = %+v", index)
	}
}
