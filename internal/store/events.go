package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
)

// A workspace's events - a post saved, a persona's remark, routing decided -
// are written to users/{key}/events, and every instance with a page open on
// the workspace follows them, whichever instance did the work. Each carries
// expireAt for a TTL policy; subscribers only read what arrives while they
// listen.

// inlineEventBytes is the largest event body kept in the document itself;
// anything bigger goes into parts, since a document holds at most 1 MiB.
const inlineEventBytes = 512 << 10

// eventKeep is how long an event is left for the TTL policy.
const eventKeep = time.Hour

type eventDoc struct {
	Name     string    `firestore:"name"`
	Data     []byte    `firestore:"data"`
	Parts    int       `firestore:"parts"`
	Sent     time.Time `firestore:"sent"`
	ExpireAt time.Time `firestore:"expireAt"`
}

func (w *FirestoreWorkspace) events() *firestore.CollectionRef {
	return w.user.Collection("events")
}

// Publish sends an event to every instance following the workspace, this
// one included.
func (w *FirestoreWorkspace) Publish(name string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref := w.events().NewDoc()
	inline, parts := data, 0
	if len(data) > inlineEventBytes {
		inline = nil
		for start := 0; start < len(data); start += inlineEventBytes {
			end := min(start+inlineEventBytes, len(data))
			if _, err := ref.Collection("parts").Doc(fmt.Sprintf("%04d", parts)).Set(ctx, map[string]any{"data": data[start:end]}); err != nil {
				return err
			}
			parts++
		}
	}
	now := time.Now()
	_, err := ref.Create(ctx, eventDoc{Name: name, Data: inline, Parts: parts, Sent: now, ExpireAt: now.Add(eventKeep)})
	return err
}

func eventData(ctx context.Context, ref *firestore.DocumentRef, doc eventDoc) ([]byte, error) {
	if doc.Parts == 0 {
		return doc.Data, nil
	}
	var data []byte
	for i := 0; i < doc.Parts; i++ {
		part, err := ref.Collection("parts").Doc(fmt.Sprintf("%04d", i)).Get(ctx)
		if err != nil {
			return nil, err
		}
		chunk, _ := part.Data()["data"].([]byte)
		data = append(data, chunk...)
	}
	return data, nil
}

// Subscribe delivers the workspace's events published from now on, until ctx
// ends. A broken stream is started again; what was sent meanwhile is lost, as
// a page that reconnects rereads the timeline anyway.
func (w *FirestoreWorkspace) Subscribe(ctx context.Context, deliver func(name string, data []byte)) {
	go func() {
		for ctx.Err() == nil {
			if err := w.follow(ctx, deliver); err != nil && ctx.Err() == nil {
				log.Printf("events %s: %v", w.user.ID, err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func (w *FirestoreWorkspace) follow(ctx context.Context, deliver func(string, []byte)) error {
	// The first snapshot holds what was already there: of that, only what was
	// sent since subscribing is new. The window keeps that snapshot small.
	since := time.Now()
	iter := w.events().Where("sent", ">", since.Add(-time.Minute)).OrderBy("sent", firestore.Asc).Snapshots(ctx)
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
			if err := change.Doc.DataTo(&doc); err != nil || (initial && doc.Sent.Before(since)) {
				continue
			}
			data, err := eventData(ctx, change.Doc.Ref, doc)
			if err != nil {
				continue
			}
			deliver(doc.Name, data)
		}
	}
}
