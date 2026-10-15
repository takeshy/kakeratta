package persona

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// The external agent owns Read + Answer only. After accepting its answer,
// Kakeratta's own model plans the schedule phase, reviews
// and executes the changes, then publishes the answer; its note keeper writes
// the notes after that. Timeouts return research and answering to the
// built-in model, without giving the agent write privileges.
//
// An ask is kept in the workspace (store.RecordAsks), not held by a waiting
// goroutine: the turn that opens it ends there, any process serving the
// workspace can answer the agent's claim, reads and answer, and once the ask
// is over - answered, released, timed out or set aside - a reaction carries
// the turn on from where it stopped (asks.go).

// Ask is one planning request, as an external agent sees it.
type Ask struct {
	ID        string     `json:"id"`
	Persona   string     `json:"persona"`
	Name      string     `json:"personaName"`
	Wake      string     `json:"wake"`
	Trigger   string     `json:"trigger,omitempty"`
	State     string     `json:"status"`
	ClaimID   string     `json:"claimId,omitempty"`
	At        time.Time  `json:"at"`
	ClaimBy   time.Time  `json:"claimBy"`
	ClaimedAt *time.Time `json:"claimedAt"`
	PlanBy    *time.Time `json:"planBy,omitempty"`
}

// AskContext is what the agent is given on claiming an ask: the same things
// the in-process planner reads, without duplicating them as full prompts.
type AskContext struct {
	Persona      AskPersona `json:"persona"`
	Instructions string     `json:"instructions"`
	Rules        string     `json:"rules"`
	Place        string     `json:"place"`
	Turn         string     `json:"turn,omitempty"`
	// Capabilities are the changes the later phases can make, so the answer
	// promises nothing the persona cannot carry out.
	Capabilities string `json:"capabilities"`

	Wake      string         `json:"wake"`
	WakeNote  string         `json:"wakeInstructions,omitempty"`
	Now       string         `json:"now"`
	Trigger   AskEntry       `json:"trigger"`
	Thread    []AskEntry     `json:"thread"`
	Memory    string         `json:"memory"`
	Schedules string         `json:"schedules"`
	Answer    map[string]any `json:"answerSchema"`
	Tools     []AskTool      `json:"tools"`
	Limits    AskLimits      `json:"limits"`
}

type AskPersona struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"`
	Tone   string `json:"tone"`
	Domain string `json:"domain"`
}

type AskEntry struct {
	ID       string `json:"id,omitempty"`
	Speaker  string `json:"speaker"`
	At       string `json:"at"`
	Text     string `json:"text"`
	Rendered string `json:"rendered"`
}

type AskTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Use is always "read": the agent looks things up through read_for_ask.
	// Tools that change state belong to the app's own later phases.
	Use string `json:"use"`
}

type AskLimits struct {
	Reads    int `json:"reads"`
	Searches int `json:"searches"`
}

// AskVerdict is the app's answer to a submitted answer.
type AskVerdict struct {
	Accepted bool     `json:"accepted"`
	Problems []string `json:"problems,omitempty"`
	// Closed means the ask is over: accepted, or out of drafts and handed
	// back to the in-process planner.
	Closed bool   `json:"closed"`
	Note   string `json:"note,omitempty"`
}

var (
	ErrAskNotFound = errors.New("依頼が見つかりません。期限切れで内蔵のプランナーに戻ったか、既に終わっています")
	ErrAskClaimed  = errors.New("この依頼は別の claimId で取得されています")
	ErrAskNotYours = errors.New("claimId が一致しません。先に claim_ask で取得してください")
)

// builtinOnlyKey marks a turn that must be planned in-process: one a person
// is waiting on, such as a trial reply, or a tidy-up after a deletion, which
// has no answer to ask an agent for.
type builtinOnlyKey struct{}

func withBuiltinPlanner(ctx context.Context) context.Context {
	return context.WithValue(ctx, builtinOnlyKey{}, true)
}

// askWaits are how long an ask waits to be claimed, and then to be planned.
func (r *Runner) askWaits(settings store.Settings) (time.Duration, time.Duration) {
	if r.waitsForAsk != nil {
		return r.waitsForAsk()
	}
	claim, plan := settings.ExternalAgent.ClaimWait(), settings.ExternalAgent.PlanWait()
	if r.connectionWaits != nil {
		if c, p := r.connectionWaits(); c > 0 || p > 0 {
			if c > 0 {
				claim = c
			}
			if p > 0 {
				plan = p
			}
		}
	}
	if claim <= 0 {
		claim = store.DefaultClaimMinutes * time.Minute
	}
	if plan <= 0 {
		plan = store.DefaultPlanMinutes * time.Minute
	}
	return claim, plan
}

// askTools lists what the persona holds, and where each may be used.
func askTools(set *tools.Set) []AskTool {
	out := []AskTool{
		{Name: "records", Description: "自分のかけらの記録を新しい順に探す。args は {\"tag\":\"…\",\"query\":\"…\",\"before\":\"2026-09\"} のうち要るもの。{\"ids\":[\"…\",\"…\"]} で記録の投稿そのもの（返信なら返信もとも）を10件までまとめて読む。", Use: "read"},
	}
	for _, d := range set.Declarations() {
		if set.IsReadOnly(d.Name) {
			out = append(out, AskTool{Name: d.Name, Description: d.Description, Parameters: d.Parameters, Use: "read"})
		}
	}
	return out
}

func askEntry(entry timeline.Entry, names map[string]string, here *time.Location) AskEntry {
	return AskEntry{ID: entry.ID, Speaker: entry.Speaker, At: entry.At.In(here).Format(time.RFC3339), Text: entry.Text, Rendered: entry.Render(names, here)}
}

// askContext gathers what the agent needs to plan this turn.
func (r *Runner) askContext(skill Skill, set *tools.Set, rules, turn, wakeNote, wake string, thread []timeline.Entry, trigger timeline.Entry,
	memoryNote, schedules string, names map[string]string, here *time.Location, deep bool) AskContext {
	entries := make([]AskEntry, 0, len(thread))
	for _, entry := range thread {
		entries = append(entries, askEntry(entry, names, here))
	}

	return AskContext{
		Persona:      AskPersona{ID: skill.ID, Name: skill.Name, Icon: skill.Icon, Tone: skill.Tone, Domain: skill.Domain},
		Instructions: skill.Instructions,
		Rules:        rules,
		Place:        placeLine(thread[0].Place),
		Turn:         turn,
		Capabilities: strings.TrimSpace(capabilityLine(set)),
		Wake:         wake,
		WakeNote:     wakeNote,
		Now:          time.Now().In(here).Format(time.RFC3339),
		Trigger:      askEntry(trigger, names, here),
		Thread:       entries,
		Memory:       memoryNote,
		Schedules:    schedules,
		Answer:       answerTool.Parameters,
		Tools:        askTools(set),
		Limits:       AskLimits{Reads: turnReadLimit(deep), Searches: turnSearchLimit(deep)},
	}
}
