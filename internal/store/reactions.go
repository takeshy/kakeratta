package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reaction is one piece of persona work accepted and not yet finished: a post
// or an edit to react to, a delivery a person approved, an external agent's
// answer to carry on with, deleted notes or posts a persona is to be told
// about, the minute's periodic checks, search indexing. It is kept until the
// work is done, so a restart or a crash carries on with it.
//
// Reactions in one lane run one at a time, in order: the replies of one
// conversation, the tidy-ups of one persona. Different lanes run side by
// side, on any instance - whoever claims one first. A claim lapses unless
// its holder renews it, so work a dead process held is taken up again.
type Reaction struct {
	ID       string    `json:"id" firestore:"id"`
	At       time.Time `json:"at" firestore:"at"`
	Lane     string    `json:"lane" firestore:"lane"`
	Attempts int       `json:"attempts" firestore:"attempts"`
	Dispatch string    `json:"dispatch,omitempty" firestore:"dispatch,omitempty"`
	// NotBefore holds a reaction back until then, as a retry after a failure.
	NotBefore time.Time `json:"notBefore,omitempty" firestore:"notBefore,omitempty"`
	// ClaimedBy and ClaimUntil are the worker working it, while it renews.
	ClaimedBy  string    `json:"claimedBy,omitempty" firestore:"claimedBy,omitempty"`
	ClaimUntil time.Time `json:"claimUntil,omitempty" firestore:"claimUntil,omitempty"`

	// Route is the initial routing phase. It may run ahead of its conversation,
	// but still blocks later deliveries in that lane until it is prepared.
	Route     bool           `json:"route,omitempty" firestore:"route,omitempty"`
	Delivery  *EntryDelivery `json:"delivery,omitempty" firestore:"delivery,omitempty"`
	EntryText *string        `json:"entryText,omitempty" firestore:"entryText,omitempty"`

	Entry  string          `json:"entry,omitempty" firestore:"entry,omitempty"`
	Edited bool            `json:"edited,omitempty" firestore:"edited,omitempty"`
	Place  *timeline.Place `json:"place,omitempty" firestore:"place,omitempty"`
	// Deliver is a persona the post is to reach now, as a person approved in
	// a routing check.
	Deliver string `json:"deliver,omitempty" firestore:"deliver,omitempty"`
	// Ask is an external agent's ask that ended, answered or not, whose turn
	// is to be carried on.
	Ask string `json:"ask,omitempty" firestore:"ask,omitempty"`
	// Due runs whatever has come due - asks past their waits, note retries,
	// expired records, check-ins, schedules - and arms the next time something
	// will; Index brings the search index up to date.
	// CheckIn runs a manually requested daily or monthly check-in.
	CheckIn string `json:"checkIn,omitempty" firestore:"checkIn,omitempty"`
	Due     bool   `json:"due,omitempty" firestore:"due,omitempty"`
	Index   bool   `json:"index,omitempty" firestore:"index,omitempty"`
	Locate  bool   `json:"locate,omitempty" firestore:"locate,omitempty"`
	// IndexAgain records changes received while this pass holds its claim.
	IndexAgain bool `json:"indexAgain,omitempty" firestore:"indexAgain,omitempty"`

	Forgotten *ForgottenReaction `json:"forgotten,omitempty" firestore:"forgotten,omitempty"`
	Deleted   *DeletedReaction   `json:"deleted,omitempty" firestore:"deleted,omitempty"`
}

// EntryDelivery is the durable result of routing, consumed without rerouting.
// SkillsJSON keeps the definition snapshot used by the router for the turns.
type EntryDelivery struct {
	Text         string            `json:"text" firestore:"text"`
	Active       []string          `json:"active,omitempty" firestore:"active,omitempty"`
	Targets      []string          `json:"targets" firestore:"targets"`
	Named        []string          `json:"named,omitempty" firestore:"named,omitempty"`
	Previous     []string          `json:"previous,omitempty" firestore:"previous,omitempty"`
	ReplySpeaker string            `json:"replySpeaker,omitempty" firestore:"replySpeaker,omitempty"`
	Asked        string            `json:"asked,omitempty" firestore:"asked,omitempty"`
	Heard        map[string]string `json:"heard,omitempty" firestore:"heard,omitempty"`
	SkillsJSON   string            `json:"skillsJSON" firestore:"skillsJSON"`
}

// ForgottenReaction is notes a persona was to be told were deleted, queued
// before memory was kept in fields. One still queued is dropped unrun.
type ForgottenReaction struct {
	Skill   string   `json:"skill" firestore:"skill"`
	Paths   []string `json:"paths" firestore:"paths"`
	Expired bool     `json:"expired,omitempty" firestore:"expired,omitempty"`
}

// DeletedReaction is a deleted post a persona had cited.
type DeletedReaction struct {
	EntryID string   `json:"entryId" firestore:"entryId"`
	Label   string   `json:"label" firestore:"label"`
	Skill   string   `json:"skill" firestore:"skill"`
	Notes   []string `json:"notes,omitempty" firestore:"notes,omitempty"`
	Removed []string `json:"removed,omitempty" firestore:"removed,omitempty"`
}

// Fixed IDs of the reactions a workspace has at most one of waiting.
const (
	DueReaction   = "due"
	IndexReaction = "index"
)

const reactionsDir = "reactions"

func sortReactions(items []Reaction) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		return items[i].ID < items[j].ID
	})
}

func laneOf(item Reaction) string {
	if item.Lane != "" {
		return item.Lane
	}
	return "reaction:" + item.ID
}

// nextClaim picks the reaction to work next: the first in its lane, not held
// by a live claim, not held back. The rest of its lane, in order, comes with
// it; a worker may fold those of the same kind into one piece of work.
func nextClaim(items []Reaction, now time.Time) (int, []Reaction, bool) {
	sortReactions(items)
	seen := map[string]bool{}
	for i, item := range items {
		lane := laneOf(item)
		if seen[lane] && !item.Route {
			continue
		}
		seen[lane] = true
		if now.Before(item.ClaimUntil) || now.Before(item.NotBefore) {
			continue
		}
		var rest []Reaction
		for _, other := range items[i+1:] {
			if laneOf(other) == lane {
				rest = append(rest, other)
			}
		}
		return i, rest, true
	}
	return 0, nil, false
}

// ErrReactionExists is a reaction created under an ID already waiting.
var ErrReactionExists = errors.New("reaction already waiting")

// SaveReaction keeps a reaction, replacing one with the same ID.
func (s *Store) SaveReaction(item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.path(reactionsDir, item.ID+".json"), item)
}

// CreateReaction keeps a reaction unless one with its ID is waiting already,
// which it reports as ErrReactionExists. Index requests instead mark a
// follow-up pass without releasing or replacing the current claim.
func (s *Store) CreateReaction(item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(reactionsDir, item.ID+".json")
	var existing Reaction
	if err := readJSON(path, &existing); err == nil {
		if item.Index && existing.Index {
			existing.IndexAgain = true
			return writeJSON(path, existing)
		}
		return ErrReactionExists
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeJSON(path, item)
}

func (s *Store) reactionsLocked() ([]Reaction, error) {
	paths, err := filepath.Glob(s.path(reactionsDir, "*.json"))
	if err != nil {
		return nil, err
	}
	items := make([]Reaction, 0, len(paths))
	for _, path := range paths {
		var item Reaction
		if err := readJSON(path, &item); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}
	sortReactions(items)
	return items, nil
}

// Reactions lists the unfinished reactions, oldest first.
func (s *Store) Reactions(context.Context) ([]Reaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reactionsLocked()
}

// ClaimReaction takes the next reaction for worker until ttl from now, with
// the rest of its lane. It reports false when there is nothing to take.
func (s *Store) ClaimReaction(_ context.Context, worker string, ttl time.Duration) (Reaction, []Reaction, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closingLocked() {
		return Reaction{}, nil, false, nil
	}
	items, err := s.reactionsLocked()
	if err != nil {
		return Reaction{}, nil, false, err
	}
	now := time.Now()
	i, rest, ok := nextClaim(items, now)
	if !ok {
		return Reaction{}, nil, false, nil
	}
	item := items[i]
	item.ClaimedBy, item.ClaimUntil = worker, now.Add(ttl)
	item.Attempts++
	item.IndexAgain = false
	if err := writeJSON(s.path(reactionsDir, item.ID+".json"), item); err != nil {
		return Reaction{}, nil, false, err
	}
	return item, rest, true, nil
}

// RenewReaction extends worker's claim. It reports false when the claim is
// no longer worker's, or the workspace is being deleted.
func (s *Store) RenewReaction(_ context.Context, id, worker string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(reactionsDir, id+".json")
	var item Reaction
	if err := readJSON(path, &item); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if item.ClaimedBy != worker || s.closingLocked() {
		return false, nil
	}
	item.ClaimUntil = time.Now().Add(ttl)
	return true, writeJSON(path, item)
}

// ReleaseReaction gives worker's claim up, so the reaction is taken up again
// at once rather than when the claim lapses.
func (s *Store) ReleaseReaction(_ context.Context, id, worker string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(reactionsDir, id+".json")
	var item Reaction
	if err := readJSON(path, &item); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if item.ClaimedBy != worker {
		return nil
	}
	item.ClaimedBy, item.ClaimUntil = "", time.Time{}
	return writeJSON(path, item)
}

// FinishReaction forgets a reaction whose work is done, together with the
// ones behind it that were folded into it. Only the worker whose claim it is
// may: if the claim lapsed and another worker took the reaction over, the
// first finding out at the end must not delete what the second is working.
// It reports whether the reaction was finished.
func (s *Store) FinishReaction(_ context.Context, id, worker string, also ...string) (bool, error) {
	for _, name := range append([]string{id}, also...) {
		if !safeName(name) {
			return false, errors.New("reaction needs an id")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var head Reaction
	if err := readJSON(s.path(reactionsDir, id+".json"), &head); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if head.ClaimedBy != worker {
		return false, nil
	}
	if head.Index && head.IndexAgain {
		head.ClaimedBy, head.ClaimUntil, head.Attempts, head.NotBefore = "", time.Time{}, 0, time.Time{}
		return true, writeJSON(s.path(reactionsDir, id+".json"), head)
	}
	for _, name := range append(append([]string(nil), also...), id) {
		if err := os.Remove(s.path(reactionsDir, name+".json")); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}

// DeferReaction gives worker's claim up and holds the reaction back until
// notBefore, with its attempts spent afresh. It is for work that cannot be
// done now and must not be dropped: a reaction given up on after its attempts
// would otherwise be lost with whatever it was to set going. It reports
// whether the reaction was worker's to defer. Location jobs retain their attempts
// so a permanently unavailable geocoder stops after the configured retry limit.
func (s *Store) DeferReaction(_ context.Context, id, worker string, notBefore time.Time) (bool, error) {
	if !safeName(id) {
		return false, errors.New("reaction needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(reactionsDir, id+".json")
	var item Reaction
	if err := readJSON(path, &item); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if item.ClaimedBy != worker {
		return false, nil
	}
	item.ClaimedBy, item.ClaimUntil, item.NotBefore = "", time.Time{}, notBefore
	if !item.Locate && item.CheckIn == "" {
		item.Attempts = 0
	}
	return true, writeJSON(path, item)
}

func reactionsOf(client *firestore.Client, key string) *firestore.CollectionRef {
	return client.Collection("users").Doc(key).Collection("reactions")
}

func (w *FirestoreWorkspace) reactions() *firestore.CollectionRef {
	return reactionsOf(w.client, w.user.ID)
}

func (w *FirestoreWorkspace) SaveReaction(item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	defer w.flushDispatch()
	return w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		if err := w.queueDispatch(tx, &item); err != nil {
			return err
		}
		return tx.Set(w.reactions().Doc(item.ID), item)
	})
}

func (w *FirestoreWorkspace) CreateReaction(item Reaction) error {
	defer w.flushDispatch()
	return w.createReaction(context.Background(), item)
}

func (w *FirestoreWorkspace) createReaction(ctx context.Context, item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	client := w.client
	ref := w.reactions().Doc(item.ID)
	if item.Index {
		return client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			doc, err := tx.Get(ref)
			if status.Code(err) == codes.NotFound {
				if err := w.queueDispatch(tx, &item); err != nil {
					return err
				}
				return tx.Create(ref, item)
			}
			if err != nil {
				return err
			}
			var existing Reaction
			if err := doc.DataTo(&existing); err != nil {
				return err
			}
			if !existing.Index {
				return ErrReactionExists
			}
			return tx.Update(ref, []firestore.Update{{Path: "indexAgain", Value: true}})
		})
	}
	err := client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := w.queueDispatch(tx, &item); err != nil {
			return err
		}
		return tx.Create(ref, item)
	})
	if status.Code(err) == codes.AlreadyExists {
		return ErrReactionExists
	}
	return err
}

func readReactions(docs []*firestore.DocumentSnapshot) ([]Reaction, error) {
	items := make([]Reaction, 0, len(docs))
	for _, doc := range docs {
		var item Reaction
		if err := doc.DataTo(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sortReactions(items)
	return items, nil
}

func (w *FirestoreWorkspace) Reactions(ctx context.Context) ([]Reaction, error) {
	docs, err := w.reactions().Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	return readReactions(docs)
}

// ClaimReaction finds the next reaction with a plain read and claims it with
// a transaction on that one document. A transaction querying the whole queue
// would lock it against the posts being queued meanwhile. The transaction
// checks the claim is still free; losing the race moves on to the next
// candidate. A reaction queued later sorts after those already there, so a
// lane's head from the read is still its head unless it was finished, which
// the transaction sees as the document being gone.
func (w *FirestoreWorkspace) ClaimReaction(ctx context.Context, worker string, ttl time.Duration) (Reaction, []Reaction, bool, error) {
	if closing, err := WorkspaceClosing(ctx, w.client, w.user.ID); err != nil || closing {
		return Reaction{}, nil, false, err
	}
	docs, err := w.reactions().Documents(ctx).GetAll()
	if err != nil {
		return Reaction{}, nil, false, err
	}
	items, err := readReactions(docs)
	if err != nil {
		return Reaction{}, nil, false, err
	}
	for len(items) > 0 {
		i, rest, ok := nextClaim(items, time.Now())
		if !ok {
			return Reaction{}, nil, false, nil
		}
		candidate := items[i]
		var claimed Reaction
		taken := false
		err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			taken = false
			if closing, err := closingIn(tx.Get(closureRef(w.client, w.user.ID))); err != nil || closing {
				return err
			}
			ref := w.reactions().Doc(candidate.ID)
			doc, err := tx.Get(ref)
			if status.Code(err) == codes.NotFound {
				return nil
			}
			if err != nil {
				return err
			}
			if err := doc.DataTo(&claimed); err != nil {
				return err
			}
			now := time.Now()
			if claimed.Route != candidate.Route || now.Before(claimed.ClaimUntil) || now.Before(claimed.NotBefore) {
				return nil
			}
			claimed.ClaimedBy, claimed.ClaimUntil = worker, now.Add(ttl)
			claimed.Attempts++
			claimed.IndexAgain = false
			taken = true
			return tx.Set(ref, claimed)
		})
		if err != nil {
			return Reaction{}, nil, false, err
		}
		if taken {
			return claimed, rest, true, nil
		}
		// Someone else took it, or it is done: the rest of its lane waits
		// behind it either way, so the whole lane is passed over this time.
		lane := laneOf(candidate)
		kept := items[:0]
		for _, item := range items {
			if item.ID != candidate.ID && (laneOf(item) != lane || item.Route) {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	return Reaction{}, nil, false, nil
}

func (w *FirestoreWorkspace) RenewReaction(ctx context.Context, id, worker string, ttl time.Duration) (bool, error) {
	ref := w.reactions().Doc(id)
	kept := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		kept = false
		if closing, err := closingIn(tx.Get(closureRef(w.client, w.user.ID))); err != nil || closing {
			return err
		}
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil {
			return err
		}
		if item.ClaimedBy != worker {
			return nil
		}
		kept = true
		return tx.Update(ref, []firestore.Update{{Path: "claimUntil", Value: time.Now().Add(ttl)}})
	})
	return kept, err
}

func (w *FirestoreWorkspace) ReleaseReaction(ctx context.Context, id, worker string) error {
	ref := w.reactions().Doc(id)
	return w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil || item.ClaimedBy != worker {
			return err
		}
		return tx.Update(ref, []firestore.Update{{Path: "claimedBy", Value: ""}, {Path: "claimUntil", Value: time.Time{}}})
	})
}

func (w *FirestoreWorkspace) FinishReaction(ctx context.Context, id, worker string, also ...string) (bool, error) {
	defer w.flushDispatch()
	for _, name := range append([]string{id}, also...) {
		if !safeName(name) {
			return false, errors.New("reaction needs an id")
		}
	}
	finished := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		finished = false
		doc, err := tx.Get(w.reactions().Doc(id))
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var head Reaction
		if err := doc.DataTo(&head); err != nil {
			return err
		}
		if head.ClaimedBy != worker {
			return nil
		}
		if head.Index && head.IndexAgain {
			finished = true
			head.ClaimedBy, head.ClaimUntil, head.Attempts, head.NotBefore = "", time.Time{}, 0, time.Time{}
			if err := w.queueDispatch(tx, &head); err != nil {
				return err
			}
			return tx.Set(w.reactions().Doc(id), head)
		}
		for _, name := range also {
			if err := tx.Delete(w.reactions().Doc(name)); err != nil {
				return err
			}
		}
		finished = true
		return tx.Delete(w.reactions().Doc(id))
	})
	return finished && err == nil, err
}

func (w *FirestoreWorkspace) DeferReaction(ctx context.Context, id, worker string, notBefore time.Time) (bool, error) {
	defer w.flushDispatch()
	if !safeName(id) {
		return false, errors.New("reaction needs an id")
	}
	ref := w.reactions().Doc(id)
	deferred := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		deferred = false
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil || item.ClaimedBy != worker {
			return err
		}
		deferred = true
		attempts := 0
		if item.Locate || item.CheckIn != "" {
			attempts = item.Attempts
		}
		item.ClaimedBy, item.ClaimUntil, item.Attempts, item.NotBefore = "", time.Time{}, attempts, notBefore
		if err := w.queueDispatch(tx, &item); err != nil {
			return err
		}
		return tx.Set(ref, item)
	})
	return deferred && err == nil, err
}

// workspaceOf is the workspace key a document under users/{key} belongs to.
func workspaceOf(ref *firestore.DocumentRef) string {
	for ref != nil {
		if ref.Parent != nil && ref.Parent.ID == "users" && ref.Parent.Parent == nil {
			return ref.ID
		}
		if ref.Parent == nil {
			return ""
		}
		ref = ref.Parent.Parent
	}
	return ""
}

// ClaimableWorkspaces names the workspaces with a reaction someone could
// take now, across every user.
func ClaimableWorkspaces(ctx context.Context, client *firestore.Client) (map[string]bool, error) {
	docs, err := client.CollectionGroup("reactions").Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	byKey := map[string][]Reaction{}
	for _, doc := range docs {
		key := workspaceOf(doc.Ref)
		if key == "" {
			continue
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil {
			continue
		}
		byKey[key] = append(byKey[key], item)
	}
	out := map[string]bool{}
	now := time.Now()
	for key, items := range byKey {
		if _, _, ok := nextClaim(items, now); ok {
			out[key] = true
		}
	}
	return out, nil
}

// WatchReactions calls added with the workspace key whenever a reaction is
// queued for any user, until ctx ends.
func WatchReactions(ctx context.Context, client *firestore.Client, added func(key string)) {
	go func() {
		for ctx.Err() == nil {
			iter := client.CollectionGroup("reactions").Snapshots(ctx)
			for {
				snapshot, err := iter.Next()
				if err != nil {
					break
				}
				for _, change := range snapshot.Changes {
					released := change.Kind == firestore.DocumentModified && strings.TrimSpace(stringField(change.Doc, "claimedBy")) == ""
					if change.Kind == firestore.DocumentAdded || released {
						if key := workspaceOf(change.Doc.Ref); key != "" {
							added(key)
						}
					}
				}
			}
			iter.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func stringField(doc *firestore.DocumentSnapshot, field string) string {
	value, _ := doc.Data()[field].(string)
	return value
}

// WorkspaceClaimable reports whether one workspace has a reaction someone
// could take now.
func WorkspaceClaimable(ctx context.Context, client *firestore.Client, key string) (bool, error) {
	if closing, err := WorkspaceClosing(ctx, client, key); err != nil || closing {
		return false, err
	}
	docs, err := reactionsOf(client, key).Documents(ctx).GetAll()
	if err != nil {
		return false, err
	}
	items, err := readReactions(docs)
	if err != nil {
		return false, err
	}
	_, _, ok := nextClaim(items, time.Now())
	return ok, nil
}

// WorkspaceExists reports whether a workspace was ever set up: saving its
// settings writes the user document.
func WorkspaceExists(ctx context.Context, client *firestore.Client, key string) (bool, error) {
	_, err := client.Collection("users").Doc(key).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return err == nil, err
}

// EntryReaction is the routing request stored alongside an accepted user entry.
func EntryReaction(entry timeline.Entry) Reaction {
	text := entry.Text
	return Reaction{ID: NewID("r"), At: time.Now(), Lane: "thread:" + timeline.RootOf(entry), Entry: entry.ID, EntryText: &text, Place: entry.Place, Route: true}
}

func validateInitialReaction(entry timeline.Entry, initial []Reaction) error {
	if len(initial) == 0 {
		return nil
	}
	if len(initial) != 1 {
		return errors.New("entry accepts one initial reaction")
	}
	r := initial[0]
	if !safeName(r.ID) || r.ID == DueReaction || r.ID == IndexReaction || !r.Route || r.Entry != entry.ID || r.Lane != "thread:"+timeline.RootOf(entry) || r.EntryText == nil || *r.EntryText != entry.Text || r.ClaimedBy != "" || r.Delivery != nil || r.Index || r.Due || r.CheckIn != "" || r.Ask != "" || r.Deliver != "" || r.Deleted != nil || r.Forgotten != nil {
		return errors.New("invalid initial entry reaction")
	}
	return nil
}
