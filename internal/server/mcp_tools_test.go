package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

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
	server := &Server{store: w, events: newHub()}
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
