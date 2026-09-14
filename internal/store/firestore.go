package store

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"
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
	client *firestore.Client
	user   *firestore.DocumentRef
	root   string
	blobs  BlobStore
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
	w := &FirestoreWorkspace{client: client, user: client.Collection("users").Doc(userKey), root: root, blobs: blobs}
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
	return w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
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
		if len(merged) > maxNoteBytes {
			return errors.New("メモが大きすぎます")
		}
		return tx.Set(ref, Note{Path: clean, Text: merged, Written: time.Now()})
	})
}

func (w *FirestoreWorkspace) DeleteNote(personaID, name string) error {
	clean, err := notePath(name)
	if err != nil {
		return err
	}
	_, err = w.persona(personaID).Collection("memory").Doc(noteID(clean)).Delete(context.Background())
	return err
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
	_, err := batch.Commit(context.Background())
	return err
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
