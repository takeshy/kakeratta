package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// dirSink writes an export as a directory, which is what these tests open as a
// workspace. The app's own sink writes the same files into a zip as they come.
type dirSink struct{ root string }

func (d dirSink) Add(name string, modified time.Time, write func(io.Writer) error) error {
	target := filepath.Join(d.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if modified.IsZero() {
		return nil
	}
	return os.Chtimes(target, modified, modified)
}

// Export writes the workspace as the files a local kakeratta reads, so the
// exported directory opens as a workspace of its own and holds the same state.
func TestExportedWorkspaceOpensAsTheOriginal(t *testing.T) {
	s := open(t)
	now := time.Now()

	settings := Settings{
		Provider: ProviderNebius, BaseURL: "https://api.example/v1", APIKey: "nebius-key",
		LowModel: "low", MiddleModel: "mid", HighModel: "high", Active: []string{"cafe"},
		DailyCheckIns: map[string]DailyCheckIn{"cafe": {Enabled: true, Time: "08:00", TimeZone: "Asia/Tokyo"}},
		Search:        SearchSettings{APIKey: "tavily-key"}, TimeZone: "Asia/Tokyo",
	}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	blob, err := s.PutBlob([]byte("capture"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	post := timeline.Entry{
		ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User,
		Text: "かけら", At: now, Root: "post_1", Media: &timeline.Media{BlobID: blob, MIME: "image/png"},
	}
	reply := timeline.Entry{
		ID: "reply_1", Kind: timeline.KindRemark, Speaker: "cafe",
		Text: "反応", At: now.Add(time.Minute), Root: "post_1", ReplyTo: "post_1",
	}
	for _, entry := range []timeline.Entry{post, reply} {
		if _, _, err := s.CreateEntry(entry); err != nil {
			t.Fatal(err)
		}
	}
	skillDir := filepath.Join(s.SkillDir(), "cafe")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := []byte(`{"id":"cafe","name":"カフェ"}`)
	if err := os.WriteFile(filepath.Join(skillDir, "skill.json"), skill, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("cafe", "shops/regular.md", "いつもの店", NoteSource{EntryID: "post_1", Label: "2026-09-20"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMemory(Memory{PersonaID: "cafe", JoinedAt: now, Wakes: 3}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.SwapSocialValue(ctx, "connection", "", SocialValue{Data: []byte("encrypted")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePublication("post_1", Publication{State: "published", URI: "at://did:example/post"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimDailyCheckIn("cafe", "2026-09-20"); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if _, err := s.SaveSchedule(Schedule{ID: "s_1", Skill: "cafe", At: now.Add(time.Hour), Note: "花を買う"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWake("post_1", "cafe"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRouting("post_1", Routing{Delivered: []string{"cafe"}, Excluded: map[string]string{"michi": "旅行とは無関係"}}); err != nil {
		t.Fatal(err)
	}
	vectorKey := strings.Repeat("ab", 32)
	if err := s.SaveSearchVector(ctx, vectorKey, SearchVector{Vector: []float64{1, 2}}); err != nil {
		t.Fatal(err)
	}
	// A crash half way through a write leaves a .tmp behind; it is not state.
	if err := os.WriteFile(filepath.Join(s.Root(), "settings.json.tmp"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	if err := s.Export(ctx, dirSink{root: target}); err != nil {
		t.Fatal(err)
	}

	exported, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	// Of the settings a backup keeps only the room: who is in it, and when
	// each one wakes. The keys stayed where they were issued.
	read := exported.ConfiguredSettings()
	if read.APIKey != "" || read.Search.APIKey != "" {
		t.Fatalf("a key travelled with the export: %+v", read)
	}
	if len(read.Active) != 1 || read.Active[0] != "cafe" {
		t.Fatalf("active = %v", read.Active)
	}
	if daily := read.DailyCheckIns["cafe"]; !daily.Enabled || daily.Time != "08:00" {
		t.Fatalf("daily check-in = %+v", daily)
	}
	// What was left out has to arrive as unset, not as a choice: a settings
	// file of blanks would read as dictation with its silence timer turned off.
	fresh := open(t).ConfiguredSettings()
	if read.Speech != fresh.Speech || read.LowModel != fresh.LowModel || read.BaseURL != fresh.BaseURL {
		t.Fatalf("a workspace opened from the export is not at its defaults: %+v", read)
	}
	entries, err := exported.Timeline()
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if entries[0].Media == nil || entries[0].Media.BlobID != blob {
		t.Fatalf("media = %+v", entries[0].Media)
	}
	raw, err := os.ReadFile(filepath.Join(exported.SkillDir(), "cafe", "skill.json"))
	if err != nil || string(raw) != string(skill) {
		t.Fatalf("skill = %s, %v", raw, err)
	}
	memory := exported.Memory("cafe")
	if len(memory.Notes) != 1 || !strings.Contains(memory.Notes[0].Text, "いつもの店") || memory.Wakes != 3 {
		t.Fatalf("memory = %+v", memory)
	}
	if note, ok := exported.Note("cafe", "shops/regular.md"); !ok || !strings.Contains(note.Text, "いつもの店") {
		t.Fatalf("note = %+v, %v", note, ok)
	}
	data, _, err := exported.Blob(blob)
	if err != nil || string(data) != "capture" {
		t.Fatalf("blob = %q, %v", data, err)
	}
	// A connection is not part of a workspace: the Bluesky tokens, the key an
	// external agent connects with, and what was published from this account
	// stay behind, and the copy connects for itself.
	if value, err := exported.SocialValue(ctx, "connection"); err != nil || len(value.Data) != 0 {
		t.Fatalf("connection data travelled with the export: %+v, %v", value, err)
	}
	if publication, err := exported.Publication("post_1"); err != nil || publication.URI != "" {
		t.Fatalf("a publication record travelled with the export: %+v, %v", publication, err)
	}
	if ok, err := exported.ClaimDailyCheckIn("cafe", "2026-09-19"); err != nil || ok {
		t.Fatalf("an earlier day was claimed again: %v, %v", ok, err)
	}
	schedules, err := exported.Schedules("cafe")
	if err != nil || len(schedules) != 1 || schedules[0].Note != "花を買う" {
		t.Fatalf("schedules = %+v, %v", schedules, err)
	}
	if woke, err := exported.Woke("post_1"); err != nil || len(woke) != 1 || woke[0] != "cafe" {
		t.Fatalf("woke = %v, %v", woke, err)
	}
	routing, err := exported.Routing("post_1")
	if err != nil || routing == nil || len(routing.Delivered) != 1 || routing.Excluded["michi"] != "旅行とは無関係" {
		t.Fatalf("routing = %+v, %v", routing, err)
	}
	if vector, err := exported.SearchVector(ctx, vectorKey); err != nil || len(vector.Vector) != 2 {
		t.Fatalf("vector = %+v, %v", vector, err)
	}
	if _, err := os.Stat(filepath.Join(target, "settings.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("a temporary file travelled with the export: %v", err)
	}
}

// Captures reach the export through the blob store, so a workspace backed by
// object storage still exports them as files.
func TestExportPullsBlobsThroughTheBlobStore(t *testing.T) {
	s := open(t)
	s.SetBlobStore(&memoryBlobs{})
	id, err := s.PutBlob([]byte("object"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := s.Export(context.Background(), dirSink{root: target}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "blobs", id))
	if err != nil || string(data) != "object" {
		t.Fatalf("exported blob = %q, %v", data, err)
	}
}
