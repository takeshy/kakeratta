// Package tools is what a persona can reach outside the conversation.
//
// A skill declares which of these it needs, and only the declared ones can be
// called. The app makes every call itself: the credentials, the provider and
// the rule that a claim comes back with a link are the app's to hold, not each
// skill's. A food persona gets shops and opening hours; a travel one gets
// routes and times; neither can reach the other's.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Result is what a tool hands back: text for the prompt, and the links behind
// it so a remark can cite where it got something.
type Result struct {
	Text    string            `json:"text"`
	Sources []timeline.Source `json:"sources,omitempty"`
}

// Args are the parameters a persona passed. They arrive as free JSON because
// the model writes them.
type Args map[string]any

func (a Args) str(name string) string {
	if value, ok := a[name].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func (a Args) num(name string, fallback float64) float64 {
	switch value := a[name].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	}
	return fallback
}

// Tool is one capability, with the line that tells a persona how to ask for it.
type Tool struct {
	Name  string
	Usage string
	run   func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error)
}

// Registry holds every tool the app knows how to run, already wired to the
// settings. A persona is given a subset of it.
type Registry struct {
	settings store.Settings
	tools    map[string]Tool
}

func New(settings store.Settings) *Registry {
	r := &Registry{settings: settings, tools: map[string]Tool{}}
	for _, tool := range []Tool{webSearch, placeSearch, routeTo} {
		r.tools[tool.Name] = tool
	}
	return r
}

// Names lists every tool a skill may declare, for the settings screen.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Set is the tools one persona declared. Anything outside it does not exist as
// far as that persona is concerned.
type Set struct {
	registry *Registry
	allowed  map[string]bool
}

// For builds the set a skill declared, dropping names the app does not have.
func (r *Registry) For(declared []string) *Set {
	allowed := map[string]bool{}
	for _, name := range declared {
		name = strings.TrimSpace(name)
		if _, known := r.tools[name]; known {
			allowed[name] = true
		}
	}
	return &Set{registry: r, allowed: allowed}
}

// Empty reports whether this persona declared no usable tools.
func (s *Set) Empty() bool { return len(s.allowed) == 0 }

// Describe writes the tool list for the prompt.
func (s *Set) Describe() string {
	if s.Empty() {
		return ""
	}
	var names []string
	for name := range s.allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		lines = append(lines, "- "+s.registry.tools[name].Usage)
	}
	return strings.Join(lines, "\n")
}

// Run calls one tool. A name the skill did not declare is refused here rather
// than anywhere further in: the declaration is the boundary.
func (s *Set) Run(ctx context.Context, name string, args Args, at *timeline.Place) (Result, error) {
	name = strings.TrimSpace(name)
	if !s.allowed[name] {
		return Result{}, fmt.Errorf("%s は使えません（宣言されていません）", name)
	}
	tool, ok := s.registry.tools[name]
	if !ok {
		return Result{}, errors.New("知らない道具です")
	}
	return tool.run(ctx, s.registry, args, at)
}

// ErrNoPlace is what the location-shaped tools return when the post had no
// position. A post from a desktop has no meaningful one, and that has to be an
// ordinary answer rather than a failure.
var ErrNoPlace = errors.New("この投稿には位置がありません")
