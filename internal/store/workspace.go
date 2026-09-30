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
	UpdateEntry(timeline.Entry) error
	DeleteEntry(context.Context, string) ([]timeline.Entry, error)
	CreateEntry(timeline.Entry) (timeline.Entry, bool, error)
	Timeline() ([]timeline.Entry, error)
	TimelinePage(context.Context, int, string) (TimelinePage, error)
	UpdateMedia(string, *timeline.Media) (timeline.Entry, error)
	UpdateText(string, string) (timeline.Entry, error)
	Entry(string) (timeline.Entry, bool)
	PutBlob([]byte, string) (string, error)
	Blob(string) ([]byte, string, error)
	DeleteBlob(string) error
	Memory(string) Memory
	MemoryMeta(string) Memory
	NotePaths(string) ([]string, error)
	Notes(string, []string) ([]Note, error)
	MemoryPage(string, string, int, func(Note) bool) (MemoryPage, error)
	Note(string, string) (Note, bool)
	WriteNote(string, string, string, ...NoteSource) error
	DeleteNote(string, string) error
	NotesBySource(string, string) ([]string, error)
	SaveReaction(Reaction) error
	CreateReaction(Reaction) error
	Reactions(context.Context) ([]Reaction, error)
	ClaimReaction(ctx context.Context, worker string, ttl time.Duration) (Reaction, []Reaction, bool, error)
	RenewReaction(ctx context.Context, id, worker string, ttl time.Duration) (bool, error)
	ReleaseReaction(ctx context.Context, id, worker string) error
	FinishReaction(string) error
	Record(ctx context.Context, collection, id string) ([]byte, bool, error)
	Records(ctx context.Context, collection string) (map[string][]byte, error)
	UpdateRecord(ctx context.Context, collection, id string, expire time.Time, change RecordChange) error
	Lock(ctx context.Context, name string) (func(), error)
	SetClosing(ctx context.Context, closing bool) error
	NoteJobs() ([]NoteJob, error)
	SaveNoteJob(NoteJob) error
	DeleteNoteJob(string) error
	DropNoteSource(string, string) ([]string, error)
	SaveMemory(Memory) error
	ForgetMemory(string) error
	ConfiguredSettings() Settings
	Settings() Settings
	SaveSettings(Settings) error
	ClaimDailyCheckInAt(id, day string, at time.Time) (bool, time.Time, error)
	RecordWake(string, string) error
	Woke(string) ([]string, error)
	ForgetWakes(string) error
	SaveRouting(string, Routing) error
	UpdateRouting(string, func(*Routing) (*Routing, error)) error
	Routing(string) (*Routing, error)
	ForgetRouting(string) error
	Schedules(string) ([]Schedule, error)
	SaveSchedule(Schedule) (Schedule, error)
	DeleteSchedule(string, string) error
	DeleteSchedules(string) error
	ClaimSchedule(string, time.Time, time.Time) (Schedule, bool, error)
	PersistSkill(string, []byte) error
	DeleteSkill(string) error
	// Export hands the whole workspace to sink as plain files in the layout a
	// local kakeratta reads, so a development server can serve it as is.
	Export(context.Context, ExportSink) error
	// Destroy removes everything the workspace holds, wherever it is kept. The
	// workspace is unusable afterwards; whoever holds it closes first.
	Destroy(context.Context) error
}
