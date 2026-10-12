package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Runtime state is what persona work has in hand rather than what the user
// keeps: an ask waiting on the external agent, a request made
// to it directly, a trial a person may apply, when
// the agent last called in, who is rewriting a persona's notes. It lives in
// the workspace, not in a process, so any instance serving the user can pick
// it up - and a restart does not lose it.

// Record collections hold small JSON values by id. Favorites are user-owned
// state; unlike runtime collections, their files are included in exports.
const (
	RecordAsks         = "asks"
	RecordRequests     = "requests"
	RecordTrials       = "trials"
	RecordPresence     = "presence"
	RecordPush         = "push-subscriptions"
	RecordFavorites    = "favorites"
	RecordUsage        = "usage"
	RecordTimelineSync = "timeline-sync"
)

var recordCollections = map[string]bool{RecordAsks: true, RecordRequests: true, RecordTrials: true, RecordPresence: true, RecordPush: true, RecordFavorites: true, RecordUsage: true, RecordTimelineSync: true}

// ErrUnknownRecords is a collection name that is not a runtime record kind.
var ErrUnknownRecords = errors.New("unknown runtime record collection")

// RecordChange computes a record's next value from its current one. It
// returns nil to delete the record.
type RecordChange func(current []byte, exists bool) ([]byte, error)

type fileRecord struct {
	ExpireAt time.Time       `json:"expireAt"`
	Data     json.RawMessage `json:"data"`
}

type recordDoc struct {
	Data     []byte    `firestore:"data"`
	ExpireAt time.Time `firestore:"expireAt"`
}

func recordLive(expireAt, now time.Time) bool { return expireAt.IsZero() || now.Before(expireAt) }

func (s *Store) recordPath(collection, id string) (string, error) {
	if !recordCollections[collection] {
		return "", ErrUnknownRecords
	}
	if !safeName(id) {
		return "", errors.New("invalid record id")
	}
	if collection == RecordFavorites {
		return s.path(collection, id+".json"), nil
	}
	return s.path("runtime", collection, id+".json"), nil
}

func (s *Store) readRecord(path string) ([]byte, bool, error) {
	var record fileRecord
	if err := readJSON(path, &record); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !recordLive(record.ExpireAt, time.Now()) {
		return nil, false, nil
	}
	return record.Data, true, nil
}

// Record reads one runtime record.
func (s *Store) Record(_ context.Context, collection, id string) ([]byte, bool, error) {
	path, err := s.recordPath(collection, id)
	if err != nil {
		return nil, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readRecord(path)
}

// Records reads every live record of a collection, by id.
func (s *Store) Records(_ context.Context, collection string) (map[string][]byte, error) {
	if !recordCollections[collection] {
		return nil, ErrUnknownRecords
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.path("runtime", collection)
	if collection == RecordFavorites {
		dir = s.path(collection)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, path := range paths {
		data, ok, err := s.readRecord(path)
		if err != nil {
			return nil, err
		}
		if ok {
			out[strings.TrimSuffix(filepath.Base(path), ".json")] = data
		}
	}
	return out, nil
}

// UpdateRecord changes one record atomically. The record lapses at expire;
// the zero time keeps it until it is deleted.
func (s *Store) UpdateRecord(_ context.Context, collection, id string, expire time.Time, change RecordChange) error {
	path, err := s.recordPath(collection, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists, err := s.readRecord(path)
	if err != nil {
		return err
	}
	next, err := change(current, exists)
	if err != nil {
		return err
	}
	if next == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !json.Valid(next) {
		return errors.New("runtime record must be JSON")
	}
	return writeJSON(path, fileRecord{ExpireAt: expire, Data: next})
}

func (w *FirestoreWorkspace) recordRef(collection, id string) (*firestore.DocumentRef, error) {
	if !recordCollections[collection] {
		return nil, ErrUnknownRecords
	}
	if !safeName(id) {
		return nil, errors.New("invalid record id")
	}
	return w.user.Collection(collection).Doc(id), nil
}

func readRecordDoc(doc *firestore.DocumentSnapshot, err error) ([]byte, bool, error) {
	if status.Code(err) == codes.NotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var record recordDoc
	if err := doc.DataTo(&record); err != nil {
		return nil, false, err
	}
	if !recordLive(record.ExpireAt, time.Now()) {
		return nil, false, nil
	}
	return record.Data, true, nil
}

func (w *FirestoreWorkspace) Record(ctx context.Context, collection, id string) ([]byte, bool, error) {
	ref, err := w.recordRef(collection, id)
	if err != nil {
		return nil, false, err
	}
	return readRecordDoc(ref.Get(ctx))
}

func (w *FirestoreWorkspace) Records(ctx context.Context, collection string) (map[string][]byte, error) {
	if !recordCollections[collection] {
		return nil, ErrUnknownRecords
	}
	iter := w.user.Collection(collection).Documents(ctx)
	defer iter.Stop()
	out := map[string][]byte{}
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		data, ok, err := readRecordDoc(doc, err)
		if err != nil {
			return nil, err
		}
		if ok {
			out[doc.Ref.ID] = data
		}
	}
}

func (w *FirestoreWorkspace) UpdateRecord(ctx context.Context, collection, id string, expire time.Time, change RecordChange) error {
	ref, err := w.recordRef(collection, id)
	if err != nil {
		return err
	}
	return w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		current, exists, err := readRecordDoc(tx.Get(ref))
		if err != nil {
			return err
		}
		next, err := change(current, exists)
		if err != nil {
			return err
		}
		if next == nil {
			if exists {
				return tx.Delete(ref)
			}
			return nil
		}
		return tx.Set(ref, recordDoc{Data: next, ExpireAt: expire})
	})
}

// LockTTL is how long a lock holds without renewal; its holder renews it at a
// third of that while it works.
// (A variable so that tests can shorten it.)
var LockTTL = 30 * time.Second

// Lock takes a named lock on the workspace, waiting for it until ctx ends. The
// context it returns is the holder's to work under: it ends when the lock is
// lost - another process took it after this one failed to renew in time - so
// the work stops instead of running beside the new holder. The release it
// returns must be called once the work is done.
func (s *Store) Lock(ctx context.Context, name string) (context.Context, func(), error) {
	value, _ := s.locks.LoadOrStore(name, make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		// Held in this process: it cannot be lost to another.
		return ctx, func() { <-lock }, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

type lockDoc struct {
	Owner string    `firestore:"owner"`
	Until time.Time `firestore:"until"`
}

// Lock takes a named lock shared by every instance serving the workspace. A
// holder that dies leaves it to run out after LockTTL. A holder that can no
// longer renew it - another took it, or renewing failed for most of LockTTL -
// has the context it was given ended.
func (w *FirestoreWorkspace) Lock(ctx context.Context, name string) (context.Context, func(), error) {
	ref := w.user.Collection("locks").Doc(sanitise(name))
	owner := NewID("lock")
	take := func(ctx context.Context, renew bool) (bool, error) {
		taken := false
		err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			taken = false
			var current lockDoc
			doc, err := tx.Get(ref)
			switch {
			case status.Code(err) == codes.NotFound:
			case err != nil:
				return err
			default:
				if err := doc.DataTo(&current); err != nil {
					return err
				}
			}
			now := time.Now()
			if renew && current.Owner != owner {
				return nil
			}
			if !renew && current.Owner != "" && current.Owner != owner && now.Before(current.Until) {
				return nil
			}
			taken = true
			return tx.Set(ref, lockDoc{Owner: owner, Until: now.Add(LockTTL)})
		})
		return taken, err
	}
	wait := 100 * time.Millisecond
	for {
		taken, err := take(ctx, false)
		if err != nil {
			return nil, nil, err
		}
		if taken {
			break
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 2*time.Second)
	}
	// The holder works under work, which ends with ctx or with the lock.
	work, lost := context.WithCancel(ctx)
	held, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	// The watchdog ends the work if the lock has not been renewed for most of
	// its lease. It runs on its own clock, so a renewal that hangs - a request
	// that never returns - cannot keep the work going past the lease.
	safe := LockTTL * 2 / 3
	watchdog := time.AfterFunc(safe, lost)
	go func() {
		defer close(done)
		ticker := time.NewTicker(LockTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-held.Done():
				return
			case <-ticker.C:
				// One renewal may take no longer than the interval.
				renewing, cancel := context.WithTimeout(held, LockTTL/3)
				kept, err := take(renewing, true)
				cancel()
				switch {
				case err == nil && kept:
					watchdog.Reset(safe)
				case err == nil:
					lost() // Another holds it now.
					return
				}
				// A renewal that failed is tried again at the next tick; the
				// watchdog ends the work if they keep failing.
			}
		}
	}()
	var once sync.Once
	return work, func() {
		once.Do(func() {
			watchdog.Stop()
			stop()
			<-done
			lost()
			release, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = w.client.RunTransaction(release, func(ctx context.Context, tx *firestore.Transaction) error {
				doc, err := tx.Get(ref)
				if err != nil {
					return nil
				}
				var current lockDoc
				if doc.DataTo(&current) != nil || current.Owner != owner {
					return nil
				}
				return tx.Delete(ref)
			})
		})
	}, nil
}

// closingStale is how long a closing mark stands when nobody lifts it: a
// deletion that died half way must not lock the account out for good.
const closingStale = 20 * time.Minute

// SetClosing marks the workspace as being deleted, or lifts the mark. While
// it stands no reaction is taken up, and one in hand loses its claim.
func (s *Store) SetClosing(_ context.Context, closing bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = closing
	return nil
}

func (s *Store) closingLocked() bool { return s.closing }

func closureRef(client *firestore.Client, key string) *firestore.DocumentRef {
	return client.Collection("workspaceClosures").Doc(key)
}

// The mark lives outside the workspace so deleting the workspace does not
// lift it before the last sweep.
func (w *FirestoreWorkspace) SetClosing(ctx context.Context, closing bool) error {
	ref := closureRef(w.client, w.user.ID)
	if !closing {
		_, err := ref.Delete(ctx)
		return err
	}
	_, err := ref.Set(ctx, map[string]any{"at": time.Now()})
	return err
}

func closingIn(doc *firestore.DocumentSnapshot, err error) (bool, error) {
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	at, _ := doc.Data()["at"].(time.Time)
	return time.Since(at) < closingStale, nil
}

// WorkspaceClosing reports whether a workspace is being deleted.
func WorkspaceClosing(ctx context.Context, client *firestore.Client, key string) (bool, error) {
	return closingIn(closureRef(client, key).Get(ctx))
}
