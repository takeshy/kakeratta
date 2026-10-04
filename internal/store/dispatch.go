package store

import (
	"context"
	"errors"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Job identifies one persistent phase. It contains no user text or credentials.
type Job struct {
	Workspace string `json:"workspace" firestore:"workspace"`
	Reaction  string `json:"reaction" firestore:"reaction"`
	Dispatch  string `json:"dispatch" firestore:"dispatch"`
}

type dispatchRequest struct {
	Job     Job       `firestore:"job"`
	ReadyAt time.Time `firestore:"readyAt"`
}

var ErrJobBusy = errors.New("reaction is pending or claimed")

// SetJobPublisher enables transactional outbox writes. Set before use.
func (w *FirestoreWorkspace) SetJobPublisher(publish func(context.Context, Job) error) {
	w.publishJob = publish
}

func (w *FirestoreWorkspace) queueDispatch(tx *firestore.Transaction, item *Reaction) error {
	if w.publishJob == nil {
		return nil
	}
	item.Dispatch = NewID("dispatch")
	at := item.NotBefore
	if at.IsZero() {
		at = time.Now()
	}
	job := Job{Workspace: w.user.ID, Reaction: item.ID, Dispatch: item.Dispatch}
	return tx.Create(w.user.Collection("dispatchOutbox").Doc(item.Dispatch), dispatchRequest{Job: job, ReadyAt: at})
}

func (w *FirestoreWorkspace) flushDispatch() {
	if w.publishJob == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	docs, err := w.user.Collection("dispatchOutbox").Where("readyAt", "<=", time.Now()).Limit(16).Documents(ctx).GetAll()
	if err == nil {
		err = publishDispatch(ctx, docs, w.publishJob)
	}
	if err != nil {
		log.Printf("job dispatch deferred: %v", err)
	}
}

// RepairDispatch reads only due unpublished requests, not users or reactions.
// Publishing twice is safe: workers compare the persistent phase generation.
func RepairDispatch(ctx context.Context, client *firestore.Client, publish func(context.Context, Job) error) error {
	docs, err := client.CollectionGroup("dispatchOutbox").Where("readyAt", "<=", time.Now()).Limit(100).Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	return publishDispatch(ctx, docs, publish)
}

func publishDispatch(ctx context.Context, docs []*firestore.DocumentSnapshot, publish func(context.Context, Job) error) error {
	var failures []error
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		var request dispatchRequest
		if err := doc.DataTo(&request); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := publish(ctx, request.Job); err != nil {
			failures = append(failures, err)
			// Back off failed documents so they cannot starve later batches.
			_, _ = doc.Ref.Update(ctx, []firestore.Update{{Path: "readyAt", Value: time.Now().Add(time.Minute)}}, firestore.LastUpdateTime(doc.UpdateTime))
			continue
		}
		if _, err := doc.Ref.Delete(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ClaimJob reads only the target and its lane. Routing may bypass earlier
// delivery; all other work must be the first eligible item in its lane.
func (w *FirestoreWorkspace) ClaimJob(ctx context.Context, job Job, worker string, ttl time.Duration) (Reaction, bool, error) {
	var claimed Reaction
	taken := false
	err := w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		taken = false
		if closing, err := closingIn(tx.Get(closureRef(w.client, w.user.ID))); err != nil || closing {
			return err
		}
		doc, err := tx.Get(w.reactions().Doc(job.Reaction))
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if err := doc.DataTo(&claimed); err != nil {
			return err
		}
		if claimed.Dispatch != job.Dispatch {
			return nil
		}
		now := time.Now()
		if now.Before(claimed.ClaimUntil) || now.Before(claimed.NotBefore) {
			return ErrJobBusy
		}
		if !claimed.Route {
			query := w.reactions().Where("lane", "==", claimed.Lane)
			docs, err := tx.Documents(query).GetAll()
			if err != nil {
				return err
			}
			items, err := readReactions(docs)
			if err != nil {
				return err
			}
			i, _, ok := nextClaim(items, now)
			if !ok || items[i].ID != claimed.ID {
				return ErrJobBusy
			}
		}
		claimed.ClaimedBy, claimed.ClaimUntil = worker, now.Add(ttl)
		claimed.Attempts++
		claimed.IndexAgain = false
		taken = true
		return tx.Set(doc.Ref, claimed)
	})
	return claimed, taken && err == nil, err
}

// JobFinished checks durable completion or an atomically saved successor.
func (w *FirestoreWorkspace) JobFinished(ctx context.Context, job Job) (bool, error) {
	doc, err := w.reactions().Doc(job.Reaction).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var item Reaction
	if err := doc.DataTo(&item); err != nil {
		return false, err
	}
	return item.Dispatch != job.Dispatch, nil
}
