// Package store keeps everything on disk as plain files under one directory.
//
// The timeline keeps current entries in one JSON file per calendar day.
// Updates atomically replace only that day; an ID/thread index locates records
// without rereading the entire workspace.
//
// No database and no account. What the user captured is files they can read,
// copy and back up.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// Store is the on-disk workspace.
type Store struct {
	root            string
	blobs           BlobStore
	noteSources     noteSourceIndex
	timelineRefs    map[string]timelineRef
	timelineThreads map[string]map[string]bool
	mu              sync.RWMutex
	// locks are the named locks of Lock; closing is SetClosing's mark.
	locks   sync.Map
	closing bool
}

func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("store root is empty")
	}
	for _, dir := range []string{"blobs", "skills", "memory"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return nil, err
		}
	}
	index, err := loadNoteSourceIndex(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root, blobs: fileBlobs{root: filepath.Join(root, "blobs")}, noteSources: index}
	if err := s.openTimeline(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) SetBlobStore(blobs BlobStore) {
	if blobs == nil {
		return
	}
	s.mu.Lock()
	s.blobs = blobs
	s.mu.Unlock()
}

// DefaultRoot is where the workspace lives when none is given.
func DefaultRoot() string {
	if dir := os.Getenv("KAKERATTA_HOME"); dir != "" {
		return dir
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "kakeratta")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kakeratta")
}

func (s *Store) Root() string { return s.root }

func (s *Store) path(parts ...string) string {
	return filepath.Join(append([]string{s.root}, parts...)...)
}

// ---- blobs -----------------------------------------------------------------
//
// Captures live beside the log as ordinary files. The interface is narrow on
// purpose: the deployment target keeps them in object storage instead, and this
// is the whole of what has to be swapped to get there.

// PutBlob stores bytes and returns the id to reference them by.
func (s *Store) PutBlob(data []byte, mime string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("空のファイルです")
	}
	id := NewID("blob")
	name := id + extensionFor(mime)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.blobs.Put(name, data, mime); err != nil {
		return "", err
	}
	return name, nil
}

// Blob reads one back.
func (s *Store) Blob(id string) ([]byte, string, error) {
	if !safeName(id) {
		return nil, "", errors.New("不正なファイル名です")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blobs.Get(id)
}

// DeleteBlob removes a capture nothing refers to any longer.
func (s *Store) DeleteBlob(id string) error {
	if !safeName(id) {
		return errors.New("不正なファイル名です")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blobs.Delete(id)
}

// dropBlobs removes the captures of entries that are gone. It runs after the
// entries themselves are deleted: a blob left behind by a failure here is
// unreferenced, never a post pointing at a missing image.
func dropBlobs(delete func(string) error, gone []timeline.Entry) {
	for _, entry := range gone {
		if entry.Media != nil && entry.Media.BlobID != "" && safeName(entry.Media.BlobID) {
			if err := delete(entry.Media.BlobID); err != nil {
				log.Printf("blob %s: %v", entry.Media.BlobID, err)
			}
		}
	}
}

func extensionFor(mime string) string {
	switch {
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
	case strings.Contains(mime, "avif"):
		return ".avif"
	case strings.Contains(mime, "heic"):
		return ".heic"
	case strings.Contains(mime, "heif"):
		return ".heif"
	default:
		return ".jpg"
	}
}

func mimeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	default:
		return "image/jpeg"
	}
}

func safeName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return true
}

// ---- persona memory --------------------------------------------------------

// Memory is one persona's private notes: named pieces of text it wrote itself.
//
// The app knows they have names and nothing else. It does not read them, does
// not know what is in one, and has no threshold about how many there should be
// or how long they may get. What to write, what to keep, what to throw away and
// when to compress are the persona's own business, written in its skill.
//
// Names exist for one reason: so a persona can change one piece without
// rewriting all of them. Rewriting everything on every wake sends the whole
// memory out and back each time, and anything not retyped disappears with no
// trace. Nothing here depends on what the names mean.
//
// It is also the only past there is. A persona is never handed the timeline
// again, and there is no path back to it: if something is not in here, this
// persona decided it was not worth keeping.
//
// They are ordinary files on disk, so the notes can be read - or pointed at by
// anything else that reads a directory of markdown - without this app.
type Memory struct {
	PersonaID string    `json:"personaId"`
	Notes     []Note    `json:"notes"`
	JoinedAt  time.Time `json:"joinedAt"`
	Wakes     int       `json:"wakes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Note is one named piece.
type Note struct {
	Path    string    `json:"path"`
	Text    string    `json:"text"`
	Written time.Time `json:"written"`
}

// The only limits the app puts on a persona's notes. All three are about not
// letting a model fill a disk, never about what is worth keeping.
const (
	maxNotes     = 2000
	maxNoteBytes = 256 << 10
	maxNoteDepth = 6
)

// Memory reads everything a persona kept, in name order.
func (s *Store) Memory(personaID string) Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	memory := s.memoryMetaLocked(personaID)
	for _, path := range s.notePathsLocked(personaID) {
		if note, ok := s.readNoteLocked(personaID, path); ok {
			memory.Notes = append(memory.Notes, note)
		}
	}
	return memory
}

// MemoryMeta is what Memory says about a persona, without reading a note.
func (s *Store) MemoryMeta(personaID string) Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memoryMetaLocked(personaID)
}

// MemoryPage reads up to limit notes that match accepts (nil accepts every
// note), the most recently written first, continuing after the cursor after.
// Only the notes on the page, and the ones passed over to fill it, are read.
func (s *Store) MemoryPage(personaID, after string, limit int, match func(Note) bool) (MemoryPage, error) {
	cursor, err := decodeNoteCursor(after)
	if err != nil {
		return MemoryPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page := MemoryPage{Memory: s.memoryMetaLocked(personaID)}
	var stamps []Note
	root := s.memoryDir(personaID)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		info, infoErr := entry.Info()
		if err == nil && infoErr == nil {
			stamps = append(stamps, Note{Path: filepath.ToSlash(relative), Written: info.ModTime()})
		}
		return nil
	})
	if after == "" {
		for _, stamp := range stamps {
			page.Paths = append(page.Paths, stamp.Path)
		}
		sort.Strings(page.Paths)
	}
	sort.Slice(stamps, func(i, j int) bool { return newerNote(stamps[i], stamps[j]) })
	last := Note{Path: cursor.Path, Written: cursor.Written}
	page.Notes, page.Next = notesPage(func(yield func(Note) bool) {
		for _, stamp := range stamps {
			if after != "" && !newerNote(last, stamp) {
				continue
			}
			if note, ok := s.readNoteLocked(personaID, stamp.Path); ok && !yield(note) {
				return
			}
		}
	}, limit, match)
	return page, nil
}

// NotePaths names every note a persona kept, in name order, without reading
// one.
func (s *Store) NotePaths(personaID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notePathsLocked(personaID), nil
}

// Notes reads the notes named, in the order given; a name with no note is
// left out.
func (s *Store) Notes(personaID string, paths []string) ([]Note, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var notes []Note
	for _, path := range paths {
		clean, err := notePath(path)
		if err != nil {
			continue
		}
		if note, ok := s.readNoteLocked(personaID, clean); ok {
			notes = append(notes, note)
		}
	}
	return notes, nil
}

func (s *Store) memoryMetaLocked(personaID string) Memory {
	memory := Memory{PersonaID: personaID}
	_ = readJSON(s.memoryMeta(personaID), &memory)
	memory.PersonaID = personaID
	memory.Notes = nil
	return memory
}

// notePathsLocked names every note a persona kept, in name order, without
// reading one.
func (s *Store) notePathsLocked(personaID string) []string {
	root := s.memoryDir(personaID)
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		if relative, err := filepath.Rel(root, path); err == nil {
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

func (s *Store) readNoteLocked(personaID, path string) (Note, bool) {
	target := filepath.Join(s.memoryDir(personaID), filepath.FromSlash(path))
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return Note{}, false
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return Note{}, false
	}
	return Note{Path: path, Text: string(raw), Written: info.ModTime()}, true
}

// Note returns one piece by name.
func (s *Store) Note(personaID, path string) (Note, bool) {
	clean, err := notePath(path)
	if err != nil {
		return Note{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readNoteLocked(personaID, clean)
}

// WriteNote stores one piece under the name the persona chose, leaving every
// other piece exactly as it was.
//
// The name comes from a model, so it is checked - not for what it means, but so
// that a persona cannot write outside its own notes.
func (s *Store) WriteNote(personaID, path, text string, sources ...NoteSource) error {
	clean, err := notePath(path)
	if err != nil {
		return err
	}
	if len(text) > maxNoteBytes {
		return fmt.Errorf("メモが大きすぎます: %s", clean)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := filepath.Join(s.memoryDir(personaID), filepath.FromSlash(clean))
	previous, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) && len(s.notePathsLocked(personaID)) >= maxNotes {
		return errors.New("メモが多すぎます")
	}
	text = withNoteSources(string(previous), text, sources)
	if reservedNote(clean) {
		// The index and the log are about the whole memory, not a post.
		text, _ = splitNoteSources(text)
	}
	if len(text) > maxNoteBytes {
		return fmt.Errorf("メモが大きすぎます: %s", clean)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := writeNoteFile(target, text); err != nil {
		return err
	}
	indexSet(s.noteSources, personaID, clean, sourceIDs(string(previous)), sourceIDs(text))
	return s.writeNoteSourceIndexLocked()
}

// NotesBySource uses the maintained reverse index; it never opens unrelated
// notes. Returned paths are sorted and safe to pass to Note.
func (s *Store) NotesBySource(personaID, entryID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	paths := append([]string(nil), s.noteSources[entryID][personaID]...)
	sort.Strings(paths)
	return paths, nil
}

// DropNoteSource removes a deleted post from the provenance blocks the app
// wrote, and reports which notes had cited it.
//
// This is the one part of a note the app maintains: it put those links there,
// and a link to a post that is gone points nowhere. What the note says about
// the post is the persona's to keep, correct or throw away.
func (s *Store) DropNoteSource(personaID, entryID string) ([]string, error) {
	if personaID == "" || entryID == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.memoryDir(personaID)
	candidates := append([]string(nil), s.noteSources[entryID][personaID]...)
	var touched []string
	for _, relative := range candidates {
		path := filepath.Join(dir, filepath.FromSlash(relative))
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				indexSet(s.noteSources, personaID, relative, []string{entryID}, nil)
				continue
			}
			return nil, err
		}
		next, changed := withoutNoteSource(string(raw), entryID)
		if !changed {
			indexSet(s.noteSources, personaID, relative, []string{entryID}, nil)
			continue
		}
		if err := writeNoteFile(path, next); err != nil {
			return nil, err
		}
		indexSet(s.noteSources, personaID, relative, sourceIDs(string(raw)), sourceIDs(next))
		touched = append(touched, relative)
	}
	if err := s.writeNoteSourceIndexLocked(); err != nil {
		return touched, err
	}
	sort.Strings(touched)
	return touched, nil
}

// DeleteNote throws one piece away, which is a decision a persona is allowed to
// make about its own notes.
func (s *Store) DeleteNote(personaID, path string) error {
	clean, err := notePath(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.memoryDir(personaID)
	target := filepath.Join(root, filepath.FromSlash(clean))
	previous, _ := os.ReadFile(target)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	indexSet(s.noteSources, personaID, clean, sourceIDs(string(previous)), nil)
	if err := s.writeNoteSourceIndexLocked(); err != nil {
		return err
	}
	// An emptied directory is noise in the listing, not history.
	for dir := filepath.Dir(target); dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// SaveMemory records the counters. The notes themselves are written as they are
// made, one at a time.
func (s *Store) SaveMemory(memory Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory.UpdatedAt = time.Now()
	memory.Notes = nil
	return writeJSON(s.memoryMeta(memory.PersonaID), memory)
}

// ForgetMemory drops everything a persona kept. Taking it out of the rotation
// does not do this: only the user asking to does.
func (s *Store) ForgetMemory(personaID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.memoryDir(personaID)); err != nil {
		return err
	}
	for entryID, personas := range s.noteSources {
		delete(personas, personaID)
		if len(personas) == 0 {
			delete(s.noteSources, entryID)
		}
	}
	if err := s.writeNoteSourceIndexLocked(); err != nil {
		return err
	}
	err := os.Remove(s.memoryMeta(personaID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) writeNoteSourceIndexLocked() error {
	return writeJSON(s.path(noteSourceIndexName), s.noteSources)
}

func (s *Store) memoryDir(personaID string) string {
	return s.path("memory", sanitise(personaID))
}

func (s *Store) memoryMeta(personaID string) string {
	return s.path("memory", sanitise(personaID)+".meta.json")
}

// notePath checks a name a model made up. Nothing here judges the name - it
// only makes sure it stays inside this persona's own directory.
func notePath(path string) (string, error) {
	clean := strings.TrimSpace(strings.ReplaceAll(path, `\`, "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		return "", errors.New("メモの名前が空です")
	}
	segments := strings.Split(clean, "/")
	if len(segments) > maxNoteDepth {
		return "", fmt.Errorf("メモの階層が深すぎます: %s", path)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return "", fmt.Errorf("メモの名前が不正です: %s", path)
		}
	}
	if filepath.Ext(clean) == "" {
		clean += ".md"
	}
	return clean, nil
}

// ---- skills ----------------------------------------------------------------

// SkillDir is where installed skills live, one directory each, as
// skills/{id}/skill.json.
func (s *Store) SkillDir() string { return s.path("skills") }

// Skills reads the installed persona definitions, by id.
func (s *Store) Skills(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.SkillDir())
	if os.IsNotExist(err) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if !entry.IsDir() || !safeName(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.SkillDir(), entry.Name(), "skill.json"))
		if err != nil {
			continue
		}
		out[entry.Name()] = raw
	}
	return out, nil
}

// PersistSkill saves one definition, replacing the file whole.
func (s *Store) PersistSkill(id string, raw []byte) error {
	if !safeName(id) {
		return errors.New("スキルのIDが不正です")
	}
	dir := filepath.Join(s.SkillDir(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "skill.json"), raw, 0o644)
}

// DeleteSkill removes one definition.
func (s *Store) DeleteSkill(id string) error {
	if !safeName(id) {
		return errors.New("スキルのIDが不正です")
	}
	return os.RemoveAll(filepath.Join(s.SkillDir(), id))
}

// ---- helpers ---------------------------------------------------------------

// Replace a note only after its complete new contents are on disk. A stopped
// write must leave the previous version readable.
func writeNoteFile(path, text string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".note-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func readJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// writeJSON writes through a temporary file: a crash mid-write must not cost
// what was already there.
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// writeFileAtomic replaces a file that another goroutine may be reading, so
// the reader sees the old content or the new, never half of either.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func sanitise(id string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// NewID returns an identifier that sorts by creation time and cannot collide.
func NewID(prefix string) string {
	var noise [4]byte
	if _, err := io.ReadFull(rand.Reader, noise[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixNano(), hex.EncodeToString(noise[:]))
}
