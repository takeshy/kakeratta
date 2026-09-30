package persona

import (
	"context"
	"strings"
	"testing"

	"github.com/takeshy/kakeratta/internal/store"
)

func answering(system, user string) string {
	if strings.Contains(system, "振り分け") {
		return `{"deliver":["gourmet"]}`
	}
	return `{"action":"speak","text":"いいね"}`
}

func pending(t *testing.T, s *store.Store) []store.Reaction {
	t.Helper()
	items, err := s.Reactions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// A post accepted before a shutdown is answered after the restart: the
// reaction is kept until its work is done, not only held in memory.
func TestAHaltedReactionIsTakenUpOnRestart(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	entry := post(t, s, "ラーメン食べたい")
	runner.React(entry.ID, nil)
	runner.Halt()
	if got := pending(t, s); len(got) != 1 || got[0].Entry != entry.ID || got[0].Lane != "thread:"+entry.ID {
		t.Fatalf("kept = %+v", got)
	}
	if len(remarks(t, s)) != 0 {
		t.Fatal("a halted runner answered")
	}

	restarted := New(s, runner.catalogue, runner.clientOf)
	restarted.Start()
	restarted.Stop()
	if len(remarks(t, s)) != 1 {
		t.Fatal("the kept reaction was not answered after the restart")
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("finished reaction still kept: %+v", got)
	}
}

// Two runners over one workspace - two processes serving the same user - share
// its queue: every reaction is worked once, by whichever claims it.
func TestTwoRunnersShareOneQueue(t *testing.T) {
	first, s, _ := setup(t, answering, "gourmet")
	second := New(s, first.catalogue, first.clientOf)
	var posts []string
	for _, text := range []string{"ラーメン", "餃子", "チャーハン", "天津飯", "麻婆豆腐", "回鍋肉"} {
		entry := post(t, s, text)
		posts = append(posts, entry.ID)
		if len(posts)%2 == 0 {
			first.React(entry.ID, nil)
		} else {
			second.React(entry.ID, nil)
		}
	}
	first.Start()
	second.Start()
	done := make(chan struct{})
	go func() { first.Stop(); close(done) }()
	second.Stop()
	<-done
	answered := map[string]int{}
	for _, remark := range remarks(t, s) {
		answered[remark.Root]++
	}
	for _, id := range posts {
		if answered[id] != 1 {
			t.Fatalf("post %s answered %d times: %v", id, answered[id], answered)
		}
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("left = %+v", got)
	}
}

// A reaction that never finishes - one that took its process down each
// time - is given up after a few restarts rather than tried forever.
func TestAReactionThatKeepsFailingIsDropped(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	entry := post(t, s, "もう一度")
	if err := s.SaveReaction(store.Reaction{ID: "r_stuck", Entry: entry.ID, Attempts: maxReactionAttempts}); err != nil {
		t.Fatal(err)
	}
	var errors []string
	runner.OnEvent(func(name string, data any) {
		if name == "error" {
			errors = append(errors, name)
		}
	})
	runner.Start()
	runner.Stop()
	if len(remarks(t, s)) != 0 || len(pending(t, s)) != 0 || len(errors) == 0 {
		t.Fatalf("remarks=%d pending=%d errors=%v", len(remarks(t, s)), len(pending(t, s)), errors)
	}
}

// wake is a queued reaction as a worker takes it up: folded with those after
// it in its lane that ask the same thing.
type wake struct {
	entry     string
	forgotten *forgotten
	deleted   *deletedPost
}

func wakes(t *testing.T, r *Runner) []wake {
	t.Helper()
	items, err := r.store.Reactions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []wake
	last := map[string]int{}
	for _, item := range items {
		next := wake{entry: item.Entry}
		if f := item.Forgotten; f != nil {
			next.forgotten = &forgotten{skill: f.Skill, paths: append([]string(nil), f.Paths...), expired: f.Expired}
		}
		if d := item.Deleted; d != nil {
			next.deleted = &deletedPost{entryID: d.EntryID, label: d.Label, skill: d.Skill, notes: d.Notes, removed: d.Removed}
		}
		if i, ok := last[item.Lane]; ok {
			prev := &out[i]
			switch {
			case prev.forgotten != nil && next.forgotten != nil && prev.forgotten.skill == next.forgotten.skill && prev.forgotten.expired == next.forgotten.expired:
				prev.forgotten.paths = append(prev.forgotten.paths, next.forgotten.paths...)
				continue
			case prev.deleted != nil && next.deleted != nil && prev.deleted.skill == next.deleted.skill && prev.deleted.entryID == next.deleted.entryID:
				continue
			}
		}
		out = append(out, next)
		last[item.Lane] = len(out) - 1
	}
	return out
}

func deletedWakes(t *testing.T, r *Runner) []deletedPost {
	t.Helper()
	var out []deletedPost
	for _, w := range wakes(t, r) {
		if w.deleted != nil {
			out = append(out, *w.deleted)
		}
	}
	return out
}
