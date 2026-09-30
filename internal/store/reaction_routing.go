package store

import (
	"context"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ClaimRoutingReaction claims only routing, independently of delivery slots
// and the conversation's current owner. The same durable record is used by
// recovery workers, so a competing instance cannot also start this routing.
func (s *Store) ClaimRoutingReaction(ctx context.Context, id, worker string, ttl time.Duration) (Reaction, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reaction{}, false, err
	}
	if s.closingLocked() {
		return Reaction{}, false, nil
	}
	var item Reaction
	if err := readJSON(s.path(reactionsDir, id+".json"), &item); err != nil {
		if os.IsNotExist(err) {
			return Reaction{}, false, nil
		}
		return Reaction{}, false, err
	}
	now := time.Now()
	if !item.Route || now.Before(item.ClaimUntil) || now.Before(item.NotBefore) {
		return Reaction{}, false, nil
	}
	item.ClaimedBy, item.ClaimUntil = worker, now.Add(ttl)
	item.Attempts++
	err := writeJSON(s.path(reactionsDir, id+".json"), item)
	return item, err == nil, err
}

func (s *Store) PrepareReaction(ctx context.Context, id, worker string, delivery EntryDelivery, routing *Routing) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.closingLocked() {
		return false, nil
	}
	path := s.path(reactionsDir, id+".json")
	var item Reaction
	if err := readJSON(path, &item); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !item.Route || item.ClaimedBy != worker || !time.Now().Before(item.ClaimUntil) {
		return false, nil
	}
	records, err := s.readRouting()
	if err != nil {
		return false, err
	}
	if routing == nil {
		delete(records, item.Entry)
	} else {
		records[item.Entry] = normaliseRouting(*routing)
	}
	// If the process dies between these two writes, routing is retried before
	// delivery becomes eligible. No turn has started to be overwritten yet.
	if err := writeJSON(routingPath(s.root), records); err != nil {
		return false, err
	}
	item.Route, item.Delivery = false, &delivery
	item.ClaimedBy, item.ClaimUntil, item.Attempts, item.NotBefore = "", time.Time{}, 0, time.Time{}
	err = writeJSON(path, item)
	return err == nil, err
}

func (w *FirestoreWorkspace) ClaimRoutingReaction(ctx context.Context, id, worker string, ttl time.Duration) (Reaction, bool, error) {
	var item Reaction
	taken := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		taken = false
		if closing, err := closingIn(tx.Get(closureRef(w.client, w.user.ID))); err != nil || closing {
			return err
		}
		ref := w.reactions().Doc(id)
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if err := doc.DataTo(&item); err != nil {
			return err
		}
		now := time.Now()
		if !item.Route || now.Before(item.ClaimUntil) || now.Before(item.NotBefore) {
			return nil
		}
		item.ClaimedBy, item.ClaimUntil = worker, now.Add(ttl)
		item.Attempts++
		taken = true
		return tx.Set(ref, item)
	})
	return item, taken && err == nil, err
}

func (w *FirestoreWorkspace) PrepareReaction(ctx context.Context, id, worker string, delivery EntryDelivery, routing *Routing) (bool, error) {
	prepared := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		prepared = false
		if closing, err := closingIn(tx.Get(closureRef(w.client, w.user.ID))); err != nil || closing {
			return err
		}
		ref := w.reactions().Doc(id)
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
		if !item.Route || item.ClaimedBy != worker || !time.Now().Before(item.ClaimUntil) {
			return nil
		}
		routeRef := w.user.Collection("routing").Doc(sanitise(item.Entry))
		if routing == nil {
			err = tx.Delete(routeRef)
		} else {
			err = tx.Set(routeRef, normaliseRouting(*routing))
		}
		if err != nil {
			return err
		}
		item.Route, item.Delivery = false, &delivery
		item.ClaimedBy, item.ClaimUntil, item.Attempts, item.NotBefore = "", time.Time{}, 0, time.Time{}
		prepared = true
		return tx.Set(ref, item)
	})
	return prepared && err == nil, err
}
