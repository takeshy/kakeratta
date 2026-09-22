package persona

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// queueHermes hands the trigger off as a durable task and returns at once:
// Hermes does its actual work later, out of process, through its own agent
// calling back over MCP. The bool says whether that later work is now
// pending - the caller must not count this turn as finished while it is, or
// a routing sheet would call Hermes quiet before it has even started.
func (r *Runner) queueHermes(ctx context.Context, trigger timeline.Entry, instructions string) (*timeline.Entry, bool) {
	// Cleanup/listening wakes must not become unsolicited executable requests.
	if instructions == deletedInstructions || instructions == forgottenInstructions {
		return nil, false
	}
	kind := "post"
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(trigger.Text)))
	identity := trigger.ID + ":" + fingerprint
	prompt := ""
	if trigger.ID == "" {
		kind = "check-in"
		// Give Hermes the same check-in brief as an in-process persona. Its
		// skill maps the in-process tool names to the MCP tools it can call.
		prompt = trigger.Text + instructions
		identity = trigger.At.Format(time.RFC3339Nano) + ":" + prompt
	}
	id := fmt.Sprintf("task_%x", sha256.Sum256([]byte(identity)))
	err := store.EnqueueAgentTask(ctx, r.store, store.AgentTask{ID: id, Trigger: trigger.ID, Fingerprint: fingerprint, Kind: kind, Prompt: prompt, At: time.Now()})
	if err != nil {
		r.emit("error", map[string]any{"persona": "hermes", "message": err.Error()})
		return nil, false
	}
	r.emit("agent-task", map[string]string{"persona": "hermes", "id": id, "state": "pending", "trigger": trigger.ID})
	return nil, true
}
