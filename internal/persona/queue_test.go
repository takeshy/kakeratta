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
	runner.stopMu.Lock()
	runner.acceptLocked(reaction{entryID: entry.ID})
	runner.stopMu.Unlock()
	runner.Halt()
	if got := pending(t, s); len(got) != 1 || got[0].Entry != entry.ID {
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

// A follower keeps what it is given for the owner and works none of it; the
// owner finds it when poked, as the change feed does.
func TestAFollowerKeepsReactionsForTheOwner(t *testing.T) {
	owner, s, _ := setup(t, answering, "gourmet")
	follower := New(s, owner.catalogue, owner.clientOf)
	follower.SetFollower(true)
	entry := post(t, s, "餃子もいい")
	follower.React(entry.ID, nil)
	follower.NoteForgotten("gourmet", "shops/a.md")
	follower.NoteForgotten("gourmet", "shops/b.md")
	if follower.started || len(follower.queue) != 0 {
		t.Fatal("the follower took the work itself")
	}
	if got := pending(t, s); len(got) != 3 {
		t.Fatalf("kept = %+v", got)
	}

	owner.Start()
	owner.Poke()
	owner.stopMu.Lock()
	var forgottenPaths []string
	for _, item := range owner.queue {
		if item.forgotten != nil {
			forgottenPaths = append(forgottenPaths, item.forgotten.paths...)
		}
	}
	owner.stopMu.Unlock()
	owner.Stop()
	if len(forgottenPaths) != 2 {
		t.Fatalf("the two deletions were not one wake: %v", forgottenPaths)
	}
	if len(remarks(t, s)) != 1 {
		t.Fatal("the owner did not answer the follower's post")
	}
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("finished reactions still kept: %+v", got)
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
