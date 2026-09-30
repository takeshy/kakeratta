package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Firestore coordinates instances through the same database the workspaces
// live in, so running more than one needs nothing else deployed.
//
// Leases live in workspaceLeases/{key}, outside the workspace, so they
// outlast its deletion. Calls and events live under users/{key}, so deleting
// a workspace takes them with it; both carry an expireAt field for a TTL
// policy to sweep what nobody collected.
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

// staleClosing is how long a closing mark stands when nobody lifts it: a
// deletion that died half way must not lock the account out for good.
const staleClosing = 10 * time.Minute

// inlineBytes is the largest body kept in the document itself; anything
// bigger goes into parts, since a document holds at most 1 MiB.
const inlineBytes = 512 << 10

// keep is how long an uncollected call or event is left for the TTL policy.
const keep = time.Hour

type leaseDoc struct {
	Owner     string    `firestore:"owner"`
	Until     time.Time `firestore:"until"`
	Closing   bool      `firestore:"closing"`
	ClosingAt time.Time `firestore:"closingAt"`
}

func (f *Firestore) leaseRef(key string) *firestore.DocumentRef {
	return f.client.Collection("workspaceLeases").Doc(key)
}

func (f *Firestore) readLease(doc *firestore.DocumentSnapshot, err error) (leaseDoc, error) {
	if status.Code(err) == codes.NotFound {
		return leaseDoc{}, nil
	}
	if err != nil {
		return leaseDoc{}, err
	}
	var lease leaseDoc
	if err := doc.DataTo(&lease); err != nil {
		return leaseDoc{}, err
	}
	if lease.Closing && f.now().Sub(lease.ClosingAt) > staleClosing {
		lease.Closing = false
	}
	return lease, nil
}

func (f *Firestore) Lease(ctx context.Context, key string) (Lease, error) {
	lease, err := f.readLease(f.leaseRef(key).Get(ctx))
	return Lease{Owner: lease.Owner, Until: lease.Until, Closing: lease.Closing}, err
}

func (f *Firestore) Acquire(ctx context.Context, key string) (bool, error) {
	ref := f.leaseRef(key)
	acquired := false
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		acquired = false
		lease, err := f.readLease(tx.Get(ref))
		if err != nil {
			return err
		}
		now := f.now()
		if lease.Closing || (lease.Owner != f.instance && lease.Owner != "" && now.Before(lease.Until)) {
			return nil
		}
		acquired = true
		return tx.Set(ref, leaseDoc{Owner: f.instance, Until: now.Add(LeaseTTL)})
	})
	return acquired, err
}

func (f *Firestore) Release(ctx context.Context, key string) error {
	ref := f.leaseRef(key)
	return f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		lease, err := f.readLease(tx.Get(ref))
		if err != nil || lease.Owner != f.instance {
			return err
		}
		return tx.Update(ref, []firestore.Update{{Path: "owner", Value: ""}, {Path: "until", Value: time.Time{}}})
	})
}

func (f *Firestore) MarkClosing(ctx context.Context, key string) error {
	_, err := f.leaseRef(key).Set(ctx, map[string]any{"closing": true, "closingAt": f.now()}, firestore.MergeAll)
	return err
}

func (f *Firestore) ClearClosing(ctx context.Context, key string) error {
	_, err := f.leaseRef(key).Delete(ctx)
	return err
}

type callDoc struct {
	Owner      string              `firestore:"owner"`
	State      string              `firestore:"state"`
	Created    time.Time           `firestore:"created"`
	ExpireAt   time.Time           `firestore:"expireAt"`
	Method     string              `firestore:"method"`
	URL        string              `firestore:"url"`
	Host       string              `firestore:"host"`
	Header     map[string][]string `firestore:"header"`
	Body       []byte              `firestore:"body"`
	BodyParts  int                 `firestore:"bodyParts"`
	Status     int                 `firestore:"status"`
	Reply      map[string][]string `firestore:"replyHeader"`
	ReplyBody  []byte              `firestore:"replyBody"`
	ReplyParts int                 `firestore:"replyParts"`
}

const (
	callPending = "pending"
	callRunning = "running"
	callDone    = "done"
)

func (f *Firestore) calls(key string) *firestore.CollectionRef {
	return f.client.Collection("users").Doc(key).Collection("calls")
}

// putBytes stores data beside doc: inline when small, as numbered parts
// otherwise. It returns the inline value and the number of parts.
func putBytes(ctx context.Context, doc *firestore.DocumentRef, prefix string, data []byte) ([]byte, int, error) {
	if len(data) <= inlineBytes {
		return data, 0, nil
	}
	parts := 0
	for start := 0; start < len(data); start += inlineBytes {
		end := min(start+inlineBytes, len(data))
		if _, err := doc.Collection("parts").Doc(fmt.Sprintf("%s-%04d", prefix, parts)).Set(ctx, map[string]any{"data": data[start:end]}); err != nil {
			return nil, 0, err
		}
		parts++
	}
	return nil, parts, nil
}

func getBytes(ctx context.Context, doc *firestore.DocumentRef, prefix string, inline []byte, parts int) ([]byte, error) {
	if parts == 0 {
		return inline, nil
	}
	var data []byte
	for i := 0; i < parts; i++ {
		part, err := doc.Collection("parts").Doc(fmt.Sprintf("%s-%04d", prefix, i)).Get(ctx)
		if err != nil {
			return nil, err
		}
		chunk, _ := part.Data()["data"].([]byte)
		data = append(data, chunk...)
	}
	return data, nil
}

// dropDoc removes a call or event with its parts.
func dropDoc(ctx context.Context, client *firestore.Client, doc *firestore.DocumentRef) {
	refs := doc.Collection("parts").DocumentRefs(ctx)
	for {
		ref, err := refs.Next()
		if err != nil {
			break
		}
		_, _ = ref.Delete(ctx)
	}
	_, _ = doc.Delete(ctx)
}

func (f *Firestore) Forward(ctx context.Context, key, owner string, call Call) (Reply, error) {
	ref := f.calls(key).NewDoc()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dropDoc(cleanup, f.client, ref)
	}()
	body, parts, err := putBytes(ctx, ref, "body", call.Body)
	if err != nil {
		return Reply{}, err
	}
	now := f.now()
	if _, err := ref.Create(ctx, callDoc{
		Owner: owner, State: callPending, Created: now, ExpireAt: now.Add(keep),
		Method: call.Method, URL: call.URL, Host: call.Host, Header: call.Header,
		Body: body, BodyParts: parts,
	}); err != nil {
		return Reply{}, err
	}

	// The owner may die with the call in hand. Its lease tells: once someone
	// else holds the workspace, or nobody does, no answer is coming.
	waiting, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go func() {
		ticker := time.NewTicker(RenewEvery / 2)
		defer ticker.Stop()
		for {
			select {
			case <-waiting.Done():
				return
			case <-ticker.C:
				lease, err := f.Lease(waiting, key)
				if err == nil && (!lease.Live(f.now()) || lease.Owner != owner) {
					stop(ErrOwnerGone)
					return
				}
			}
		}
	}()
	iter := ref.Snapshots(waiting)
	defer iter.Stop()
	for {
		snapshot, err := iter.Next()
		if err != nil {
			if cause := context.Cause(waiting); errors.Is(cause, ErrOwnerGone) {
				return Reply{}, ErrOwnerGone
			}
			if ctx.Err() != nil {
				return Reply{}, ctx.Err()
			}
			return Reply{}, err
		}
		if !snapshot.Exists() {
			return Reply{}, ErrOwnerGone
		}
		var doc callDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return Reply{}, err
		}
		if doc.State != callDone {
			continue
		}
		reply, err := getBytes(ctx, ref, "reply", doc.ReplyBody, doc.ReplyParts)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Status: doc.Status, Header: http.Header(doc.Reply), Body: reply}, nil
	}
}

func (f *Firestore) Serve(ctx context.Context, key string, handle func(context.Context, Call) Reply) {
	query := f.calls(key).Where("owner", "==", f.instance).Where("state", "==", callPending)
	listen(ctx, "calls "+key, func(ctx context.Context) error {
		iter := query.Snapshots(ctx)
		defer iter.Stop()
		for {
			snapshot, err := iter.Next()
			if err != nil {
				return err
			}
			for _, change := range snapshot.Changes {
				if change.Kind == firestore.DocumentAdded {
					go f.answer(ctx, change.Doc.Ref, handle)
				}
			}
		}
	})
}

// answer takes one call - unless another listener already did - and writes
// back the reply.
func (f *Firestore) answer(ctx context.Context, ref *firestore.DocumentRef, handle func(context.Context, Call) Reply) {
	var doc callDoc
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(ref)
		if err != nil {
			return err
		}
		if err := snapshot.DataTo(&doc); err != nil {
			return err
		}
		if doc.State != callPending || doc.Owner != f.instance {
			return errTaken
		}
		return tx.Update(ref, []firestore.Update{{Path: "state", Value: callRunning}})
	})
	if err != nil {
		return
	}
	body, err := getBytes(ctx, ref, "body", doc.Body, doc.BodyParts)
	reply := Reply{Status: http.StatusBadGateway, Body: []byte(`{"error":"forwarded request unreadable"}`)}
	if err == nil {
		reply = handle(ctx, Call{Method: doc.Method, URL: doc.URL, Host: doc.Host, Header: http.Header(doc.Header), Body: body})
	}
	inline, parts, err := putBytes(ctx, ref, "reply", reply.Body)
	if err != nil {
		log.Printf("cluster: reply to %s: %v", ref.Path, err)
		return
	}
	_, err = ref.Update(ctx, []firestore.Update{
		{Path: "state", Value: callDone}, {Path: "status", Value: reply.Status},
		{Path: "replyHeader", Value: map[string][]string(reply.Header)},
		{Path: "replyBody", Value: inline}, {Path: "replyParts", Value: parts},
	})
	if err != nil && status.Code(err) != codes.NotFound {
		log.Printf("cluster: reply to %s: %v", ref.Path, err)
	}
}

var errTaken = errors.New("call already taken")

type eventDoc struct {
	Name     string    `firestore:"name"`
	Data     []byte    `firestore:"data"`
	Parts    int       `firestore:"parts"`
	Sent     time.Time `firestore:"sent"`
	ExpireAt time.Time `firestore:"expireAt"`
}

func (f *Firestore) events(key string) *firestore.CollectionRef {
	return f.client.Collection("users").Doc(key).Collection("events")
}

func (f *Firestore) Publish(ctx context.Context, key, name string, data []byte) error {
	ref := f.events(key).NewDoc()
	inline, parts, err := putBytes(ctx, ref, "data", data)
	if err != nil {
		return err
	}
	now := f.now()
	_, err = ref.Create(ctx, eventDoc{Name: name, Data: inline, Parts: parts, Sent: now, ExpireAt: now.Add(keep)})
	return err
}

func (f *Firestore) Subscribe(ctx context.Context, key string, deliver func(name string, data []byte)) {
	listen(ctx, "events "+key, func(ctx context.Context) error {
		// The first snapshot holds what was already there: of that, only
		// what was sent since subscribing is new. The window merely keeps
		// that first snapshot small.
		since := f.now()
		query := f.events(key).Where("sent", ">", since.Add(-time.Minute)).OrderBy("sent", firestore.Asc)
		iter := query.Snapshots(ctx)
		defer iter.Stop()
		first := true
		for {
			snapshot, err := iter.Next()
			if err != nil {
				return err
			}
			initial := first
			first = false
			for _, change := range snapshot.Changes {
				if change.Kind != firestore.DocumentAdded {
					continue
				}
				var doc eventDoc
				if err := change.Doc.DataTo(&doc); err != nil {
					continue
				}
				if initial && doc.Sent.Before(since) {
					continue
				}
				data, err := getBytes(ctx, change.Doc.Ref, "data", doc.Data, doc.Parts)
				if err != nil {
					continue
				}
				deliver(doc.Name, data)
			}
		}
	})
}

// listen keeps a snapshot listener going until ctx ends, starting it again
// after a broken stream. It returns once the first attempt has begun.
func listen(ctx context.Context, what string, run func(context.Context) error) {
	go func() {
		for ctx.Err() == nil {
			err := run(ctx)
			if ctx.Err() != nil || errors.Is(err, iterator.Done) {
				return
			}
			log.Printf("cluster: %s listener: %v", what, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}
