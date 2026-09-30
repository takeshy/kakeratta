package store

// Destroy is the one operation here that takes away rather than adds. A user
// who stops using kakeratta is owed a workspace that is gone, not one that is
// merely unreachable: the posts, the personas and every note they wrote, the
// captures wherever the blob store put them, the router's calls, the wakes and
// schedules, the search index, the settings with their keys, the Bluesky and
// MCP connections and the record of what was published. Everything under the
// workspace, and nothing outside it.

import (
	"context"
	"errors"
	"os"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// Destroy removes the file-backed workspace: the captures through the blob
// store, which may be object storage, then the directory itself.
func (s *Store) Destroy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.blobs.Purge(); err != nil {
		return err
	}
	return os.RemoveAll(s.root)
}

// Destroy removes the user's document with every collection under it, the
// captures, and the directory the skills were materialised into.
func (w *FirestoreWorkspace) Destroy(ctx context.Context) error {
	// The skills feed would otherwise write definitions back into the
	// directory removed below.
	if w.stopWatch != nil {
		w.stopWatch()
	}
	if err := deleteDocument(ctx, w.client, w.user); err != nil {
		return err
	}
	if err := w.blobs.Purge(); err != nil {
		return err
	}
	return os.RemoveAll(w.root)
}

// deleteDocument removes a document and everything beneath it. Firestore does
// not delete a subcollection with its parent, so each one is walked; the
// collections are discovered rather than listed here, so a collection added
// later is not left behind.
func deleteDocument(ctx context.Context, client *firestore.Client, doc *firestore.DocumentRef) error {
	if err := deleteCollectionsUnder(ctx, client, doc); err != nil {
		return err
	}
	_, err := doc.Delete(ctx)
	return err
}

func deleteCollectionsUnder(ctx context.Context, client *firestore.Client, doc *firestore.DocumentRef) error {
	collections := doc.Collections(ctx)
	for {
		collection, err := collections.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := deleteCollection(ctx, client, collection); err != nil {
			return err
		}
	}
}

// deleteCollection removes every document in a collection, subcollections
// first. DocumentRefs lists missing documents too, so a parent that was only
// ever a path to its notes is still walked.
func deleteCollection(ctx context.Context, client *firestore.Client, collection *firestore.CollectionRef) error {
	refs := collection.DocumentRefs(ctx)
	batch := client.Batch()
	count := 0
	for {
		ref, err := refs.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return err
		}
		if err := deleteCollectionsUnder(ctx, client, ref); err != nil {
			return err
		}
		batch.Delete(ref)
		count++
		if count == 400 {
			if _, err := batch.Commit(ctx); err != nil {
				return err
			}
			batch, count = client.Batch(), 0
		}
	}
	if count == 0 {
		return nil
	}
	_, err := batch.Commit(ctx)
	return err
}
