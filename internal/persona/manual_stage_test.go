package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type interruptedManualStore struct {
	store.Workspace
	cancelAfterStart context.CancelFunc
	failCompletion   bool
	failAppend       bool
	ambiguousAppend  bool
}

func (s *interruptedManualStore) UpdateRecord(ctx context.Context, collection, id string, expire time.Time, change store.RecordChange) error {
	err := s.Workspace.UpdateRecord(ctx, collection, id, expire, func(raw []byte, exists bool) ([]byte, error) {
		next, err := change(raw, exists)
		if err != nil {
			return nil, err
		}
		if collection == store.RecordCheckIns {
			var state manualStageRecord
			if err := json.Unmarshal(next, &state); err != nil {
				return nil, err
			}
			if state.Done && s.failCompletion {
				s.failCompletion = false
				return nil, errors.New("checkpoint unavailable")
			}
		}
		return next, nil
	})
	if err == nil && collection == store.RecordCheckIns && s.cancelAfterStart != nil {
		cancel := s.cancelAfterStart
		s.cancelAfterStart = nil
		cancel()
	}
	return err
}
func (s *interruptedManualStore) Append(entry timeline.Entry) error {
	if strings.HasPrefix(entry.ID, "e_manual_") {
		if s.failAppend {
			s.failAppend = false
			return errors.New("append unavailable")
		}
		if s.ambiguousAppend {
			s.ambiguousAppend = false
			if err := s.Workspace.Append(entry); err != nil {
				return err
			}
			return errors.New("append response lost")
		}
	}
	return s.Workspace.Append(entry)
}

func TestManualStageResumesInterruptedWorkWithoutDuplicatePosts(t *testing.T) {
	for _, period := range []string{"daily", "monthly"} {
		for _, boundary := range []string{"started", "before append", "after append", "checkpoint"} {
			t.Run(period+"/"+boundary, func(t *testing.T) {
				calls := 0
				active := []string{}
				if period == "daily" {
					active = []string{"routine"}
				}
				r, s, _ := setup(t, func(system, user string) string {
					if strings.Contains(system, "今月のまとめ") {
						calls++
						return "### Month\n- A walk"
					}
					if strings.Contains(system, "Daily check-in") {
						calls++
						return `{"action":"speak","text":"A useful connection"}`
					}
					return `{"action":"silent"}`
				}, active...)
				now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
				appendAt(t, s, "A walk", now.Add(-time.Hour))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				faulty := &interruptedManualStore{Workspace: s}
				switch boundary {
				case "started":
					faulty.cancelAfterStart = cancel
				case "before append":
					faulty.failAppend = true
				case "after append":
					faulty.ambiguousAppend = true
				case "checkpoint":
					faulty.failCompletion = true
				}
				r.store = faulty
				if err := r.checkInAt(ctx, now, period, now); err == nil {
					t.Fatal("interruption was not reported")
				}
				r.store = s
				if err := r.checkInAt(context.Background(), now.Add(time.Minute), period, now); err != nil {
					t.Fatal(err)
				}
				expectedCalls := 1
				if boundary == "before append" {
					expectedCalls = 2
				}
				if calls != expectedCalls {
					t.Fatalf("provider calls=%d want=%d", calls, expectedCalls)
				}
				count := func() int {
					entries, err := s.Timeline()
					if err != nil {
						t.Fatal(err)
					}
					n := 0
					for _, e := range entries {
						if strings.HasPrefix(e.ID, "e_manual_") {
							n++
						}
					}
					return n
				}
				if count() != 1 {
					t.Fatalf("posts after recovery=%d want=1", count())
				}
				if err := r.checkInAt(context.Background(), now.Add(2*time.Minute), period, now); err != nil {
					t.Fatal(err)
				}
				if count() != 1 || calls != expectedCalls {
					t.Fatal("completed stage repeated")
				}
				if err := r.checkInAt(context.Background(), now.Add(3*time.Minute), period, now.Add(3*time.Minute)); err != nil {
					t.Fatal(err)
				}
				if count() != 2 {
					t.Fatalf("new request posts=%d want=2", count())
				}
			})
		}
	}
}

func TestManualStageRetriesProviderFailure(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "routine")
	now := time.Now()
	f.fail = func(system string) bool { return strings.Contains(system, "Daily check-in") }
	if err := r.checkInAt(context.Background(), now, "daily", now); err == nil {
		t.Fatal("provider failure swallowed")
	}
	f.fail = nil
	if err := r.checkInAt(context.Background(), now.Add(time.Minute), "daily", now); err != nil {
		t.Fatal(err)
	}
	records, err := s.Records(context.Background(), store.RecordCheckIns)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("checkpoints=%d", len(records))
	}
	for _, raw := range records {
		var state manualStageRecord
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		if !state.Done {
			t.Fatal("successful silence was not checkpointed")
		}
	}
}

func TestManualDailyIncludesAllUncoveredPosts(t *testing.T) {
	var handed string
	r, s, _ := setup(t, func(system, user string) string {
		if strings.Contains(system, "Daily check-in") {
			handed = user
		}
		return `{"action":"silent"}`
	}, "routine")
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for i := range 45 {
		appendAt(t, s, fmt.Sprintf("manual-message-%02d", i), now.Add(-time.Hour+time.Duration(i)*time.Minute))
	}
	if err := r.checkInAt(context.Background(), now, "daily", now); err != nil {
		t.Fatal(err)
	}
	for i := range 45 {
		if !strings.Contains(handed, fmt.Sprintf("manual-message-%02d", i)) {
			t.Fatalf("post %d omitted from manual check-in", i)
		}
	}
	digest, _ := r.checkInDigest(context.Background(), now.Add(-24*time.Hour), now, time.UTC)
	if strings.Contains(digest, "manual-message-00") || !strings.Contains(digest, "only the newest 30 of 45") {
		t.Fatal("automatic fallback limit changed")
	}
}

func TestManualExternalHandoffSurvivesCheckpointFailure(t *testing.T) {
	r, s, _ := setup(t, func(system, user string) string { return `{"action":"silent"}` }, "gourmet")
	settings := s.ConfiguredSettings()
	settings.Planners = map[string]string{"gourmet": store.PlannerExternal}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	r.AgentSeen(time.Now())
	now := time.Now()
	r.store = &interruptedManualStore{Workspace: s, failCompletion: true}
	if err := r.checkInAt(context.Background(), now, "daily", now); err == nil {
		t.Fatal("checkpoint failure swallowed")
	}
	r.store = s
	ask := openAskOf(t, r)
	if _, _, err := r.ClaimAsk(ask.ID, "same-agent"); err != nil {
		t.Fatal(err)
	}
	if err := r.checkInAt(context.Background(), now.Add(time.Minute), "daily", now); err != nil {
		t.Fatal(err)
	}
	asks := r.openAsks(context.Background())
	if len(asks) != 1 || asks[0].Ask.ID != ask.ID || asks[0].Ask.ClaimID != "same-agent" {
		t.Fatalf("external ask duplicated or reset: %+v", asks)
	}
	if asks[0].Turn.ManualOutput == "" {
		t.Fatal("external output identity was not persisted")
	}
	// The resumed external answer uses the same deterministic output ID.
	record := asks[0]
	if _, err := r.SubmitAskAnswer(context.Background(), ask.ID, "same-agent", &Answer{Text: "External connection", Sources: []string{}}, nil); err != nil {
		t.Fatal(err)
	}
	r.Stop()
	if entry, ok := s.Entry(record.Turn.ManualOutput); !ok || entry.Text != "External connection" {
		t.Fatalf("external output identity lost: %+v %v", entry, ok)
	}
}

func TestManualQueueRetainsFailedWorkForRetry(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"action":"speak","text":"Recovered connection"}` }, "routine")
	f.fail = func(system string) bool { return strings.Contains(system, "Daily check-in") }
	if err := r.RequestCheckIn("daily"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	item, rest, ok, err := s.ClaimReaction(ctx, r.worker, claimTTL)
	if err != nil || !ok {
		t.Fatalf("claim=%v %v", ok, err)
	}
	r.handle(item, rest)
	items, err := s.Reactions(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("failed work lost: %+v %v", items, err)
	}
	pending := items[0]
	if pending.Attempts != 1 || pending.ClaimedBy != "" || !pending.NotBefore.After(time.Now()) {
		t.Fatalf("retry not deferred: %+v", pending)
	}
	// Advance the test's retry immediately without waiting for the production backoff.
	pending.NotBefore = time.Time{}
	if err := s.SaveReaction(pending); err != nil {
		t.Fatal(err)
	}
	f.fail = nil
	item, rest, ok, err = s.ClaimReaction(ctx, r.worker, claimTTL)
	if err != nil || !ok || item.Attempts != 2 {
		t.Fatalf("retry claim=%+v %v %v", item, ok, err)
	}
	r.handle(item, rest)
	items, err = s.Reactions(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("successful retry not finished: %+v %v", items, err)
	}
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "Recovered connection" {
		t.Fatalf("recovered output=%+v", entries)
	}
}

// A manual check-in that keeps failing is given up after maxReactionAttempts
// rather than retried forever.
func TestManualQueueGivesUpPersistentFailure(t *testing.T) {
	r, s, f := setup(t, func(system, user string) string { return `{"action":"speak","text":"never"}` }, "routine")
	f.fail = func(system string) bool { return strings.Contains(system, "Daily check-in") }
	if err := r.RequestCheckIn("daily"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runs := 0
	for ; runs <= maxReactionAttempts+1; runs++ {
		items, err := s.Reactions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		items[0].NotBefore = time.Time{}
		if err := s.SaveReaction(items[0]); err != nil {
			t.Fatal(err)
		}
		item, rest, ok, err := s.ClaimReaction(ctx, r.worker, claimTTL)
		if err != nil || !ok {
			t.Fatalf("claim=%v %v", ok, err)
		}
		r.handle(item, rest)
	}
	if runs != maxReactionAttempts {
		t.Fatalf("runs=%d; want %d", runs, maxReactionAttempts)
	}
}
