package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type failingMCPWorkspace struct {
	store.Workspace
	readErr error
}

func (w *failingMCPWorkspace) Timeline() ([]timeline.Entry, error) {
	if w.readErr != nil {
		return nil, w.readErr
	}
	return w.Workspace.Timeline()
}

func mcpTaskFixture(t *testing.T) (*Server, *failingMCPWorkspace, store.AgentTask) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.Active = []string{"hermes"}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	w := &failingMCPWorkspace{Workspace: s}
	server := &Server{store: w, events: newHub(), catalogue: persona.NewCatalogue(s.SkillDir())}
	entry := timeline.Entry{ID: "request", Root: "request", Kind: timeline.KindPost, Text: "do work", At: time.Now()}
	if err := s.Append(entry); err != nil {
		t.Fatal(err)
	}
	task := store.AgentTask{ID: "task", Trigger: entry.ID, Fingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(entry.Text))), Kind: "post", At: time.Now()}
	if err := store.EnqueueAgentTask(context.Background(), w, task); err != nil {
		t.Fatal(err)
	}
	return server, w, task
}

func TestMCPTaskReadFailurePreservesWork(t *testing.T) {
	for _, state := range []string{"pending", "claimed", "completed"} {
		t.Run(state, func(t *testing.T) {
			s, w, task := mcpTaskFixture(t)
			ctx := context.Background()
			if state != "pending" {
				if _, err := store.ClaimAgentTask(ctx, w, task.ID, "execution"); err != nil {
					t.Fatal(err)
				}
			}
			if state == "completed" {
				if _, err := store.FinishAgentTask(ctx, w, task.ID, "execution", "done"); err != nil {
					t.Fatal(err)
				}
			}
			outage := errors.New("temporary storage outage")
			w.readErr = outage
			if _, err := s.callMCPTool(ctx, "list_tasks", json.RawMessage(`{}`)); !errors.Is(err, outage) {
				t.Fatalf("error = %v", err)
			}
			tasks, err := store.AgentTasks(ctx, w)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks = %+v, error = %v", tasks, err)
			}
			if tasks[0].State != state || tasks[0].Published {
				t.Fatalf("task changed during outage: %+v", tasks[0])
			}
			if state == "completed" {
				if tasks[0].Result != "done" {
					t.Fatal("accepted result lost")
				}

			}
			w.readErr = nil
			if _, err := s.callMCPTool(ctx, "list_tasks", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			tasks, err = store.AgentTasks(ctx, w)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks = %+v, error = %v", tasks, err)
			}
			if tasks[0].Published != (state == "completed") {
				t.Fatalf("unexpected recovered state: %+v", tasks[0])
			}
			if state == "completed" {
				id := fmt.Sprintf("mcp_%x", sha256.Sum256([]byte("task:"+task.ID)))
				entry, ok := w.Entry(id)
				if !ok || entry.Text != "done" || entry.ReplyTo != task.Trigger {
					t.Fatalf("recovered result = %+v, exists = %v", entry, ok)
				}
				if _, err := s.callMCPTool(ctx, "complete_task", json.RawMessage(`{"taskId":"task","claimId":"execution","text":"replacement"}`)); err != nil {
					t.Fatal(err)
				}
				entries, err := w.Timeline()
				if err != nil || len(entries) != 2 {
					t.Fatalf("duplicate delivery: %+v, error = %v", entries, err)
				}
				entry, _ = w.Entry(id)
				if entry.Text != "done" {
					t.Fatal("retry replaced accepted result")
				}
			}
		})
	}
}

func TestMCPTaskConfirmedInvalidation(t *testing.T) {
	for _, change := range []string{"edited", "deleted", "root-deleted"} {
		t.Run(change, func(t *testing.T) {
			s, w, task := mcpTaskFixture(t)
			ctx := context.Background()
			entry, _ := w.Entry(task.Trigger)
			if change == "root-deleted" {
				entry.Kind = timeline.KindRemark
				entry.Root = "deleted-root"
				if err := w.Append(timeline.Entry{ID: entry.Root, Root: entry.Root, Kind: timeline.KindPost, Deleted: true}); err != nil {
					t.Fatal(err)
				}
			} else if change == "deleted" {
				entry.Deleted = true
			} else {
				entry.Text = "changed request"
			}
			if err := w.Append(entry); err != nil {
				t.Fatal(err)
			}
			if _, err := s.callMCPTool(ctx, "list_tasks", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			tasks, err := store.AgentTasks(ctx, w)
			if err != nil || len(tasks) != 1 || tasks[0].State != "completed" || !tasks[0].Published {
				t.Fatalf("tasks = %+v, error = %v", tasks, err)
			}
		})
	}
}

func TestMCPCompleteTaskRetriesDeliveryAfterReadFailure(t *testing.T) {
	s, w, task := mcpTaskFixture(t)
	ctx := context.Background()
	if _, err := store.ClaimAgentTask(ctx, w, task.ID, "execution"); err != nil {
		t.Fatal(err)
	}
	outage := errors.New("temporary storage outage")
	w.readErr = outage
	args := json.RawMessage(`{"taskId":"task","claimId":"execution","text":"done"}`)
	if _, err := s.callMCPTool(ctx, "complete_task", args); !errors.Is(err, outage) {
		t.Fatalf("error = %v", err)
	}
	tasks, err := store.AgentTasks(ctx, w)
	if err != nil || len(tasks) != 1 || tasks[0].State != "completed" || tasks[0].Published || tasks[0].Result != "done" {
		t.Fatalf("tasks = %+v, error = %v", tasks, err)
	}
	w.readErr = nil
	// A deleted request suppresses delivery, but only after a successful read.
	entry, _ := w.Entry(task.Trigger)
	entry.Deleted = true
	if err := w.Append(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(ctx, "complete_task", args); err != nil {
		t.Fatal(err)
	}
	tasks, err = store.AgentTasks(ctx, w)
	if err != nil || !tasks[0].Published {
		t.Fatalf("tasks = %+v, error = %v", tasks, err)
	}
}

func TestMCPClaimRecoveryMetadata(t *testing.T) {
	s, w, _ := mcpTaskFixture(t)
	ctx := context.Background()
	result, err := s.callMCPTool(ctx, "claim_task", json.RawMessage(`{"taskId":"task","claimId":"execution"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Task struct {
			Status    string     `json:"status"`
			ClaimID   string     `json:"claimId"`
			ClaimedAt *time.Time `json:"claimedAt"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Task.Status != "claimed" || response.Task.ClaimID != "execution" || response.Task.ClaimedAt == nil {
		t.Fatalf("claim response = %s", raw)
	}
	result, err = s.callMCPTool(ctx, "list_tasks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tasks []struct {
			Status    string     `json:"status"`
			ClaimID   string     `json:"claimId"`
			ClaimedAt *time.Time `json:"claimedAt"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].Status != "claimed" || listed.Tasks[0].ClaimID != "execution" || listed.Tasks[0].ClaimedAt == nil || !listed.Tasks[0].ClaimedAt.Equal(*response.Task.ClaimedAt) {
		t.Fatalf("list response = %s", raw)
	}
	// Existing queue JSON lacks claimedAt; do not invent a timestamp on retry.
	v, err := w.SocialValue(ctx, "mcp-hermes-tasks")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := w.SwapSocialValue(ctx, "mcp-hermes-tasks", v.Version, store.SocialValue{Data: json.RawMessage(`[{"id":"legacy","kind":"check-in","state":"claimed","claim":"old-execution"}]`)})
	if err != nil || !ok {
		t.Fatalf("legacy setup: %v, %v", ok, err)
	}
	result, err = s.callMCPTool(ctx, "claim_task", json.RawMessage(`{"taskId":"legacy","claimId":"old-execution"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Task.Status != "claimed" || response.Task.ClaimID != "old-execution" || response.Task.ClaimedAt != nil {
		t.Fatalf("legacy response = %s", raw)
	}
}

func TestMCPDisabledBlocksEveryTool(t *testing.T) {
	s, w, _ := mcpTaskFixture(t)
	settings := w.Settings()
	settings.Active = nil
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for _, tool := range kakerattaMCPTools() {
		t.Run(tool.Name, func(t *testing.T) {
			result, err := s.callMCPTool(context.Background(), tool.Name, json.RawMessage(`{}`))
			if err == nil || err.Error() != "担当画面でHermesを有効にしてください" || result != nil {
				t.Fatalf("disabled tool returned %v, %v", result, err)
			}
		})
	}
	settings.Active = []string{"hermes"}
	if err := w.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callMCPTool(context.Background(), "get_thread", json.RawMessage(`{"entryId":"request"}`)); err != nil {
		t.Fatal(err)
	}
}
