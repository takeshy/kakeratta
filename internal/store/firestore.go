package store

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreWorkspace keeps one user's structured state in Firestore. Its root
// is only a disposable materialisation directory for components that consume
// installed skill files and browser-only OAuth helpers.
type FirestoreWorkspace struct {
	client  *firestore.Client
	user    *firestore.DocumentRef
	root    string
	blobs   BlobStore
	indexMu sync.Mutex
	indexed map[string]bool
	// stopWatch ends the skills change feed; closeOnce guards Close.
	stopWatch context.CancelFunc
	closeOnce sync.Once
}

func NewFirestoreWorkspace(ctx context.Context, client *firestore.Client, userKey string, blobs BlobStore) (*FirestoreWorkspace, error) {
	root, err := os.MkdirTemp("", "kakeratta-user-")
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"skills", "memory", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return nil, err
		}
	}
	w := &FirestoreWorkspace{client: client, user: client.Collection("users").Doc(userKey), root: root, blobs: blobs, indexed: map[string]bool{}}
	if w.blobs == nil {
		w.blobs = fileBlobs{root: filepath.Join(root, "blobs")}
	}
	if err := w.materialiseSkills(ctx); err != nil {
		return nil, err
	}
	watch, stop := context.WithCancel(context.Background())
	w.stopWatch = stop
	go w.watchSkills(watch)
	return w, nil
}

// Close stops following the workspace and removes its local materialisation.
// The workspace itself stays where it is kept.
func (w *FirestoreWorkspace) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.stopWatch()
		err = os.RemoveAll(w.root)
	})
	return err
}

// watchSkills keeps the materialised persona definitions current. Another
// instance serving the same user may install, edit or remove one; the
// catalogue reads this directory, so it sees the change here too.
func (w *FirestoreWorkspace) watchSkills(ctx context.Context) {
	for ctx.Err() == nil {
		iter := w.user.Collection("skills").Snapshots(ctx)
		for {
			snapshot, err := iter.Next()
			if err != nil {
				break
			}
			for _, change := range snapshot.Changes {
				dir := filepath.Join(w.SkillDir(), sanitise(change.Doc.Ref.ID))
				if change.Kind == firestore.DocumentRemoved {
					_ = os.RemoveAll(dir)
					continue
				}
				raw, _ := change.Doc.Data()["raw"].(string)
				if raw == "" {
					continue
				}
				if err := os.MkdirAll(dir, 0o755); err == nil {
					_ = writeFileAtomic(filepath.Join(dir, "skill.json"), []byte(raw), 0o600)
				}
			}
		}
		iter.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *FirestoreWorkspace) Root() string     { return w.root }
func (w *FirestoreWorkspace) SkillDir() string { return filepath.Join(w.root, "skills") }

func (w *FirestoreWorkspace) timeline() *firestore.CollectionRef {
	return w.user.Collection("timeline")
}

func (w *FirestoreWorkspace) Append(entry timeline.Entry) error {
	if entry.Deleted {
		_, err := w.DeleteEntry(context.Background(), entry.ID)
		if errors.Is(err, ErrEntryMissing) {
			return nil
		}
		return err
	}
	entry.SearchDirty = true
	if entry.Kind == timeline.KindPost {
		_, err := w.timeline().Doc(entry.ID).Set(context.Background(), entry)
		return err
	}
	return w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		if err := w.checkEntryParents(tx, entry); err != nil {
			return err
		}
		return tx.Set(w.timeline().Doc(entry.ID), entry)
	})
}

func (w *FirestoreWorkspace) Timeline() ([]timeline.Entry, error) {
	iter := w.timeline().OrderBy("At", firestore.Asc).Documents(context.Background())
	defer iter.Stop()
	var entries []timeline.Entry
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var entry timeline.Entry
		if err := doc.DataTo(&entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (w *FirestoreWorkspace) Entry(id string) (timeline.Entry, bool) {
	doc, err := w.timeline().Doc(id).Get(context.Background())
	if err != nil {
		return timeline.Entry{}, false
	}
	var entry timeline.Entry
	if doc.DataTo(&entry) != nil {
		return timeline.Entry{}, false
	}
	return entry, true
}

func (w *FirestoreWorkspace) UpdateMedia(id string, media *timeline.Media) (timeline.Entry, error) {
	var result timeline.Entry
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(w.timeline().Doc(id))
		if err != nil {
			return err
		}
		if err := doc.DataTo(&result); err != nil {
			return err
		}
		if result.Deleted {
			return nil
		}
		result.Media = media
		result.SearchDirty = true
		return tx.Set(w.timeline().Doc(id), result)
	})
	return result, err
}

func (w *FirestoreWorkspace) UpdateText(id, text string) (timeline.Entry, error) {
	var result timeline.Entry
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(w.timeline().Doc(id))
		if err != nil {
			return err
		}
		if err := doc.DataTo(&result); err != nil {
			return err
		}
		if err := editText(&result, text); err != nil {
			return err
		}
		return tx.Set(w.timeline().Doc(id), result)
	})
	return result, err
}

func (w *FirestoreWorkspace) PutBlob(data []byte, mime string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("空のファイルです")
	}
	name := NewID("blob") + extensionFor(mime)
	if err := w.blobs.Put(name, data, mime); err != nil {
		return "", err
	}
	return name, nil
}

func (w *FirestoreWorkspace) Blob(id string) ([]byte, string, error) {
	if !safeName(id) {
		return nil, "", errors.New("不正なファイル名です")
	}
	return w.blobs.Get(id)
}

func (w *FirestoreWorkspace) DeleteBlob(id string) error {
	if !safeName(id) {
		return errors.New("不正なファイル名です")
	}
	return w.blobs.Delete(id)
}

func (w *FirestoreWorkspace) persona(id string) *firestore.DocumentRef {
	return w.user.Collection("personas").Doc(sanitise(id))
}

func (w *FirestoreWorkspace) Memory(personaID string) Memory {
	memory := w.MemoryMeta(personaID)
	iter := w.persona(personaID).Collection("memory").Documents(context.Background())
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if err != nil {
			break
		}
		var note Note
		if doc.DataTo(&note) == nil {
			memory.Notes = append(memory.Notes, note)
		}
	}
	sort.Slice(memory.Notes, func(i, j int) bool { return memory.Notes[i].Path < memory.Notes[j].Path })
	return memory
}

// MemoryMeta is what Memory says about a persona, without reading a note.
func (w *FirestoreWorkspace) MemoryMeta(personaID string) Memory {
	memory := Memory{PersonaID: personaID}
	if doc, err := w.persona(personaID).Get(context.Background()); err == nil {
		_ = doc.DataTo(&memory)
	}
	memory.PersonaID = personaID
	memory.Notes = nil
	return memory
}

// MemoryPage reads up to limit notes that match accepts (nil accepts every
// note), the most recently written first, continuing after the cursor after.
// Without match, Firestore is asked for no more than the page needs. Notes
// written at the same moment are told apart by document id, which the
// single-field index on Written already orders, so no composite index is
// needed.
func (w *FirestoreWorkspace) MemoryPage(personaID, after string, limit int, match func(Note) bool) (MemoryPage, error) {
	cursor, err := decodeNoteCursor(after)
	if err != nil {
		return MemoryPage{}, err
	}
	ctx := context.Background()
	page := MemoryPage{Memory: w.MemoryMeta(personaID)}
	if after == "" {
		paths, err := w.notePaths(ctx, personaID)
		if err != nil {
			return page, err
		}
		page.Paths = paths
	}
	notes := w.persona(personaID).Collection("memory")
	query := notes.OrderBy("Written", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	if after != "" {
		query = query.StartAfter(cursor.Written, notes.Doc(noteID(cursor.Path)))
	}
	if match == nil {
		query = query.Limit(limit + 1)
	}
	docs := query.Documents(ctx)
	defer docs.Stop()
	var failed error
	page.Notes, page.Next = notesPage(func(yield func(Note) bool) {
		for {
			doc, err := docs.Next()
			if errors.Is(err, iterator.Done) {
				return
			}
			if err != nil {
				failed = err
				return
			}
			var note Note
			if doc.DataTo(&note) == nil && !yield(note) {
				return
			}
		}
	}, limit, match)
	return page, failed
}

func (w *FirestoreWorkspace) NotePaths(personaID string) ([]string, error) {
	return w.notePaths(context.Background(), personaID)
}

// Notes reads the notes named in one round trip, in the order given; a name
// with no note is left out.
func (w *FirestoreWorkspace) Notes(personaID string, paths []string) ([]Note, error) {
	var refs []*firestore.DocumentRef
	for _, path := range paths {
		if clean, err := notePath(path); err == nil {
			refs = append(refs, w.persona(personaID).Collection("memory").Doc(noteID(clean)))
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	docs, err := w.client.GetAll(context.Background(), refs)
	if err != nil {
		return nil, err
	}
	var notes []Note
	for _, doc := range docs {
		var note Note
		if doc.Exists() && doc.DataTo(&note) == nil {
			notes = append(notes, note)
		}
	}
	return notes, nil
}

// notePaths names every note a persona kept, in name order. The document ids
// are the names, so no note is read.
func (w *FirestoreWorkspace) notePaths(ctx context.Context, personaID string) ([]string, error) {
	refs := w.persona(personaID).Collection("memory").DocumentRefs(ctx)
	var paths []string
	for {
		ref, err := refs.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if path, err := base64.RawURLEncoding.DecodeString(ref.ID); err == nil {
			paths = append(paths, string(path))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func noteID(path string) string { return base64.RawURLEncoding.EncodeToString([]byte(path)) }

type noteSourceRecord struct {
	EntryID string   `firestore:"entryId"`
	Persona string   `firestore:"persona"`
	Paths   []string `firestore:"paths"`
}

func noteSourceID(entryID, persona string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(entryID + "\x00" + persona))
}

func (w *FirestoreWorkspace) noteSource(entryID, persona string) *firestore.DocumentRef {
	return w.user.Collection("noteSources").Doc(noteSourceID(entryID, persona))
}

func (w *FirestoreWorkspace) Note(personaID, name string) (Note, bool) {
	clean, err := notePath(name)
	if err != nil {
		return Note{}, false
	}
	doc, err := w.persona(personaID).Collection("memory").Doc(noteID(clean)).Get(context.Background())
	if err != nil {
		return Note{}, false
	}
	var note Note
	return note, doc.DataTo(&note) == nil
}

func (w *FirestoreWorkspace) WriteNote(personaID, name, text string, sources ...NoteSource) error {
	clean, err := notePath(name)
	if err != nil {
		return err
	}
	if len(text) > maxNoteBytes {
		return errors.New("メモが大きすぎます")
	}
	if paths, err := w.notePaths(context.Background(), personaID); err != nil {
		return err
	} else if len(paths) >= maxNotes {
		if _, exists := w.Note(personaID, clean); !exists {
			return errors.New("メモが多すぎます")
		}
	}
	ref := w.persona(personaID).Collection("memory").Doc(noteID(clean))
	var before, after []string
	err = w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		previous := Note{}
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			if err := doc.DataTo(&previous); err != nil {
				return err
			}
		}
		merged := withNoteSources(previous.Text, text, sources)
		if reservedNote(clean) {
			merged, _ = splitNoteSources(merged)
		}
		if len(merged) > maxNoteBytes {
			return errors.New("メモが大きすぎます")
		}
		before, after = sourceIDs(previous.Text), sourceIDs(merged)
		return tx.Set(ref, Note{Path: clean, Text: merged, Written: time.Now()})
	})
	if err != nil {
		return err
	}
	return w.updateNoteSourceIndex(personaID, clean, before, after)
}

func (w *FirestoreWorkspace) updateNoteSourceIndex(persona, path string, before, after []string) error {
	old, next := map[string]bool{}, map[string]bool{}
	for _, id := range before {
		old[id] = true
	}
	for _, id := range after {
		next[id] = true
	}
	ctx := context.Background()
	for id := range old {
		if next[id] {
			continue
		}
		_, err := w.noteSource(id, persona).Set(ctx, map[string]any{"entryId": id, "persona": persona, "paths": firestore.ArrayRemove(path)}, firestore.MergeAll)
		if err != nil {
			return err
		}
	}
	for id := range next {
		if old[id] {
			continue
		}
		_, err := w.noteSource(id, persona).Set(ctx, map[string]any{"entryId": id, "persona": persona, "paths": firestore.ArrayUnion(path)}, firestore.MergeAll)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *FirestoreWorkspace) NotesBySource(personaID, entryID string) ([]string, error) {
	if err := w.ensureNoteSourceIndex(personaID); err != nil {
		return nil, err
	}
	doc, err := w.noteSource(entryID, personaID).Get(context.Background())
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record noteSourceRecord
	if err := doc.DataTo(&record); err != nil {
		return nil, err
	}
	paths := append([]string(nil), record.Paths...)
	sort.Strings(paths)
	return paths, nil
}

func (w *FirestoreWorkspace) ensureNoteSourceIndex(persona string) error {
	w.indexMu.Lock()
	defer w.indexMu.Unlock()
	if w.indexed[persona] {
		return nil
	}
	ctx := context.Background()
	marker := w.user.Collection("noteSourceIndexMigrations").Doc(sanitise(persona))
	if _, err := marker.Get(ctx); err == nil {
		w.indexed[persona] = true
		return nil
	} else if status.Code(err) != codes.NotFound {
		return err
	}
	for _, note := range w.Memory(persona).Notes {
		if err := w.updateNoteSourceIndex(persona, note.Path, nil, sourceIDs(note.Text)); err != nil {
			return err
		}
	}
	if _, err := marker.Set(ctx, map[string]any{"indexedAt": time.Now()}); err != nil {
		return err
	}
	w.indexed[persona] = true
	return nil
}

func (w *FirestoreWorkspace) DropNoteSource(personaID, entryID string) ([]string, error) {
	if personaID == "" || entryID == "" {
		return nil, nil
	}
	touched, err := w.NotesBySource(personaID, entryID)
	if err != nil {
		return nil, err
	}
	for _, path := range touched {
		note, ok := w.Note(personaID, path)
		if !ok {
			continue
		}
		next, changed := withoutNoteSource(note.Text, entryID)
		if !changed {
			continue
		}
		clean, err := notePath(note.Path)
		if err != nil {
			continue
		}
		ref := w.persona(personaID).Collection("memory").Doc(noteID(clean))
		if _, err := ref.Set(context.Background(), Note{Path: clean, Text: next, Written: time.Now()}); err != nil {
			return touched, err
		}
	}
	_, err = w.noteSource(entryID, personaID).Delete(context.Background())
	if err != nil && status.Code(err) != codes.NotFound {
		return touched, err
	}
	sort.Strings(touched)
	return touched, nil
}

func (w *FirestoreWorkspace) DeleteNote(personaID, name string) error {
	clean, err := notePath(name)
	if err != nil {
		return err
	}
	note, _ := w.Note(personaID, clean)
	_, err = w.persona(personaID).Collection("memory").Doc(noteID(clean)).Delete(context.Background())
	if err != nil {
		return err
	}
	return w.updateNoteSourceIndex(personaID, clean, sourceIDs(note.Text), nil)
}

func (w *FirestoreWorkspace) SaveMemory(memory Memory) error {
	memory.PersonaID, memory.Notes, memory.UpdatedAt = sanitise(memory.PersonaID), nil, time.Now()
	_, err := w.persona(memory.PersonaID).Set(context.Background(), memory)
	return err
}

func (w *FirestoreWorkspace) ForgetMemory(personaID string) error {
	iter := w.persona(personaID).Collection("memory").Documents(context.Background())
	defer iter.Stop()
	batch := w.client.Batch()
	count := 0
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return err
		}
		batch.Delete(doc.Ref)
		count++
		if count == 400 {
			if _, err := batch.Commit(context.Background()); err != nil {
				return err
			}
			batch, count = w.client.Batch(), 0
		}
	}
	batch.Delete(w.persona(personaID))
	if _, err := batch.Commit(context.Background()); err != nil {
		return err
	}
	refs, err := w.user.Collection("noteSources").Where("persona", "==", personaID).Documents(context.Background()).GetAll()
	if err != nil {
		return err
	}
	for start := 0; start < len(refs); start += 400 {
		end := start + 400
		if end > len(refs) {
			end = len(refs)
		}
		cleanup := w.client.Batch()
		for _, doc := range refs[start:end] {
			cleanup.Delete(doc.Ref)
		}
		if _, err := cleanup.Commit(context.Background()); err != nil {
			return err
		}
	}
	_, err = w.user.Collection("noteSourceIndexMigrations").Doc(sanitise(personaID)).Delete(context.Background())
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	w.indexMu.Lock()
	delete(w.indexed, personaID)
	w.indexMu.Unlock()
	return nil
}

func (w *FirestoreWorkspace) ConfiguredSettings() Settings {
	settings := defaultSettings()
	if doc, err := w.user.Collection("config").Doc("settings").Get(context.Background()); err == nil {
		_ = doc.DataTo(&settings)
	}
	return normaliseConfiguredSettings(settings)
}

func (w *FirestoreWorkspace) Settings() Settings { return effectiveSettings(w.ConfiguredSettings()) }

func (w *FirestoreWorkspace) SaveSettings(settings Settings) error {
	// Parent documents make configured workspaces discoverable after a restart.
	if _, err := w.user.Set(context.Background(), map[string]any{"configured": true}, firestore.MergeAll); err != nil {
		return err
	}
	_, err := w.user.Collection("config").Doc("settings").Set(context.Background(), settings)
	return err
}

func (w *FirestoreWorkspace) PersistSkill(id string, raw []byte) error {
	_, err := w.user.Collection("skills").Doc(sanitise(id)).Set(context.Background(), map[string]any{"raw": string(raw)})
	return err
}

func (w *FirestoreWorkspace) DeleteSkill(id string) error {
	_, err := w.user.Collection("skills").Doc(sanitise(id)).Delete(context.Background())
	return err
}

func (w *FirestoreWorkspace) materialiseSkills(ctx context.Context) error {
	iter := w.user.Collection("skills").Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		raw, _ := doc.Data()["raw"].(string)
		if raw == "" {
			continue
		}
		dir := filepath.Join(w.SkillDir(), sanitise(doc.Ref.ID))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "skill.json"), []byte(raw), 0o600); err != nil {
			return err
		}
	}
}

func (w *FirestoreWorkspace) CreateEntry(entry timeline.Entry) (timeline.Entry, bool, error) {
	if entry.Deleted {
		return timeline.Entry{}, false, errors.New("deletion notifications cannot be stored")
	}
	entry.SearchDirty = true
	var result timeline.Entry
	var created bool
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		created = false
		doc, err := tx.Get(w.timeline().Doc(entry.ID))
		if err == nil {
			return doc.DataTo(&result)
		}
		if status.Code(err) != codes.NotFound {
			return err
		}
		if err := w.checkEntryParents(tx, entry); err != nil {
			return err
		}
		result, created = entry, true
		return tx.Create(w.timeline().Doc(entry.ID), entry)
	})
	return result, created && err == nil, err
}
