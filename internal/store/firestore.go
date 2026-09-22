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
	return w, nil
}

func (w *FirestoreWorkspace) Root() string     { return w.root }
func (w *FirestoreWorkspace) SkillDir() string { return filepath.Join(w.root, "skills") }

func (w *FirestoreWorkspace) timeline() *firestore.CollectionRef {
	return w.user.Collection("timeline")
}

func (w *FirestoreWorkspace) Append(entry timeline.Entry) error {
	entry.SearchDirty = true
	_, err := w.timeline().Doc(entry.ID).Set(context.Background(), entry)
	return err
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

func (w *FirestoreWorkspace) persona(id string) *firestore.DocumentRef {
	return w.user.Collection("personas").Doc(sanitise(id))
}

func (w *FirestoreWorkspace) Memory(personaID string) Memory {
	memory := Memory{PersonaID: personaID}
	if doc, err := w.persona(personaID).Get(context.Background()); err == nil {
		_ = doc.DataTo(&memory)
	}
	memory.PersonaID = personaID
	memory.Notes = nil
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
	if len(w.Memory(personaID).Notes) >= maxNotes {
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
	entry.SearchDirty = true
	_, err := w.timeline().Doc(entry.ID).Create(context.Background(), entry)
	if status.Code(err) != codes.AlreadyExists {
		return entry, err == nil, err
	}
	doc, err := w.timeline().Doc(entry.ID).Get(context.Background())
	if err != nil {
		return timeline.Entry{}, false, err
	}
	var old timeline.Entry
	err = doc.DataTo(&old)
	return old, false, err
}
