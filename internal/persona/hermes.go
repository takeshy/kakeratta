package persona

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func (r *Runner) queueHermes(ctx context.Context, trigger timeline.Entry, instructions string) *timeline.Entry {
	// Cleanup/listening wakes must not become unsolicited executable requests.
	if instructions == deletedInstructions || instructions == forgottenInstructions {
		return nil
	}
	kind := "post"
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(trigger.Text)))
	identity := trigger.ID + ":" + fingerprint
	prompt := ""
	if trigger.ID == "" {
		kind = "check-in"
		prompt = trigger.Text
		identity = trigger.At.Format(time.RFC3339Nano) + ":" + prompt
	}
	id := fmt.Sprintf("task_%x", sha256.Sum256([]byte(identity)))
	err := store.EnqueueAgentTask(ctx, r.store, store.AgentTask{ID: id, Trigger: trigger.ID, Fingerprint: fingerprint, Kind: kind, Prompt: prompt, At: time.Now()})
	if err != nil {
		r.emit("error", map[string]any{"persona": "hermes", "message": err.Error()})
		return nil
	}
	r.emit("agent-task", map[string]string{"persona": "hermes", "id": id, "state": "pending", "trigger": trigger.ID})
	return nil
}
