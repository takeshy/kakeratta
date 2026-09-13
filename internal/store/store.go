// Package store keeps everything on disk as plain files under one directory.
//
// The timeline is an append-only log: an entry is written once and never
// rewritten in place, and a change to one - an image finishing its reading, a
// deletion - is another line that wins over the earlier one. Appending is cheap
// no matter how long the log gets, and a crash halfway through a write costs
// the last line rather than the file.
//
// No database and no account. What the user captured is files they can read,
// copy and back up.
package store

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	root  string
	blobs BlobStore
	mu    sync.RWMutex
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
	return &Store{root: root, blobs: fileBlobs{root: filepath.Join(root, "blobs")}}, nil
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

// ---- timeline --------------------------------------------------------------

const logName = "timeline.jsonl"

// Append writes an entry to the end of the log. It is also how an entry is
// changed: a later line with the same id replaces the earlier one, so history
// is never rewritten underneath anything that already read it.
func (s *Store) Append(entry timeline.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(entry)
}

func (s *Store) appendLocked(entry timeline.Entry) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(raw) >= 8<<20 {
		return errors.New("投稿が大きすぎます")
	}
	file, err := os.OpenFile(s.path(logName), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	// Separate an interrupted last record from the next append. Never rewrite
	// the log, and never let a partial record swallow the next accepted post.
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err := file.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// Timeline reads the whole log, oldest first, with later versions of an entry
// replacing earlier ones.
func (s *Store) Timeline() ([]timeline.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timelineLocked()
}

func (s *Store) timelineLocked() ([]timeline.Entry, error) {
	file, err := os.Open(s.path(logName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	order := []string{}
	latest := map[string]timeline.Entry{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry timeline.Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// A half-written last line after a crash is the only way this
			// happens. Skipping it loses that one entry, not the log.
			continue
		}
		if _, seen := latest[entry.ID]; !seen {
			order = append(order, entry.ID)
		}
		latest[entry.ID] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	entries := make([]timeline.Entry, 0, len(order))
	for _, id := range order {
		entries = append(entries, latest[id])
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	return entries, nil
}

// UpdateMedia preserves any deletion made while an image was being analysed.
func (s *Store) UpdateMedia(id string, media *timeline.Media) (timeline.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.timelineLocked()
	if err != nil {
		return timeline.Entry{}, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			if entry.Deleted {
				return entry, nil
			}
			entry.Media = media
			return entry, s.appendLocked(entry)
		}
	}
	return timeline.Entry{}, errors.New("投稿が見つかりません")
}

// Entry returns one entry by id.
func (s *Store) Entry(id string) (timeline.Entry, bool) {
	entries, err := s.Timeline()
	if err != nil {
		return timeline.Entry{}, false
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return timeline.Entry{}, false
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

func extensionFor(mime string) string {
	switch {
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
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
	memory := Memory{PersonaID: personaID}
	_ = readJSON(s.memoryMeta(personaID), &memory)
	memory.PersonaID = personaID
	memory.Notes = nil

	root := s.memoryDir(personaID)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		written := time.Time{}
		if info, err := entry.Info(); err == nil {
			written = info.ModTime()
		}
		memory.Notes = append(memory.Notes, Note{
			Path: filepath.ToSlash(relative), Text: string(raw), Written: written,
		})
		return nil
	})
	sort.Slice(memory.Notes, func(i, j int) bool { return memory.Notes[i].Path < memory.Notes[j].Path })
	return memory
}

// Note returns one piece by name.
func (s *Store) Note(personaID, path string) (Note, bool) {
	clean, err := notePath(path)
	if err != nil {
		return Note{}, false
	}
	for _, note := range s.Memory(personaID).Notes {
		if note.Path == clean {
			return note, true
		}
	}
	return Note{}, false
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
	text = withNoteSources(string(previous), text, sources)
	if len(text) > maxNoteBytes {
		return fmt.Errorf("メモが大きすぎます: %s", clean)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(text), 0o644)
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
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
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
	err := os.Remove(s.memoryMeta(personaID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
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

// SkillDir is where installed skills live, one directory each.
func (s *Store) SkillDir() string { return s.path("skills") }

// File catalogues already write directly under SkillDir.
func (s *Store) PersistSkill(_ string, _ []byte) error { return nil }
func (s *Store) DeleteSkill(_ string) error            { return nil }

// ---- helpers ---------------------------------------------------------------

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
