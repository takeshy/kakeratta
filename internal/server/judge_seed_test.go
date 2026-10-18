package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func seedServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	w, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: w, Catalogue: persona.NewCatalogue(w), Passive: true})
	t.Cleanup(s.Close)
	return s, w
}

func TestJudgeSeedEnglishSourcesAndNoExecution(t *testing.T) {
	s, w := seedServer(t)
	ctx := context.Background()
	if err := s.seedJudgeWorkspace(ctx); err != nil {
		t.Fatal(err)
	}
	var seed judgeSeed
	if err := json.Unmarshal(judgeSeedJSON, &seed); err != nil {
		t.Fatal(err)
	}
	entries, err := w.Timeline()
	if err != nil || len(entries) != len(seed.Entries) {
		t.Fatalf("entries: %d, %v", len(entries), err)
	}
	for _, e := range entries {
		if e.Language != "en" || e.Scheduled || e.Model != "" {
			t.Fatalf("unexpected entry: %+v", e)
		}
		if e.Media != nil {
			data, mime, err := w.Blob(e.Media.BlobID)
			if err != nil || len(data) == 0 || mime != "image/jpeg" || !e.Media.Ready() {
				t.Fatalf("photo missing or not ready: %s %v", mime, err)
			}
		}
		if _, ok := w.Entry(e.Root); !ok {
			t.Fatalf("missing root %s", e.Root)
		}
		if e.ReplyTo != "" {
			if _, ok := w.Entry(e.ReplyTo); !ok {
				t.Fatalf("missing reply target %s", e.ReplyTo)
			}
		}
	}
	for _, skill := range seed.Personas {
		if _, ok := s.catalogue.Find(ctx, skill.ID); !ok {
			t.Fatalf("missing persona %s", skill.ID)
		}
	}
	if all := s.catalogue.All(ctx); len(all) != len(seed.Personas) {
		t.Fatalf("unexpected or duplicate built-ins: %d", len(all))
	}
	for _, memory := range seed.Memories {
		note, ok := w.Note(memory.Persona, persona.MemoPath)
		if !ok {
			t.Fatalf("missing memory for %s", memory.Persona)
		}
		for _, f := range memory.Facts {
			if !strings.Contains(note.Text, "from:"+f.From) {
				t.Fatalf("missing fact source %s", f.From)
			}
			paths, err := w.NotesBySource(memory.Persona, f.From)
			if err != nil || len(paths) != 1 {
				t.Fatalf("source index %s: %v %v", f.From, paths, err)
			}
		}
		for _, record := range memory.Records {
			if _, ok := w.Entry(record.From); !ok {
				t.Fatalf("missing record source %s", record.From)
			}
			paths, err := w.NotesBySource(memory.Persona, record.From)
			if err != nil || len(paths) != 1 {
				t.Fatalf("record source index: %v %v", paths, err)
			}
		}
	}
	jobs, err := w.Reactions(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("seed queued AI jobs: %v %v", jobs, err)
	}
	schedules, err := w.Schedules("")
	if err != nil || len(schedules) != 0 {
		t.Fatalf("seed created schedules: %v %v", schedules, err)
	}
	// A repeat registration must not restore deleted examples or reset activation.
	if _, err := w.DeleteEntry(ctx, "judge-example-shoes"); err != nil {
		t.Fatal(err)
	}
	settings := w.ConfiguredSettings()
	settings.Active = []string{"support"}
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.seedJudgeWorkspace(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Entry("judge-example-shoes"); ok {
		t.Fatal("deleted example restored")
	}
	if active := w.Settings().Active; len(active) != 1 || active[0] != "support" {
		t.Fatalf("activation reset: %v", active)
	}
}

func TestJudgeSeedPreservesExistingWorkspace(t *testing.T) {
	s, w := seedServer(t)
	entry := timeline.Entry{ID: "original", Kind: timeline.KindPost, Root: "original", Speaker: timeline.User, Text: "My existing post", At: time.Now()}
	if err := w.Append(entry); err != nil {
		t.Fatal(err)
	}
	if err := s.seedJudgeWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.Timeline()
	skills, _ := w.Skills(context.Background())
	if len(entries) != 1 || entries[0].Text != entry.Text || len(skills) != 0 {
		t.Fatal("existing workspace changed")
	}
}

type seedFailureWorkspace struct {
	store.Workspace
	fail bool
}

func (w *seedFailureWorkspace) WriteNote(id, path, text string, sources ...store.NoteSource) error {
	if w.fail {
		w.fail = false
		return errors.New("interrupted seed")
	}
	return w.Workspace.WriteNote(id, path, text, sources...)
}

func TestJudgeSeedResumesAfterPartialFailure(t *testing.T) {
	s, w := seedServer(t)
	s.store = &seedFailureWorkspace{Workspace: w, fail: true}
	if err := s.seedJudgeWorkspace(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	before, _ := w.Timeline()
	if err := s.seedJudgeWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := w.Timeline()
	if len(before) == 0 || len(before) != len(after) {
		t.Fatal("partial seed duplicated or lost entries")
	}
	for i := range before {
		if before[i].ID != after[i].ID || !before[i].At.Equal(after[i].At) {
			t.Fatal("retry changed dates")
		}
	}
	if _, ok := w.Note("mei", persona.MemoPath); !ok {
		t.Fatal("retry did not finish memory")
	}
}

func TestJudgeRegistrationSeedsWithoutSharedMCP(t *testing.T) {
	g := newGateway(AuthOptions{Root: t.TempDir(), JudgeInviteToken: "invite", JudgeAPIKey: "model-key", JudgeExpiresAt: time.Now().Add(time.Hour), JudgeMaxUses: 10}, &judgeVerifier{})
	t.Cleanup(g.Close)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/judge", strings.NewReader(`{"token":"invite"}`))
	request.Header.Set("Authorization", "Bearer password:alice")
	response := httptest.NewRecorder()
	g.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("registration: %d %s", response.Code, response.Body.String())
	}
	judge, err := g.tenant("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := judge.store.Entry("judge-example-welcome"); !ok {
		t.Fatal("missing judge examples")
	}
	ordinary, err := g.tenant("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ordinary.store.Timeline()
	if err != nil || len(entries) != 0 {
		t.Fatal("ordinary account seeded")
	}
}
