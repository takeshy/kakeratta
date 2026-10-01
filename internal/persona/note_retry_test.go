package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// retryKeep fails the keeper while fail is set, and records the kakera once
// it is not.
func retryKeep(fail *bool) func(string, string) string {
	return func(system, user string) string {
		if *fail {
			return `{"summary":"","tags":[],"facts":[]}`
		}
		return `{"summary":"ラーメンについて聞いた","tags":["ラーメン"],"facts":[{"op":"add","field":"Preference","id":"","ids":[],"text":"ラーメンについて聞かれた。"}]}`
	}
}

func TestFailedNoteJobSurvivesRestartAndRetries(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	fail := true
	f.phaseReply = answeringReply("ラーメンの話ですね。")
	f.keep = retryKeep(&fail)
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
	if note, ok := s.Note("gourmet", "Preference.md"); !ok || !strings.Contains(note.Text, "聞かれた") {
		t.Fatalf("retried field = %+v", note)
	}
	if found, _ := restarted.findRecords("gourmet", "ラーメン", "", "", 10, time.UTC); len(found) != 1 {
		t.Fatalf("records after retry = %+v", found)
	}
}

func TestRetryDoesNotRestoreDeletedPost(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"deliver":["gourmet"]}` }, "gourmet")
	fail := true
	f.phaseReply = answeringReply("ラーメンの話ですね。")
	f.keep = retryKeep(&fail)
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
	if paths, _ := s.NotePaths("gourmet"); len(paths) != 0 {
		t.Fatalf("a deleted post was written back into memory: %v", paths)
	}
}
