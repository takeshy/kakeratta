package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// The download is a workspace: unzip it and a local kakeratta serves the same
// state. The manifest and the restore instructions travel with the files.
func TestExportDownloadsAWorkspaceThatRunsInDevelopment(t *testing.T) {
	server, s := app(t)
	if err := s.SaveSettings(store.Settings{APIKey: "nebius-key", LowModel: "low"}); err != nil {
		t.Fatal(err)
	}
	blob, err := s.PutBlob([]byte("capture"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateEntry(timeline.Entry{
		ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User,
		Text: "かけら", At: time.Now(), Root: "post_1", Media: &timeline.Media{BlobID: blob, MIME: "image/png"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("cafe", "shops/regular.md", "いつもの店"); err != nil {
		t.Fatal(err)
	}
	written, ok := s.Note("cafe", "shops/regular.md")
	if !ok {
		t.Fatal("the note was not written")
	}

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type = %q", got)
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("content disposition = %q", disposition)
	}

	extracted := t.TempDir()
	reader, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if strings.ContainsAny(file.Name, `\`) || strings.Contains(file.Name, "..") {
			t.Fatalf("unsafe zip entry: %q", file.Name)
		}
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(extracted, filepath.Dir(file.Name)), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(extracted, file.Name)
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
		// Unzipping restores each file's time; a note is dated by it.
		if err := os.Chtimes(target, file.Modified, file.Modified); err != nil {
			t.Fatal(err)
		}
	}

	var manifest struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(extracted, "backup.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.App != "kakeratta" || manifest.Version != "test" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(extracted, "README.txt")); err != nil {
		t.Fatal(err)
	}

	// The unzipped directory is the workspace a development kakeratta opens.
	opened, err := store.Open(extracted)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := opened.Timeline()
	if err != nil || len(entries) != 1 || entries[0].Text != "かけら" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	// The zip is safe to keep anywhere: the key stayed on the server.
	if read := opened.ConfiguredSettings(); read.APIKey != "" {
		t.Fatalf("a key travelled with the export: %+v", read)
	}
	settings, err := os.ReadFile(filepath.Join(extracted, "settings.json"))
	if err != nil || bytes.Contains(settings, []byte("nebius-key")) {
		t.Fatalf("settings.json = %s, %v", settings, err)
	}
	data, _, err := opened.Blob(blob)
	if err != nil || string(data) != "capture" {
		t.Fatalf("blob = %q, %v", data, err)
	}
	// A note keeps the day it was written: the zip carries the file time, so
	// the copy is not dated 1980 the moment it is unzipped.
	note, ok := opened.Note("cafe", "shops/regular.md")
	if !ok || note.Text != "いつもの店" {
		t.Fatalf("note = %+v, %v", note, ok)
	}
	if gap := note.Written.Sub(written.Written); gap < -2*time.Second || gap > 2*time.Second {
		t.Fatalf("note written = %s, want %s", note.Written, written.Written)
	}
}

// fakeExports stands in for object storage: it keeps what was streamed to it
// and hands back a link, which is all the app knows about the place.
type fakeExports struct {
	mu      sync.Mutex
	name    string
	saved   []byte
	started chan struct{}
	release chan struct{}
	purged  bool
}

func (f *fakeExports) Save(ctx context.Context, name string, write func(io.Writer) error) (string, error) {
	if f.started != nil {
		close(f.started)
	}
	if f.release != nil {
		<-f.release
	}
	var buffer bytes.Buffer
	if err := write(&buffer); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.name, f.saved = name, buffer.Bytes()
	f.mu.Unlock()
	return "https://storage.example/" + name + "?signed", nil
}

func (f *fakeExports) Purge(context.Context) error {
	f.mu.Lock()
	f.name, f.saved = "", nil
	f.purged = true
	f.mu.Unlock()
	return nil
}

func (f *fakeExports) bytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saved
}

func exporting(t *testing.T, exports store.ExportTarget) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{
		Store: s, Catalogue: persona.NewCatalogue(s.SkillDir()),
		Version: "test", Exports: exports,
	})
	t.Cleanup(server.Close)
	return server, s
}

// With somewhere to put it, the backup is written there and the page is told
// the link over the event stream: the download itself never comes through here.
func TestExportGoesToStorageAndIsAnnounced(t *testing.T) {
	exports := &fakeExports{}
	server, s := exporting(t, exports)
	if _, _, err := s.CreateEntry(timeline.Entry{
		ID: "post_1", Kind: timeline.KindPost, Speaker: timeline.User,
		Text: "かけら", At: time.Now(), Root: "post_1",
	}); err != nil {
		t.Fatal(err)
	}
	watching := server.events.join()
	defer server.events.leave(watching)

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/export", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var started struct{ State string }
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil || started.State != "running" {
		t.Fatalf("body = %s, %v", recorder.Body.String(), err)
	}

	announced := map[string]any{}
	deadline := time.After(10 * time.Second)
	for announced["state"] == nil {
		select {
		case event := <-watching:
			if event.Name == "export" {
				announced = event.Data.(map[string]any)
			}
		case <-deadline:
			t.Fatal("the export was never announced")
		}
	}
	if announced["state"] != "ready" {
		t.Fatalf("announced = %+v", announced)
	}
	link, _ := announced["url"].(string)
	if !strings.HasPrefix(link, "https://storage.example/kakeratta-") {
		t.Fatalf("url = %q", link)
	}

	// What went to storage is the workspace, whole.
	saved := exports.bytes()
	reader, err := zip.NewReader(bytes.NewReader(saved), int64(len(saved)))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	daily := false
	for _, file := range reader.File {
		found[file.Name] = true
		if strings.HasPrefix(file.Name, "timeline/") && strings.HasSuffix(file.Name, ".json") {
			daily = true
		}
	}
	if !daily {
		t.Fatal("daily timeline missing from export")
	}
	for _, name := range []string{"backup.json", "README.txt"} {
		if !found[name] {
			t.Fatalf("%s is missing from %v", name, found)
		}
	}
}

// Without storage there is nowhere to put it, so the page is sent to the
// endpoint that streams it down the response instead.
func TestExportWithoutStorageIsStreamed(t *testing.T) {
	server, _ := app(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/export", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var ready struct{ State, URL string }
	if err := json.Unmarshal(recorder.Body.Bytes(), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.State != "ready" || ready.URL != "/api/export" {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// One at a time. An export reads the whole workspace, and a page that asks
// twice must not set a second one going beside the first.
func TestExportRefusesASecondOneWhileItRuns(t *testing.T) {
	exports := &fakeExports{started: make(chan struct{}), release: make(chan struct{})}
	server, _ := exporting(t, exports)
	first := httptest.NewRecorder()
	server.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/export", nil))
	if first.Code != http.StatusAccepted {
		t.Fatalf("status = %d", first.Code)
	}
	<-exports.started
	second := httptest.NewRecorder()
	server.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/export", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d: %s", second.Code, second.Body.String())
	}
	close(exports.release)
}
