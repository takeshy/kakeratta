package persona

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
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
	entry   string
	deleted *deletedPost
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
		if d := item.Deleted; d != nil {
			next.deleted = &deletedPost{entryID: d.EntryID, label: d.Label, skill: d.Skill}
		}
		if i, ok := last[item.Lane]; ok {
			prev := &out[i]
			switch {
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

// countingSkills counts how often the persona definitions are read.
type countingSkills struct {
	*store.Store
	reads atomic.Int32
}

func (c *countingSkills) Skills(ctx context.Context) (map[string][]byte, error) {
	c.reads.Add(1)
	return c.Store.Skills(ctx)
}

// Everything one reaction does looks at one reading of the definitions: the
// router, each persona's turn, the names, the note keeper.
func TestAReactionReadsThePersonaDefinitionsOnce(t *testing.T) {
	runner, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "振り分け") {
			return `{"deliver":["gourmet","move"]}`
		}
		return `{"action":"speak","text":"いいね"}`
	}, "gourmet", "move")
	counting := &countingSkills{Store: s}
	runner.catalogue = NewCatalogue(counting)
	entry := post(t, s, "ラーメン食べたい")
	runner.React(entry.ID, nil)
	runner.Stop()
	if got := len(remarks(t, s)); got != 2 {
		t.Fatalf("remarks = %d", got)
	}
	if reads := counting.reads.Load(); reads != 1 {
		t.Fatalf("the definitions were read %d times for one reaction", reads)
	}
}

// A worker whose claim was lost to another does not delete the reaction the
// other is working, when it finds out at the end.
func TestAWorkerThatLostItsClaimLeavesTheReactionAlone(t *testing.T) {
	runner, s, _ := setup(t, answering, "gourmet")
	entry := post(t, s, "ラーメン食べたい")
	item := store.Reaction{
		ID: "r-taken", At: time.Now(), Lane: "thread:" + entry.ID, Entry: entry.ID,
		ClaimedBy: "another", ClaimUntil: time.Now().Add(time.Minute),
	}
	if err := s.SaveReaction(item); err != nil {
		t.Fatal(err)
	}
	runner.handle(item, nil)
	if got := pending(t, s); len(got) != 1 || got[0].ID != "r-taken" || got[0].ClaimedBy != "another" {
		t.Fatalf("the other worker's reaction was touched: %+v", got)
	}
}

// A due reaction that cannot set the next wake is not finished: it is tried
// again, rather than the workspace going quiet.
func TestADueThatCannotSetTheNextWakeIsTriedAgain(t *testing.T) {
	defer func(backoff time.Duration) { armBackoff = backoff }(armBackoff)
	armBackoff = time.Millisecond
	runner, s, _ := setup(t, answering, "gourmet")
	var calls atomic.Int32
	var down atomic.Bool
	down.Store(true)
	runner.SetAlarm(func(time.Time) error {
		calls.Add(1)
		if down.Load() {
			return errors.New("the queue is down")
		}
		return nil
	})
	if err := runner.due(context.Background(), time.Now()); err == nil || calls.Load() != 3 {
		t.Fatalf("due = %v after %d tries", err, calls.Load())
	}

	if err := s.CreateReaction(store.Reaction{ID: store.DueReaction, At: time.Now(), Lane: store.DueReaction, Due: true}); err != nil {
		t.Fatal(err)
	}
	item, rest, ok, err := s.ClaimReaction(context.Background(), runner.worker, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	runner.handle(item, rest)
	if got := pending(t, s); len(got) != 1 || got[0].ID != store.DueReaction {
		t.Fatalf("a due that set no next wake was finished: %+v", got)
	}

	down.Store(false)
	item.ClaimedBy = runner.worker
	runner.handle(item, nil)
	if got := pending(t, s); len(got) != 0 {
		t.Fatalf("a due that set the next wake was kept: %+v", got)
	}
}

// A keeper that lost its lock writes nothing more: its context has ended, and
// the memory is the new holder's to write.
func TestALostLockStopsMemoryWrites(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	lost, cancel := context.WithCancel(context.Background())
	cancel()
	skill, _ := r.catalogue.Find(context.Background(), "gourmet")
	m, changed, _ := planFacts(r.loadFacts(skill, time.UTC), []factOp{{Op: "add", Field: "Shop", Text: "店"}}, "e_1", time.Now(), time.UTC)
	if _, err := r.writeFacts(lost, skill.ID, m, changed, time.UTC); err == nil {
		t.Fatal("a lost lock still wrote a field")
	}
	trigger := timeline.Entry{ID: "e_1", Speaker: timeline.User, At: time.Now()}
	if err := r.appendRecord(lost, "gourmet", record{At: trigger.At, ID: "e_1", Summary: "x"}, trigger, time.UTC); err == nil {
		t.Fatal("a lost lock still appended a record")
	}
	if paths, _ := s.NotePaths("gourmet"); len(paths) != 0 {
		t.Fatalf("memory was written: %v", paths)
	}
}

// A due reaction out of attempts is dropped only once the next wake is set;
// until then it is kept, held back and with its attempts spent afresh, so the
// workspace is woken again when the queue is back.
func TestADueOutOfAttemptsIsNotDroppedWithoutANextWake(t *testing.T) {
	defer func(backoff time.Duration) { armBackoff = backoff }(armBackoff)
	armBackoff = time.Millisecond
	runner, s, _ := setup(t, answering, "gourmet")
	var down atomic.Bool
	down.Store(true)
	runner.SetAlarm(func(time.Time) error {
		if down.Load() {
			return errors.New("the queue is down")
		}
		return nil
	})
	claim := func() store.Reaction {
		item, _, ok, err := s.ClaimReaction(context.Background(), runner.worker, time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v %v", ok, err)
		}
		return item
	}
	if err := s.CreateReaction(store.Reaction{ID: store.DueReaction, At: time.Now(), Lane: store.DueReaction, Due: true, Attempts: maxReactionAttempts}); err != nil {
		t.Fatal(err)
	}
	item := claim() // Attempts is now over the limit.
	if item.Attempts <= maxReactionAttempts {
		t.Fatalf("attempts = %d", item.Attempts)
	}
	runner.handle(item, nil)
	got := pending(t, s)
	if len(got) != 1 || got[0].ClaimedBy != "" || got[0].Attempts != 0 || !got[0].NotBefore.After(time.Now().Add(retryDue-time.Minute)) {
		t.Fatalf("a due that could not set the next wake = %+v", got)
	}
	if _, _, ok, _ := s.ClaimReaction(context.Background(), "someone", time.Minute); ok {
		t.Fatal("the deferred reaction can be claimed at once")
	}

	// Once the queue is back, and the time has come, it is worked and goes.
	got[0].NotBefore = time.Time{}
	if err := s.SaveReaction(got[0]); err != nil {
		t.Fatal(err)
	}
	down.Store(false)
	runner.handle(claim(), nil)
	if left := pending(t, s); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}

	// Given up on with the next wake set, it simply goes.
	if err := s.CreateReaction(store.Reaction{ID: store.DueReaction, At: time.Now(), Lane: store.DueReaction, Due: true, Attempts: maxReactionAttempts + 1}); err != nil {
		t.Fatal(err)
	}
	runner.handle(claim(), nil)
	if left := pending(t, s); len(left) != 0 {
		t.Fatalf("a due given up on with the next wake set was kept: %+v", left)
	}
}

// failingRenewals is a workspace whose claims cannot be renewed.
type failingRenewals struct{ *store.Store }

func (failingRenewals) RenewReaction(context.Context, string, string, time.Duration) (bool, error) {
	return false, errors.New("firestore is down")
}

// Work cut short because its claim could not be renewed is not finished: it
// stays, held back briefly and unclaimed, to be taken up again once the
// storage is back - the answer it was to give is not lost with it.
func TestWorkCutShortByFailingRenewalsIsKeptForAnotherTry(t *testing.T) {
	defer func(ttl time.Duration) { claimTTL = ttl }(claimTTL)
	claimTTL = 150 * time.Millisecond
	block := make(chan struct{})
	runner, s, _ := setup(t, func(system, user string) string {
		<-block // The model does not answer.
		return `{"deliver":[]}`
	}, "gourmet")
	t.Cleanup(func() { close(block) })
	runner.store = failingRenewals{s}
	entry := post(t, s, "ラーメン食べたい")
	if err := s.SaveReaction(store.Reaction{ID: "r-cut", At: time.Now(), Lane: "thread:" + entry.ID, Entry: entry.ID}); err != nil {
		t.Fatal(err)
	}
	item, _, ok, err := s.ClaimReaction(context.Background(), runner.worker, claimTTL)
	if err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	done := make(chan struct{})
	go func() {
		runner.handle(item, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("work whose claim could not be renewed was not cut short")
	}
	got := pending(t, s)
	if len(got) != 1 || got[0].ID != "r-cut" || got[0].ClaimedBy != "" || got[0].Attempts != 0 ||
		!got[0].NotBefore.After(time.Now().Add(interruptedDelay-10*time.Second)) {
		t.Fatalf("a reaction cut short was not kept for another try: %+v", got)
	}
}

// An earlier turn can record its recipient after the edit has already routed.
// Persist the prepared phase so the assertion also covers restart delivery.
func TestEditedDeliveryRefreshesRecipientsAfterRouting(t *testing.T) {
	for _, decision := range []string{`{"deliver":[],"excluded":{"move":"not relevant"}}`, `{"deliver":["gourmet"]}`} {
		t.Run(decision, func(t *testing.T) {
			r, s, stub := setup(t, func(system, user string) string {
				if strings.Contains(system, "振り分け") {
					return decision
				}
				return `{"action":"silent","notes":""}`
			}, "move", "gourmet")
			entry := post(t, s, "今夜は自宅でワインを飲みたい")
			plan, record, err := r.prepareEntry(r.withSkills(context.Background()), entry.ID, nil, true)
			if err != nil || plan == nil || len(plan.Previous) != 0 {
				t.Fatalf("prepare = %+v, %v", plan, err)
			}
			if record != nil {
				if err := s.SaveRouting(entry.ID, *record); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RecordWake(entry.ID, "move"); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveReaction(store.Reaction{ID: "edit", At: time.Now(), Lane: "thread:" + entry.ID, Entry: entry.ID, Edited: true, Delivery: plan}); err != nil {
				t.Fatal(err)
			}
			r.Stop()
			if prompt := stub.promptFor("michi"); !strings.Contains(prompt, editedInstructions) || !strings.Contains(prompt, entry.Text) {
				t.Fatalf("late recipient missed the edit: %s", prompt)
			}
			if prompt := stub.promptFor("tabe"); strings.Contains(prompt, editedInstructions) {
				t.Fatal("a new recipient received edit instructions")
			}
			if got := pending(t, s); len(got) != 0 {
				t.Fatalf("unfinished delivery: %+v", got)
			}
		})
	}
}

type failingDeliveryWakes struct{ *store.Store }

func (s *failingDeliveryWakes) Woke(string) ([]string, error) {
	return nil, errors.New("wake history unavailable")
}

func TestEditedDeliveryRetainsReactionWhenRecipientsCannotBeRead(t *testing.T) {
	r, s, _ := setup(t, answering, "gourmet")
	entry := post(t, s, "編集後")
	r.store = &failingDeliveryWakes{Store: s}
	if err := s.SaveReaction(store.Reaction{
		ID: "edit", At: time.Now(), Lane: "thread:" + entry.ID,
		Entry: entry.ID, Edited: true,
		Delivery: &store.EntryDelivery{Text: entry.Text, SkillsJSON: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	r.Stop()
	items := pending(t, s)
	if len(items) != 1 || items[0].ClaimedBy != "" || !items[0].NotBefore.After(time.Now()) {
		t.Fatalf("failed delivery was not deferred: %+v", items)
	}
}

func TestRoutingResolvesLocationBeforeDelivery(t *testing.T) {
	for _, outcome := range []string{"resolved", "no match", "unavailable", "custom label", "no coordinates"} {
		t.Run(outcome, func(t *testing.T) {
			r, s, stub := setup(t, answering, "gourmet")
			entry := timeline.Entry{
				ID: "located-post", Root: "located-post", Kind: timeline.KindPost,
				Speaker: timeline.User, Text: "ラーメン食べたい", At: time.Now(),
				Place: &timeline.Place{Lat: 35, Lon: 139},
			}
			if outcome == "custom label" {
				entry.Place.Label = "自宅"
			} else if outcome == "no coordinates" {
				entry.Place = nil
			}
			if err := s.Append(entry); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			r.SetPlaceLookup(func(ctx context.Context, id string) error {
				calls.Add(1)
				if outcome == "unavailable" {
					return errors.New("geo offline")
				}
				if outcome == "no match" {
					return nil
				}
				resolved := *entry.Place
				resolved.Label = "横浜市"
				_, _, err := s.UpdatePlace(ctx, id, *entry.Place, resolved)
				return err
			})
			if err := s.SaveReaction(store.Reaction{ID: "route-place", Lane: "thread:" + entry.ID, Entry: entry.ID, Route: true}); err != nil {
				t.Fatal(err)
			}
			item, rest, ok, err := s.ClaimReaction(context.Background(), r.worker, claimTTL)
			if err != nil || !ok {
				t.Fatalf("claim: %v, %v", ok, err)
			}
			r.handle(item, rest)
			wantCalls := int32(1)
			if outcome == "custom label" || outcome == "no coordinates" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatalf("lookup must finish within routing: calls=%d, want %d", calls.Load(), wantCalls)
			}
			var retry bool
			for _, job := range pending(t, s) {
				retry = retry || job.Locate
			}
			if retry != (outcome == "unavailable") {
				t.Fatalf("background retry=%v for %s", retry, outcome)
			}
			r.Stop()
			saved, _ := s.Entry(entry.ID)
			if outcome == "resolved" && saved.Place.Label != "横浜市" {
				t.Fatal("location was not saved")
			}
			if prompt := stub.promptFor("tabe"); !strings.Contains(prompt, placeLine(saved.Place)) {
				t.Fatalf("recipient missed location %q: %s", placeLine(saved.Place), prompt)
			}
		})
	}
}

func TestLocationRetrySurvivesRestartAndStopsAfterThreeAttempts(t *testing.T) {
	first, s, _ := setup(t, answering, "gourmet")
	item := store.Reaction{ID: "place-test", Lane: "place-test", Entry: "post", Locate: true}
	if err := s.SaveReaction(item); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		r := New(s, first.catalogue, first.clientOf)
		calls := 0
		r.SetPlaceLookup(func(ctx context.Context, id string) error {
			calls++
			return errors.New("cold service")
		})
		r.Start()
		r.Stop()
		if calls != 1 {
			t.Fatalf("attempt %d calls=%d", attempt, calls)
		}
		jobs := pending(t, s)
		if attempt == 3 {
			if len(jobs) != 0 {
				t.Fatalf("exhausted job: %+v", jobs)
			}
			continue
		}
		if len(jobs) != 1 || jobs[0].Attempts != attempt || jobs[0].NotBefore.IsZero() {
			t.Fatalf("retry state: %+v", jobs)
		}
		jobs[0].NotBefore = time.Time{}
		if err := s.SaveReaction(jobs[0]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocationRetryCanSucceedOnAnotherRunner(t *testing.T) {
	first, s, _ := setup(t, answering, "gourmet")
	first.SetPlaceLookup(func(context.Context, string) error { return errors.New("cold") })
	if err := first.accept(store.Reaction{ID: "place-retry", Lane: "place-retry", Locate: true, Entry: "p"}); err != nil {
		t.Fatal(err)
	}
	first.Start()
	first.Stop()
	jobs := pending(t, s)
	if len(jobs) != 1 {
		t.Fatalf("%+v", jobs)
	}
	jobs[0].NotBefore = time.Time{}
	if err := s.SaveReaction(jobs[0]); err != nil {
		t.Fatal(err)
	}
	next := New(s, first.catalogue, first.clientOf)
	calls := 0
	next.SetPlaceLookup(func(context.Context, string) error { calls++; return nil })
	next.Start()
	next.Stop()
	if calls != 1 || len(pending(t, s)) != 0 {
		t.Fatal("retry not completed")
	}
}
