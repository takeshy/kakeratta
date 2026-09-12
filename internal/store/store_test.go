package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The log is append-only, and changing an entry means writing it again. History
// is never rewritten underneath anything that already read it.
func TestALaterLineWinsWithoutRewritingTheLog(t *testing.T) {
	s := open(t)
	entry := timeline.Entry{
		ID: "e1", Kind: timeline.KindPost, Speaker: timeline.User,
		Text: "撮った", At: time.Now(), Media: &timeline.Media{BlobID: "b1"},
	}
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	read := time.Now()
	entry.Media = &timeline.Media{BlobID: "b1", Text: "読み取った文字", ReadAt: &read}
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want one after the update", len(entries))
	}
	if entries[0].Media.Text != "読み取った文字" {
		t.Fatalf("the later version did not win: %+v", entries[0].Media)
	}
}

func TestEntriesComeBackOldestFirst(t *testing.T) {
	s := open(t)
	now := time.Now()
	// Written out of order, the way a late remark arrives after a newer post.
	_ = s.Append(timeline.Entry{ID: "b", Kind: timeline.KindPost, At: now.Add(time.Minute)})
	_ = s.Append(timeline.Entry{ID: "a", Kind: timeline.KindPost, At: now})
	entries, _ := s.Timeline()
	if len(entries) != 2 || entries[0].ID != "a" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestBlobsRoundTripAndRefuseAWayOut(t *testing.T) {
	s := open(t)
	id, err := s.PutBlob([]byte{0x89, 'P', 'N', 'G'}, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	data, mime, err := s.Blob(id)
	if err != nil || len(data) != 4 || mime != "image/png" {
		t.Fatalf("blob = %v %q (%v)", data, mime, err)
	}
	// The id comes off a URL, so it is user input.
	for _, bad := range []string{"../settings.json", "a/b", `a\b`, ""} {
		if _, _, err := s.Blob(bad); err == nil {
			t.Fatalf("read %q", bad)
		}
	}
}

// The notes are the persona's own text and the only past it has. The app stores
// them under the names it chose and has no opinion about what is in them.
func TestMemoryIsWhateverThePersonaWrote(t *testing.T) {
	s := open(t)
	if memory := s.Memory("gourmet"); len(memory.Notes) != 0 || memory.Wakes != 0 {
		t.Fatalf("a persona starts with nothing: %+v", memory)
	}
	if err := s.WriteNote("gourmet", "tastes/tonkotsu.md", "---\ntype: Preference\n---\n豚骨に寄る"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("gourmet", "shops/jiro.md", "三田"); err != nil {
		t.Fatal(err)
	}
	back := s.Memory("gourmet")
	if len(back.Notes) != 2 || back.Notes[0].Path != "shops/jiro.md" || back.Notes[1].Path != "tastes/tonkotsu.md" {
		t.Fatalf("notes = %+v", back.Notes)
	}

	// Separate domains mean separate memories, and that is the whole point:
	// what is kept stays concentrated because nothing else lands in it.
	if other := s.Memory("tech"); len(other.Notes) != 0 {
		t.Fatalf("another persona's notes leaked: %+v", other.Notes)
	}
	if err := s.ForgetMemory("gourmet"); err != nil {
		t.Fatal(err)
	}
	if len(s.Memory("gourmet").Notes) != 0 {
		t.Fatal("forgetting did not")
	}
}

// Writing one piece leaves every other piece exactly as it was. That is the
// whole reason the pieces have names.
func TestWritingOnePieceLeavesTheRestAlone(t *testing.T) {
	s := open(t)
	_ = s.WriteNote("gourmet", "index.md", "最初の目次")
	_ = s.WriteNote("gourmet", "tastes/tonkotsu.md", "豚骨")
	_ = s.WriteNote("gourmet", "tastes/tonkotsu.md", "豚骨（更新）")

	notes := s.Memory("gourmet").Notes
	if len(notes) != 2 {
		t.Fatalf("notes = %+v", notes)
	}
	byPath := map[string]string{}
	for _, note := range notes {
		byPath[note.Path] = note.Text
	}
	if byPath["index.md"] != "最初の目次" {
		t.Fatalf("an untouched piece changed: %q", byPath["index.md"])
	}
	if byPath["tastes/tonkotsu.md"] != "豚骨（更新）" {
		t.Fatalf("the rewritten piece = %q", byPath["tastes/tonkotsu.md"])
	}

	// Throwing one away is the persona's decision, and it takes nothing else.
	if err := s.DeleteNote("gourmet", "tastes/tonkotsu.md"); err != nil {
		t.Fatal(err)
	}
	if remaining := s.Memory("gourmet").Notes; len(remaining) != 1 || remaining[0].Path != "index.md" {
		t.Fatalf("remaining = %+v", remaining)
	}
}

// The names come from a model, so they are the one thing about the notes the
// app does check - not for what they mean, but so a persona stays in its own
// directory.
func TestNoteNamesCannotEscape(t *testing.T) {
	s := open(t)
	for _, bad := range []string{"../settings.json", "a/../../b", "", ".hidden", "a/b/c/d/e/f/g.md"} {
		if err := s.WriteNote("gourmet", bad, "x"); err == nil {
			t.Fatalf("wrote %q", bad)
		}
	}
	// A leading slash is the bundle-relative form the format uses for links, so
	// it means the root of this persona's own notes - not the root of anything
	// else. A name that looks alarming is still just a name inside the bundle.
	if err := s.WriteNote("gourmet", "/etc/passwd", "x"); err != nil {
		t.Fatal(err)
	}
	// A name with no extension is still a name; it just gets one.
	if err := s.WriteNote("gourmet", "tastes/tonkotsu", "x"); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, note := range s.Memory("gourmet").Notes {
		paths = append(paths, note.Path)
	}
	if len(paths) != 2 || paths[0] != "etc/passwd.md" || paths[1] != "tastes/tonkotsu.md" {
		t.Fatalf("paths = %v", paths)
	}
	// And it really is inside the persona's directory, not beside the settings.
	if _, err := os.Stat(filepath.Join(s.Root(), "memory", "gourmet", "etc", "passwd.md")); err != nil {
		t.Fatalf("the note did not land in the persona's own directory: %v", err)
	}
}

func TestKeysAreNotInTheDefaults(t *testing.T) {
	s := open(t)
	settings := s.Settings()
	if settings.APIKey != "" || settings.Search.APIKey != "" {
		t.Fatal("a key appeared from nowhere")
	}
	// The contrast between the tiers is the experience, so the ones that ship
	// filled in must not be the same model.
	if settings.LowModel == settings.MiddleModel {
		t.Fatal("low and middle are the same model by default")
	}
	// An unset tier falls back rather than failing, so a single-model setup works.
	if settings.HighModel != settings.MiddleModel {
		t.Fatalf("an unset high tier should fall back to middle, got %q", settings.HighModel)
	}
}
