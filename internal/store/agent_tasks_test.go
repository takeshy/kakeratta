package store

import (
	"context"
	"testing"
	"time"
)

func TestAgentClaimSurvivesRestartAndRejectsTakeover(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnqueueAgentTask(ctx, s, AgentTask{ID: "task", Kind: "post", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	claimed, err := ClaimAgentTask(ctx, s, "task", "execution")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedAt == nil || claimed.ClaimedAt.Before(before) || claimed.ClaimedAt.After(time.Now()) {
		t.Fatalf("claimedAt = %v", claimed.ClaimedAt)
	}
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := ClaimAgentTask(ctx, s, "task", "execution")
	if err != nil || retry.ClaimedAt == nil || !retry.ClaimedAt.Equal(*claimed.ClaimedAt) {
		t.Fatalf("retry = %+v, error = %v", retry, err)
	}
	if _, err := ClaimAgentTask(ctx, s, "task", "different-execution"); err == nil {
		t.Fatal("another execution took a claimed task")
	}
	if _, err := FinishAgentTask(ctx, s, "task", "different-execution", "done"); err == nil {
		t.Fatal("another execution completed a claimed task")
	}
	if err := CancelAgentTask(ctx, s, "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimAgentTask(ctx, s, "task", "different-execution"); err == nil {
		t.Fatal("cancelled task was reclaimed")
	}
	retry, err = ClaimAgentTask(ctx, s, "task", "execution")
	if err != nil || retry.State != "completed" {
		t.Fatalf("cancelled task became executable: %+v, %v", retry, err)
	}
	if err := EnqueueAgentTask(ctx, s, AgentTask{ID: "new-task", Kind: "post", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimAgentTask(ctx, s, "new-task", "new-execution"); err != nil {
		t.Fatal(err)
	}
}
