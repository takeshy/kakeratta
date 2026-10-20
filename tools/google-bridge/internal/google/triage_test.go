package google

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func keepValue(v bool) *bool { return &v }
func triageChoice(item TriageItem, keep bool, kind string) TriageDecision {
	return TriageDecision{MessageID: item.Message.MessageID, Token: item.Token, Keep: keepValue(keep), Importance: 90, Reason: "Owner-relevant booking", Summary: "10月20日の予約が確定しました。", Type: MailType{ID: kind, Label: "予約の確認"}}
}
func TestEditablePromptTriageAndLearnedTypeExclusion(t *testing.T) {
	s, p := importSetup(t)
	ctx := context.Background()
	settings := defaultInboxSettings()
	settings.Enabled = true
	settings.LLMEnabled = true
	settings.TriagePrompt = "予約の確認は候補にして、広告は除いてください。"
	if _, e := s.SetInboxSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	res, e := s.SyncGmail(ctx)
	if e != nil || res.TriagePending != 2 || len(res.ImportedIDs) != 0 {
		t.Fatal(res, e)
	}
	view, _ := s.Inbox(ctx)
	if len(view.Messages) != 0 {
		t.Fatal("LLM bypassed")
	}
	tc, e := s.Triage(ctx)
	if e != nil || !tc.Enabled || len(tc.Items) != 2 || tc.OwnerPrompt != settings.TriagePrompt || tc.Items[0].Message.TrustLevel != "external" {
		t.Fatal(tc, e)
	}
	first := triageChoice(tc.Items[0], true, "booking_confirmation")
	if _, e = s.SubmitTriage(ctx, first); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubmitTriage(ctx, first); e != nil {
		t.Fatal("idempotent retry", e)
	}
	changed := first
	changed.Keep = keepValue(false)
	if _, e = s.SubmitTriage(ctx, changed); !errors.Is(e, ErrTriageChanged) {
		t.Fatal(e)
	}
	if _, e = s.SubmitTriage(ctx, triageChoice(tc.Items[1], false, "newsletter")); e != nil {
		t.Fatal(e)
	}
	view, _ = s.Inbox(ctx)
	if len(view.Messages) != 1 || view.Messages[0].CategoryID != "booking_confirmation" || view.Messages[0].Summary != first.Summary {
		t.Fatal(view)
	}
	settings.ExcludedTypes = []MailType{{ID: "booking_confirmation", Label: "予約の確認"}}
	if _, e = s.SetInboxSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	p.history = `[{"messagesAdded":[{"message":{"id":"m4"}}]}]`
	if _, e = s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	tc, e = s.Triage(ctx)
	if e != nil || len(tc.Items) != 1 || len(tc.ExcludedTypes) != 1 {
		t.Fatal(tc, e)
	}
	if _, e = s.SubmitTriage(ctx, triageChoice(tc.Items[0], true, "booking_confirmation")); e != nil {
		t.Fatal(e)
	}
	view, _ = s.Inbox(ctx)
	if len(view.Messages) != 1 {
		t.Fatal("excluded type was forwarded", view)
	}
}

func TestKeptTriageRequiresBoundedSummary(t *testing.T) {
	s, _ := importSetup(t)
	ctx := context.Background()
	in := defaultInboxSettings()
	in.Enabled, in.LLMEnabled = true, true
	if _, e := s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	tc, e := s.Triage(ctx)
	if e != nil || len(tc.Items) == 0 {
		t.Fatal(tc, e)
	}
	decision := triageChoice(tc.Items[0], true, "booking_confirmation")
	decision.Summary = ""
	if _, e = s.SubmitTriage(ctx, decision); !errors.Is(e, ErrInput) {
		t.Fatal("kept mail without a summary was accepted", e)
	}
	decision.Summary = strings.Repeat("あ", 2001)
	if _, e = s.SubmitTriage(ctx, decision); !errors.Is(e, ErrInput) {
		t.Fatal("oversized summary was accepted", e)
	}
}
func TestPromptAndBodyPolicyChangesInvalidatePendingModelResult(t *testing.T) {
	s, _ := importSetup(t)
	ctx := context.Background()
	in := defaultInboxSettings()
	in.Enabled = true
	in.LLMEnabled = true
	if _, e := s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	tc, e := s.Triage(ctx)
	if e != nil {
		t.Fatal(e)
	}
	decision := triageChoice(tc.Items[0], true, "booking_confirmation")
	in.TriagePrompt = "広告だけを除外してください。"
	if _, e = s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubmitTriage(ctx, decision); !errors.Is(e, ErrTriageChanged) {
		t.Fatal("stale prompt result accepted", e)
	}
	tc, _ = s.Triage(ctx)
	decision = triageChoice(tc.Items[0], true, "booking_confirmation")
	in.BodyChars = 0
	if _, e = s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubmitTriage(ctx, decision); !errors.Is(e, ErrTriageChanged) {
		t.Fatal("stale body result accepted", e)
	}
	in.Enabled = false
	if _, e = s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	tc, e = s.Triage(ctx)
	if e != nil || tc.Enabled || len(tc.Items) != 0 {
		t.Fatal(tc, e)
	}
}

func TestFromExclusionPrecedesModelAndCanBeCancelled(t *testing.T) {
	s, p := importSetup(t)
	ctx := context.Background()
	in := defaultInboxSettings()
	in.Enabled, in.LLMEnabled = true, true
	if _, e := s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	before, e := s.Triage(ctx)
	if e != nil || len(before.Items) != 2 {
		t.Fatal(before, e)
	}
	stale := triageChoice(before.Items[0], true, "booking_confirmation")
	in.ExcludedSenders = []string{"SENDER@EXAMPLE.COM"}
	if _, e = s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	blocked, e := s.Triage(ctx)
	if e != nil || len(blocked.Items) != 0 {
		t.Fatal("cached blocked mail reached model", blocked, e)
	}
	if _, e = s.SubmitTriage(ctx, stale); e == nil {
		t.Fatal("model restored a blocked message")
	}
	p.history = `[{"messagesAdded":[{"message":{"id":"blocked-new"}}]}]`
	if _, e = s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	blocked, e = s.Triage(ctx)
	if e != nil || len(blocked.Items) != 0 {
		t.Fatal("new blocked mail reached model", blocked, e)
	}
	in.ExcludedSenders = nil
	if _, e = s.SetInboxSettings(ctx, in); e != nil {
		t.Fatal(e)
	}
	p.history = `[{"messagesAdded":[{"message":{"id":"allowed-new"}},{"message":{"id":"blocked-new"}}]}]`
	if _, e = s.SyncGmail(ctx); e != nil {
		t.Fatal(e)
	}
	allowed, e := s.Triage(ctx)
	if e != nil || len(allowed.Items) != 1 || allowed.Items[0].Message.MessageID != "allowed-new" {
		t.Fatal("cancelled From did not allow future mail or replayed old mail", allowed, e)
	}
}
