package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Firestore holds leases in the leases collection of the database the
// workspaces live in, so electing needs nothing else deployed.
type Firestore struct {
	client   *firestore.Client
	instance string
	now      func() time.Time
}

// NewFirestore coordinates through client as the instance named instance.
// An empty name gets a random one.
func NewFirestore(client *firestore.Client, instance string) *Firestore {
	if instance == "" {
		instance = NewInstanceID()
	}
	return &Firestore{client: client, instance: instance, now: time.Now}
}

// NewInstanceID names a process uniquely among those sharing a database.
func NewInstanceID() string {
	var noise [8]byte
	if _, err := rand.Read(noise[:]); err != nil {
		return fmt.Sprintf("i%d", time.Now().UnixNano())
	}
	return "i" + hex.EncodeToString(noise[:])
}

func (f *Firestore) Instance() string { return f.instance }

type leaseDoc struct {
	Owner string    `firestore:"owner"`
	Until time.Time `firestore:"until"`
}

func (f *Firestore) ref(name string) *firestore.DocumentRef {
	return f.client.Collection("leases").Doc(name)
}

func readLease(doc *firestore.DocumentSnapshot, err error) (leaseDoc, error) {
	if status.Code(err) == codes.NotFound {
		return leaseDoc{}, nil
	}
	if err != nil {
		return leaseDoc{}, err
	}
	var lease leaseDoc
	err = doc.DataTo(&lease)
	return lease, err
}

func (f *Firestore) Lease(ctx context.Context, name string) (Lease, error) {
	lease, err := readLease(f.ref(name).Get(ctx))
	return Lease{Owner: lease.Owner, Until: lease.Until}, err
}

func (f *Firestore) Acquire(ctx context.Context, name string) (bool, error) {
	ref := f.ref(name)
	acquired := false
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		acquired = false
		lease, err := readLease(tx.Get(ref))
		if err != nil {
			return err
		}
		now := f.now()
		if lease.Owner != f.instance && lease.Owner != "" && now.Before(lease.Until) {
			return nil
		}
		acquired = true
		return tx.Set(ref, leaseDoc{Owner: f.instance, Until: now.Add(LeaseTTL)})
	})
	return acquired, err
}

func (f *Firestore) Release(ctx context.Context, name string) error {
	ref := f.ref(name)
	return f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		lease, err := readLease(tx.Get(ref))
		if err != nil || lease.Owner != f.instance {
			return err
		}
		return tx.Delete(ref)
	})
}
