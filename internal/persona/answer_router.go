package persona

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// AnswerRouter is operator-owned configuration for research and answer calls.
// The zero value retains Lightning escalation and the workspace's model slots.
type AnswerRouter struct {
	Mode       string
	Client     *llm.Client
	Model      string
	UltraModel string
}

func AnswerRouterFromEnv() (AnswerRouter, error) {
	r := AnswerRouter{Mode: strings.ToLower(strings.TrimSpace(os.Getenv("ROUTER_MODE")))}
	switch r.Mode {
	case "", "lightning":
		return r, nil
	case "switchyard":
	default:
		return r, fmt.Errorf("ROUTER_MODE must be lightning or switchyard")
	}
	base := firstSet(os.Getenv("SWITCHYARD_BASE_URL"), "http://127.0.0.1:4000/v1")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return r, fmt.Errorf("SWITCHYARD_BASE_URL must be an HTTP(S) API base URL without credentials, query or fragment")
	}
	r.Client = llm.New(base, os.Getenv("SWITCHYARD_API_KEY"))
	r.Model = firstSet(os.Getenv("SWITCHYARD_MODEL"), "kakeratta-auto")
	r.UltraModel = firstSet(os.Getenv("SWITCHYARD_ULTRA_MODEL"), "kakeratta-ultra")
	return r, nil
}

func (r AnswerRouter) Switchyard() bool { return r.Mode == "switchyard" }

// Select preserves explicit Ultra requests through a fixed route. Provider
// credentials are never copied from the workspace to the routing server.
func (r AnswerRouter) Select(client *llm.Client, model, slot string, deep bool) (*llm.Client, string, string) {
	if !r.Switchyard() {
		return client, model, slot
	}
	if deep {
		return r.Client, r.UltraModel, "ultra"
	}
	return r.Client, r.Model, "switchyard"
}

// SetAnswerRouter must be called before Start.
func (r *Runner) SetAnswerRouter(router AnswerRouter) { r.answerRouter = router }

// ReplayAnswer evaluates a captured answer-stage transcript without running
// tools or writing workspace state. It uses the runtime escalation and answer
// parser, including the same schema and repair attempts.
func ReplayAnswer(ctx context.Context, router AnswerRouter, client *llm.Client, settings store.Settings, messages []llm.Message, previous, reply string, alwaysUltra bool) (*Answer, error) {
	deep := alwaysUltra
	if !router.Switchyard() && previous != "" && reply != "" {
		deep = deep || Escalate(ctx, client, lightningModel(settings), timeline.Entry{Text: previous}, timeline.Entry{Text: reply})
	}
	model, slot := personaModel(settings), "super"
	if deep {
		model, slot = ultraModel(settings), "ultra"
	}
	client, model, _ = router.Select(client, model, slot, deep)
	return composeAnswer(ctx, client, model, messages, turnRules{named: true, cited: map[string]string{}})
}
