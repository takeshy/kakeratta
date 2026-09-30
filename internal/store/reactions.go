package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reaction is one piece of persona work the runner has accepted and not yet
// finished: a post or an edit to react to, a deleted note or post a persona
// is to be told about. It is kept until the work is done, so a restart, a
// crash or another instance taking the workspace over carries on with it
// instead of losing it.
type Reaction struct {
	ID       string    `json:"id" firestore:"id"`
	At       time.Time `json:"at" firestore:"at"`
	Attempts int       `json:"attempts" firestore:"attempts"`

	Entry  string          `json:"entry,omitempty" firestore:"entry,omitempty"`
	Edited bool            `json:"edited,omitempty" firestore:"edited,omitempty"`
	Place  *timeline.Place `json:"place,omitempty" firestore:"place,omitempty"`

	Forgotten *ForgottenReaction `json:"forgotten,omitempty" firestore:"forgotten,omitempty"`
	Deleted   *DeletedReaction   `json:"deleted,omitempty" firestore:"deleted,omitempty"`
}

// ForgottenReaction is notes a persona is to be told were deleted.
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

const reactionsDir = "reactions"

func sortReactions(items []Reaction) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		return items[i].ID < items[j].ID
	})
}

// SaveReaction keeps a reaction, replacing one with the same ID.
func (s *Store) SaveReaction(item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.path(reactionsDir, item.ID+".json"), item)
}

// Reactions lists the unfinished reactions, oldest first.
func (s *Store) Reactions(ctx context.Context) ([]Reaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths, err := filepath.Glob(s.path(reactionsDir, "*.json"))
	if err != nil {
		return nil, err
	}
	items := make([]Reaction, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

// FinishReaction forgets a reaction whose work is done.
func (s *Store) FinishReaction(id string) error {
	if !safeName(id) {
		return errors.New("reaction needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(reactionsDir, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *FirestoreWorkspace) reactions() *firestore.CollectionRef {
	return w.user.Collection("reactions")
}

func (w *FirestoreWorkspace) SaveReaction(item Reaction) error {
	if item.ID == "" || !safeName(item.ID) {
		return errors.New("reaction needs an id")
	}
	_, err := w.reactions().Doc(item.ID).Set(context.Background(), item)
	return err
}

func (w *FirestoreWorkspace) Reactions(ctx context.Context) ([]Reaction, error) {
	iter := w.reactions().Documents(ctx)
	defer iter.Stop()
	var items []Reaction
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var item Reaction
		if err := doc.DataTo(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sortReactions(items)
	return items, nil
}

func (w *FirestoreWorkspace) FinishReaction(id string) error {
	if !safeName(id) {
		return errors.New("reaction needs an id")
	}
	_, err := w.reactions().Doc(id).Delete(context.Background())
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

// WatchReactions calls changed whenever a reaction is added anywhere - by
// this instance or another one serving the same workspace - until ctx ends.
func (w *FirestoreWorkspace) WatchReactions(ctx context.Context, changed func()) {
	go func() {
		iter := w.reactions().Snapshots(ctx)
		defer iter.Stop()
		for {
			snapshot, err := iter.Next()
			if err != nil {
				if ctx.Err() == nil {
					// The runner also rereads the queue every minute, so a
					// broken listener delays a reaction rather than losing it.
					time.Sleep(5 * time.Second)
					if ctx.Err() == nil {
						w.WatchReactions(ctx, changed)
					}
				}
				return
			}
			for _, change := range snapshot.Changes {
				if change.Kind == firestore.DocumentAdded {
					changed()
					break
				}
			}
		}
	}()
}
