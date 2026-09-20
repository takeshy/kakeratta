package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The hosted workspace exports into the layout the file store reads, so the
// download from a Firestore deployment opens as a development workspace. Runs
// against the emulator only (`firebase emulators:start --only firestore`).
func TestFirestoreWorkspaceExportOpensAsAFileWorkspace(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the Firestore export test")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "kakeratta-export-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	w, err := NewFirestoreWorkspace(ctx, client, NewID("user"), nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	settings := Settings{
		Provider: ProviderNebius, BaseURL: "https://api.example/v1", APIKey: "nebius-key",
		LowModel: "low", MiddleModel: "mid", HighModel: "high", Active: []string{"cafe"},
		TimeZone: "Asia/Tokyo",
	}
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	blob, err := w.PutBlob([]byte("capture"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PersistSkill("cafe", []byte(`{"id":"cafe","name":"カフェ"}`)); err != nil {
		t.Fatal(err)
	}
	post := timeline.Entry{
		ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User,
		Text: "かけら", At: now, Root: "post_1", Media: &timeline.Media{BlobID: blob, MIME: "image/png"},
	}
	if err := w.Append(post); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteNote("cafe", "shops/regular.md", "いつもの店"); err != nil {
		t.Fatal(err)
	}
	if err := w.SaveMemory(Memory{PersonaID: "cafe", JoinedAt: now, Wakes: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SwapSocialValue(ctx, "connection", "", SocialValue{Data: []byte("encrypted")}); err != nil {
		t.Fatal(err)
	}
	if err := w.SavePublication("post_1", Publication{State: "published", URI: "at://did:example/post"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.ClaimDailyCheckIn("cafe", "2026-09-20"); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if _, err := w.SaveSchedule(Schedule{ID: "s_1", Skill: "cafe", At: now.Add(time.Hour), Note: "花を買う"}); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordWake("post_1", "cafe"); err != nil {
		t.Fatal(err)
	}
	if err := w.SaveRouting("post_1", Routing{Delivered: []string{"cafe"}, Excluded: map[string]string{"michi": "旅行とは無関係"}}); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	if err := w.Export(ctx, dirSink{root: target}); err != nil {
		t.Fatal(err)
	}

	exported, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	// The backup keeps the room, not the keys that reached the models.
	read := exported.ConfiguredSettings()
	if read.APIKey != "" {
		t.Fatalf("a key travelled with the export: %+v", read)
	}
	if len(read.Active) != 1 || read.Active[0] != "cafe" {
		t.Fatalf("active = %v", read.Active)
	}
	entries, err := exported.Timeline()
	if err != nil || len(entries) != 1 || entries[0].Text != "かけら" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(exported.SkillDir(), "cafe", "skill.json"))
	if err != nil || strings.Contains(string(raw), "カフェ") != true {
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
	if value, err := exported.SocialValue(ctx, "connection"); err != nil || string(value.Data) != "encrypted" {
		t.Fatalf("social value = %+v, %v", value, err)
	}
	if publication, err := exported.Publication("post_1"); err != nil || publication.URI != "at://did:example/post" {
		t.Fatalf("publication = %+v, %v", publication, err)
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
	if err != nil || routing == nil || routing.Excluded["michi"] != "旅行とは無関係" {
		t.Fatalf("routing = %+v, %v", routing, err)
	}
}
