package persona

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

func organizationProposal() string {
	raw, _ := json.Marshal(map[string]any{"policy": "重複をまとめ、元の根拠を残す", "changes": []map[string]any{
		{"path": "merged.md", "text": "---\ntype: Preference\ntitle: 統合した好み\n---\n軽い赤が好き", "from": []string{"a.md", "b.md"}},
		{"path": "a.md", "delete": true}, {"path": "b.md", "delete": true},
	}})
	return string(raw)
}

func organizationSetup(t *testing.T) (*Runner, *store.Store, *int) {
	t.Helper()
	calls := 0
	runner, workspace, _ := setup(t, func(system, user string) string { calls++; return organizationProposal() }, "gourmet")
	for _, name := range []string{"a.md", "b.md"} {
		if err := workspace.WriteNote("gourmet", name, "---\ntype: Preference\ntitle: 好み\n---\n軽い赤が好き", store.NoteSource{EntryID: name, Label: name}); err != nil {
			t.Fatal(err)
		}
	}
	return runner, workspace, &calls
}

func TestOrganizationRequiresApprovalAndPreservesSources(t *testing.T) {
	r, s, calls := organizationSetup(t)
	ctx := context.Background()
	preview, err := r.PreviewOrganization(ctx, "gourmet", "同じメモをまとめて", "builtin")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "ready" || len(preview.Changes) != 4 {
		t.Fatalf("preview: %+v", preview)
	}
	if _, ok := s.Note("gourmet", "merged.md"); ok {
		t.Fatal("preview changed memory")
	}
	// An approval can reach another Runner, without asking the model again.
	other := New(s, r.catalogue, r.clientOf)
	if err := other.ApplyOrganization(ctx, "book", preview.Token); err == nil {
		t.Fatal("another persona used token")
	}
	if err := other.ApplyOrganization(ctx, "gourmet", preview.Token); err != nil {
		t.Fatal(err)
	}
	merged, ok := s.Note("gourmet", "merged.md")
	if !ok || len(store.NoteSources(merged.Text)) != 2 {
		t.Fatalf("lost provenance: %+v", merged)
	}
	if _, ok := s.Note("gourmet", "a.md"); ok {
		t.Fatal("original still exists")
	}
	index, _ := s.Note("gourmet", "index.md")
	if !strings.Contains(index.Text, "merged.md") || strings.Contains(index.Text, "/a.md") {
		t.Fatalf("index: %s", index.Text)
	}
	if err := other.ApplyOrganization(ctx, "gourmet", preview.Token); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatal("approval regenerated the plan")
	}
}

func TestOrganizationRejectsChangedMemoryAndExpiredTokens(t *testing.T) {
	for _, kind := range []string{"edit", "add", "delete", "expired"} {
		t.Run(kind, func(t *testing.T) {
			r, s, _ := organizationSetup(t)
			ctx := context.Background()
			preview, err := r.PreviewOrganization(ctx, "gourmet", "まとめて", "builtin")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "edit":
				_ = s.WriteNote("gourmet", "a.md", "changed")
			case "add":
				_ = s.WriteNote("gourmet", "new.md", "new")
			case "delete":
				_ = s.DeleteNote("gourmet", "a.md")
			case "expired":
				trial, _ := r.readOrganization(ctx, "gourmet", preview.Token)
				trial.At = time.Now().Add(-2 * time.Hour)
				_ = r.saveOrganization(ctx, preview.Token, trial)
			}
			if err := r.ApplyOrganization(ctx, "gourmet", preview.Token); err == nil {
				t.Fatal("invalid approval accepted")
			}
			if _, ok := s.Note("gourmet", "merged.md"); ok {
				t.Fatal("invalid approval wrote a note")
			}
		})
	}
}

func TestExternalOrganizationProducesPreviewWithoutPostingOrWriting(t *testing.T) {
	r, s, calls := organizationSetup(t)
	ctx := context.Background()
	preview, err := r.PreviewOrganization(ctx, "gourmet", "重複を整理して", "external")
	if err != nil || preview.Status != "researching" {
		t.Fatalf("%+v %v", preview, err)
	}
	if *calls != 0 {
		t.Fatal("built-in planner started before external research")
	}
	asks := r.Asks()
	if len(asks) != 1 || asks[0].Wake != "organize_notes" {
		t.Fatalf("asks: %+v", asks)
	}
	ask, brief, err := r.ClaimAsk(asks[0].ID, "test")
	if err != nil || !strings.Contains(brief.Rules, "OK") {
		t.Fatalf("%+v %v", brief, err)
	}
	read, err := r.ReadForAsk(ctx, ask.ID, "test", []ReadStep{{Type: "notes_read", Paths: []string{"a.md", "b.md"}}})
	if err != nil || !strings.Contains(read, "軽い赤") {
		t.Fatalf("read %s %v", read, err)
	}
	verdict, err := r.SubmitAskAnswer(ctx, ask.ID, "test", &Answer{Text: "重複を統合する方針です"}, nil)
	if err != nil || !verdict.Accepted {
		t.Fatalf("%+v %v", verdict, err)
	}
	r.resumeAsk(ctx, ask.ID)
	ready, err := r.Organization(ctx, "gourmet", preview.Token)
	if err != nil || ready.Status != "ready" {
		t.Fatalf("%+v %v", ready, err)
	}
	if _, ok := s.Note("gourmet", "merged.md"); ok {
		t.Fatal("external answer executed before approval")
	}
	entries, _ := s.Timeline()
	if len(entries) != 0 {
		t.Fatal("organization posted to timeline")
	}
	if err := r.ApplyOrganization(ctx, "gourmet", preview.Token); err != nil {
		t.Fatal(err)
	}
}

func TestExternalOrganizationTimeoutFallsBackToPreview(t *testing.T) {
	r, s, _ := organizationSetup(t)
	ctx := context.Background()
	preview, err := r.PreviewOrganization(ctx, "gourmet", "まとめて", "external")
	if err != nil {
		t.Fatal(err)
	}
	ask := r.Asks()[0]
	record, _, _ := r.readAsk(ctx, ask.ID)
	record.Ask.ClaimBy = time.Now().Add(-time.Second)
	_, _ = r.changeAsk(ctx, ask.ID, record.expiry(), func(current *askRecord, _ bool) (bool, error) { *current = record; return true, nil })
	r.sweepAsks(ctx, time.Now())
	r.resumeAsk(ctx, ask.ID)
	ready, err := r.Organization(ctx, "gourmet", preview.Token)
	if err != nil || ready.Status != "ready" {
		t.Fatalf("%+v %v", ready, err)
	}
	if _, ok := s.Note("gourmet", "merged.md"); ok {
		t.Fatal("fallback wrote memory")
	}
}

func TestOrganizationRejectsMalformedChangesBeforeApproval(t *testing.T) {
	for _, name := range []string{"../outside.md", "/absolute.md", "a.md\x00", "no-extension", "a/b/c/d/e/f/g.md"} {
		t.Run(name, func(t *testing.T) {
			response, _ := json.Marshal(map[string]any{"policy": "整理", "changes": []map[string]any{{"path": name, "text": "---\ntype: Preference\n---\n内容"}}})
			r, s, _ := setup(t, func(string, string) string { return string(response) }, "gourmet")
			_ = s.WriteNote("gourmet", "original.md", "keep")
			_, err := r.PreviewOrganization(context.Background(), "gourmet", "整理", "builtin")
			if err == nil {
				t.Fatal("invalid path accepted")
			}
			paths, _ := s.NotePaths("gourmet")
			if len(paths) != 1 || paths[0] != "original.md" {
				t.Fatalf("preview mutated notes: %v", paths)
			}
		})
	}
}
