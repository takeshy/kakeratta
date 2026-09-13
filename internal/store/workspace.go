package store

import (
	"context"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// Workspace is one user's complete persistent state. File-backed and
// Firestore-backed implementations share this contract so the HTTP and persona
// layers do not know where data lives.
type Workspace interface {
	Root() string
	SkillDir() string
	Append(timeline.Entry) error
	Timeline() ([]timeline.Entry, error)
	TimelinePage(context.Context, int, string) (TimelinePage, error)
	UpdateMedia(string, *timeline.Media) (timeline.Entry, error)
	Entry(string) (timeline.Entry, bool)
	PutBlob([]byte, string) (string, error)
	Blob(string) ([]byte, string, error)
	Memory(string) Memory
	Note(string, string) (Note, bool)
	WriteNote(string, string, string) error
	DeleteNote(string, string) error
	SaveMemory(Memory) error
	ForgetMemory(string) error
	ConfiguredSettings() Settings
	Settings() Settings
	SaveSettings(Settings) error
	PersistSkill(string, []byte) error
	DeleteSkill(string) error
}
