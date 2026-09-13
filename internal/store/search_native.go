package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrSearchEntryChanged = errors.New("投稿が更新されました。もう一度検索してください")

type SearchIndexEntry struct {
	Entry   timeline.Entry
	Updated time.Time
}

// SearchPassage uses Firestore's vector type, not an ordinary numeric array.
type SearchPassage struct {
	EntryID   string             `firestore:"entryId"`
	RootID    string             `firestore:"rootId"`
	ModelKey  string             `firestore:"modelKey"`
	Hash      string             `firestore:"hash"`
	Text      string             `firestore:"text"`
	Embedding firestore.Vector64 `firestore:"embedding"`
	Distance  float64            `firestore:"distance,omitempty"`
}

func (w *FirestoreWorkspace) searchPassages() *firestore.CollectionRef {
	return w.user.Collection("searchPassages")
}

// PurgeSearchThread removes the deleted root's own passages and its replies so
// hidden conversations cannot occupy the nearest-neighbor candidate window.
func (w *FirestoreWorkspace) PurgeSearchThread(ctx context.Context, rootID string) error {
	docs, err := w.searchPassages().Where("rootId", "==", rootID).Select().Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	for start := 0; start < len(docs); start += 400 {
		batch := w.client.Batch()
		for _, doc := range docs[start:min(start+400, len(docs))] {
			batch.Delete(doc.Ref)
		}
		if _, err := batch.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// PendingSearchEntries scans the timeline once for legacy data or a changed
// model. Subsequent searches read only the entries marked dirty by writes.
func (w *FirestoreWorkspace) PendingSearchEntries(ctx context.Context, modelKey string) ([]SearchIndexEntry, error) {
	state, err := w.user.Collection("searchState").Doc("current").Get(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return nil, err
	}
	if err != nil || state.Data()["modelKey"] != modelKey {
		// Seed durable pending flags before generating any embeddings. A slow
		// first search can then resume from unfinished entries, rather than
		// repeatedly revisiting the already indexed prefix of the timeline.
		docs, err := w.timeline().Select().Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		for start := 0; start < len(docs); start += 400 {
			batch := w.client.Batch()
			for _, doc := range docs[start:min(start+400, len(docs))] {
				batch.Update(doc.Ref, []firestore.Update{{Path: "SearchDirty", Value: true}})
			}
			if _, err := batch.Commit(ctx); err != nil {
				return nil, err
			}
		}
		if _, err := w.user.Collection("searchState").Doc("current").Set(ctx, map[string]any{"modelKey": modelKey}); err != nil {
			return nil, err
		}
	}
	docs, err := w.timeline().Where("SearchDirty", "==", true).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	entries := make([]SearchIndexEntry, 0, len(docs))
	for _, doc := range docs {
		var entry timeline.Entry
		if err := doc.DataTo(&entry); err != nil {
			return nil, err
		}
		entries = append(entries, SearchIndexEntry{Entry: entry, Updated: doc.UpdateTime})
	}
	return entries, nil
}

func (w *FirestoreWorkspace) IndexedSearchPassages(ctx context.Context, entryID string) ([]SearchPassage, error) {
	docs, err := w.searchPassages().Where("entryId", "==", entryID).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	passages := make([]SearchPassage, 0, len(docs))
	for _, doc := range docs {
		var passage SearchPassage
		if err := doc.DataTo(&passage); err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	return passages, nil
}

// ReplaceSearchPassages atomically replaces an entry's chunks and acknowledges
// its dirty flag. A write during embedding generation cannot clear newer work.
func (w *FirestoreWorkspace) ReplaceSearchPassages(ctx context.Context, candidate SearchIndexEntry, passages []SearchPassage) error {
	old, err := w.searchPassages().Where("entryId", "==", candidate.Entry.ID).Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	return w.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(w.timeline().Doc(candidate.Entry.ID))
		if err != nil {
			return err
		}
		if !doc.UpdateTime.Equal(candidate.Updated) {
			return ErrSearchEntryChanged
		}
		keep := map[string]bool{}
		for i, passage := range passages {
			id := fmt.Sprintf("%x-%04d", sha256.Sum256([]byte(candidate.Entry.ID)), i)
			keep[id] = true
			if err := tx.Set(w.searchPassages().Doc(id), passage); err != nil {
				return err
			}
		}
		for _, previous := range old {
			if !keep[previous.Ref.ID] {
				if err := tx.Delete(previous.Ref); err != nil {
					return err
				}
			}
		}
		return tx.Update(doc.Ref, []firestore.Update{{Path: "SearchDirty", Value: false}})
	})
}

// NearestSearchPassages delegates ranking to Firestore's vector index. The
// collection is scoped to this user; modelKey prevents mixed embedding spaces.
func (w *FirestoreWorkspace) NearestSearchPassages(ctx context.Context, modelKey string, vector []float64, limit int) ([]SearchPassage, error) {
	docs, err := w.searchPassages().Where("modelKey", "==", modelKey).
		Select("entryId", "hash", "text", "distance").
		FindNearest("embedding", vector, limit, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceResultField: "distance"}).Documents(ctx).GetAll()
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			return nil, errors.New("Firestoreの検索インデックスが必要です。firestore.indexes.jsonをデプロイし、作成完了後に再検索してください")
		}
		return nil, err
	}
	hits := make([]SearchPassage, 0, len(docs))
	for _, doc := range docs {
		var passage SearchPassage
		if err := doc.DataTo(&passage); err != nil {
			return nil, err
		}
		hits = append(hits, passage)
	}
	return hits, nil
}

func (w *FirestoreWorkspace) CurrentSearchEntry(ctx context.Context, id string) (timeline.Entry, bool, error) {
	doc, err := w.timeline().Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return timeline.Entry{}, false, nil
	}
	if err != nil {
		return timeline.Entry{}, false, err
	}
	var entry timeline.Entry
	if err := doc.DataTo(&entry); err != nil {
		return timeline.Entry{}, false, err
	}
	return entry, !entry.Deleted, nil
}
