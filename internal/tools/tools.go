// Package tools is what a persona can reach outside the conversation.
//
// A skill declares external tools; timeline search is shared by all personas.
// The app makes every call itself: the credentials, the provider and
// the rule that a claim comes back with a link are the app's to hold, not each
// skill's. A food persona gets shops and opening hours; a travel one gets
// routes and times; neither can reach the other's.
//
// Tools come from two places. A few are built in. The rest are whatever MCP
// servers the operator configured, listed under server.tool; a skill declares
// those by the same name and a persona calls them the same way.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/takeshy/kakeratta/internal/search"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// Result is what a tool hands back: text for the prompt, and the links behind
// it so a remark can cite where it got something.
type Result struct {
	Text    string            `json:"text"`
	Sources []timeline.Source `json:"sources,omitempty"`
	// Citable are the saved posts a lookup found. Unlike Sources they are not
	// hung on the remark as they are: the persona names the ones it rested on
	// by id, and only those are linked.
	Citable []Citable `json:"citable,omitempty"`
}

// Citable is one saved post a persona may cite, by id, with the line the
// link shows.
type Citable struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	ReadOnly bool
	Name     string
	Usage    string
	// Parameters is the JSON Schema for the arguments, so the model is given
	// the shape rather than asked to copy an example out of prose.
	Parameters map[string]any
	run        func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error)
}

// Declaration is one tool as the model is offered it, in a shape this package
// can hand over without knowing anything about a provider.
type Declaration struct {
	ReadOnly    bool
	Name        string
	Description string
	Parameters  map[string]any
}

// Registry holds every tool the app knows how to run, already wired to the
// operator's configuration. A persona is given a subset of it.
type Registry struct {
	config    Config
	tools     map[string]Tool
	search    *search.Service
	schedules Schedules
	// zone is the user's own, for a schedule that did not name one.
	zone string
	// tavilyKey is the user's own; web_search is the one tool whose credential
	// belongs to the workspace rather than the deployment.
	tavilyKey string
}

// New lists the built-in tools and whatever the configured MCP servers offer.
// The MCP list comes from a shared cache, so this is cheap to call per post.
// A built-in place tool that a server also offers goes to the server first.
func New(config Config) *Registry {
	r := &Registry{config: config, tools: map[string]Tool{}}
	listed := config.MCP.Tools()
	for _, tool := range []Tool{webSearch, placeSearch, routeTo, searchTimeline, scheduleTool} {
		r.tools[tool.Name] = preferMCP(tool, listed)
	}
	for _, tool := range mcpTools(config.MCP) {
		if _, taken := r.tools[tool.Name]; !taken {
			r.tools[tool.Name] = tool
		}
	}
	return r
}

// Timeline search is a shared capability of every persona in this workspace.
func (r *Registry) WithSearch(service *search.Service) *Registry {
	r.search = service
	return r
}

// WithSchedules lets a persona ask the app to wake it. Without it the tool
// stays listed and says it is not configured when asked.
func (r *Registry) WithSchedules(schedules Schedules) *Registry {
	r.schedules = schedules
	return r
}

// WithZone is the workspace's time zone, used when a persona sets a schedule
// without naming one. An offset in an RFC3339 string is not a zone: it cannot
// survive daylight saving, and Go reads it back as "Local", which on a server
// means UTC.
func (r *Registry) WithZone(zone string) *Registry {
	r.zone = zone
	return r
}

// WithTavilyKey hands web_search the workspace's key. Without one, the tool
// stays listed and says it is not configured when asked.
func (r *Registry) WithTavilyKey(key string) *Registry {
	r.tavilyKey = key
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

// Set includes declared external tools and the shared timeline search.
type Set struct {
	registry *Registry
	allowed  map[string]bool
	owner    string
}

// As names the persona whose set this is. A schedule belongs to the one that
// asked for it, so the owner travels with the call rather than with the
// arguments a model wrote.
func (s *Set) As(owner string) *Set {
	s.owner = owner
	return s
}

// For adds shared timeline search and drops unknown declared tool names.
func (r *Registry) For(declared []string) *Set {
	allowed := map[string]bool{}
	if r.search != nil {
		allowed["timeline_search"] = true
	}
	for _, name := range declared {
		name = strings.TrimSpace(name)
		if _, known := r.tools[name]; known {
			allowed[name] = true
		}
	}
	return &Set{registry: r, allowed: allowed}
}

// Allows reports whether this persona may call one tool by name.
func (s *Set) Allows(name string) bool { return s.allowed[strings.TrimSpace(name)] }

// IsReadOnly is the execution boundary for research. Unannotated MCP tools
// are actions; configured servers can explicitly advertise readOnlyHint.
func (s *Set) IsReadOnly(name string) bool {
	if !s.Allows(name) {
		return false
	}
	switch name {
	case "web_search", "place_search", "route", "timeline_search":
		return true
	}
	return s.registry.tools[name].ReadOnly
}

// Declarations lists what this persona may call, for a request that carries
// its tools rather than describing them.
func (s *Set) Declarations() []Declaration {
	var names []string
	for name := range s.allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Declaration, 0, len(names))
	for _, name := range names {
		tool := s.registry.tools[name]
		out = append(out, Declaration{Name: name, Description: tool.Usage, Parameters: tool.Parameters, ReadOnly: s.IsReadOnly(name)})
	}
	return out
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
	return tool.run(context.WithValue(ctx, ownerKey{}, s.owner), s.registry, args, at)
}

// ErrNoPlace is what the location-shaped tools return when the post had no
// position. A post from a desktop has no meaningful one, and that has to be an
// ordinary answer rather than a failure.
var ErrNoPlace = errors.New("この投稿には位置がありません")
