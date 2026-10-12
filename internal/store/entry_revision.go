package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/takeshy/kakeratta/internal/timeline"
)

var ErrEntryConflict = errors.New("投稿が同時に変更されています")

// EntryRevision excludes internal bookkeeping, but includes every displayed field.
func EntryRevision(entry timeline.Entry) string {
	entry.RequestHash = ""
	entry.SearchDirty = false
	data, _ := json.Marshal(entry)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DeletionRevision also protects descendants newly added or edited after a read.
func DeletionRevision(entries []timeline.Entry) string {
	revisions := make([]string, 0, len(entries))
	for _, entry := range entries {
		revisions = append(revisions, entry.ID+":"+EntryRevision(entry))
	}
	sort.Strings(revisions)
	data, _ := json.Marshal(revisions)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
