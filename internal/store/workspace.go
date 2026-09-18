package store

import (
	"context"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// Workspace is one user's complete persistent state. File-backed and
// Firestore-backed implementations share this contract so the HTTP and persona
// layers do not know where data lives.
type Workspace interface {
	SocialValue(context.Context, string) (SocialValue, error)
	SwapSocialValue(context.Context, string, string, SocialValue) (bool, error)
	Publication(string) (Publication, error)
	ClaimPublication(string, Publication) (bool, error)
	SavePublication(string, Publication) error
	Root() string
	SkillDir() string
	Append(timeline.Entry) error
	CreateEntry(timeline.Entry) (timeline.Entry, bool, error)
	Timeline() ([]timeline.Entry, error)
	TimelinePage(context.Context, int, string) (TimelinePage, error)
	UpdateMedia(string, *timeline.Media) (timeline.Entry, error)
	UpdateText(string, string) (timeline.Entry, error)
	Entry(string) (timeline.Entry, bool)
	PutBlob([]byte, string) (string, error)
	Blob(string) ([]byte, string, error)
	Memory(string) Memory
	Note(string, string) (Note, bool)
	WriteNote(string, string, string, ...NoteSource) error
	DeleteNote(string, string) error
	DropNoteSource(string, string) ([]string, error)
	SaveMemory(Memory) error
	ForgetMemory(string) error
	ConfiguredSettings() Settings
	Settings() Settings
	SaveSettings(Settings) error
	ClaimDailyCheckIn(string, string) (bool, error)
	RecordWake(string, string) error
	Woke(string) ([]string, error)
	ForgetWakes(string) error
	SaveRouting(string, Routing) error
	Routing(string) (*Routing, error)
	ForgetRouting(string) error
	Schedules(string) ([]Schedule, error)
	SaveSchedule(Schedule) (Schedule, error)
	DeleteSchedule(string, string) error
	DeleteSchedules(string) error
	ClaimSchedule(string, time.Time, time.Time) (Schedule, bool, error)
	PersistSkill(string, []byte) error
	DeleteSkill(string) error
}
