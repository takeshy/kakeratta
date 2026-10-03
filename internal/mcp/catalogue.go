package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Listed is one tool with the server it came from. Its qualified name is
// what a skill declares and a persona writes.
type Listed struct {
	Server string
	Tool
}

// QualifiedName is server.tool. The dot separates because tool names in MCP
// commonly use underscores, and no server name contains one.
func (l Listed) QualifiedName() string { return l.Server + "." + l.Tool.Name }

// Split takes a qualified name apart. The second value is false for names
// without a server prefix, which are the built-in tools.
func Split(qualified string) (server, tool string, ok bool) {
	server, tool, ok = strings.Cut(qualified, ".")
	if !ok || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// Catalogue is every configured server's tool list, fetched once and refreshed
// in the background. A persona is created for every reaction; asking each
// server for its tools every time would make the servers the bottleneck.
//
// A server that stops answering keeps its last known list: a tool that then
// fails to run tells the persona so, which is better than the tool vanishing
// from the prompt between two posts.
type Catalogue struct {
	clients []*Client
	ttl     time.Duration

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	tools     []Tool
	fetchedAt time.Time
	err       error
	fetching  bool
}

// NewCatalogue wires the servers. Nothing is fetched until Refresh or Tools.
func NewCatalogue(servers []Server, version string) *Catalogue {
	c := &Catalogue{ttl: 5 * time.Minute, entries: map[string]*entry{}}
	for _, server := range servers {
		c.clients = append(c.clients, NewClient(server, version))
		c.entries[server.Name] = &entry{}
	}
	return c
}

// Servers lists the configured names, for logs and the about endpoint.
func (c *Catalogue) Servers() []string {
	if c == nil {
		return nil
	}
	var names []string
	for _, client := range c.clients {
		names = append(names, client.Server().Name)
	}
	return names
}

// Refresh fetches every server's list now and returns the first failure.
func (c *Catalogue) Refresh(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, len(c.clients))
	for i, client := range c.clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.fetch(ctx, client)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (c *Catalogue) fetch(ctx context.Context, client *Client) error {
	name := client.Server().Name
	tools, err := client.ListTools(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[name]
	e.fetching = false
	e.err = err
	if err != nil {
		log.Printf("mcp: %s の道具一覧を取得できませんでした: %v", name, err)
		if e.fetchedAt.IsZero() {
			// Nothing to keep; try again soon rather than in ttl.
			e.fetchedAt = time.Now().Add(-c.ttl).Add(30 * time.Second)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	e.tools = tools
	e.fetchedAt = time.Now()
	return nil
}

// Tools returns every known tool, sorted by qualified name. A stale list is
// refreshed in the background; a list that was never fetched is fetched now,
// because a persona about to be prompted has nothing to show otherwise.
func (c *Catalogue) Tools() []Listed {
	return c.ToolsContext(context.Background())
}

// ToolsContext bounds initial discovery by the caller's deadline.
func (c *Catalogue) ToolsContext(ctx context.Context) []Listed {
	if c == nil {
		return nil
	}
	var wait []*Client
	c.mu.Lock()
	for _, client := range c.clients {
		e := c.entries[client.Server().Name]
		if e.fetching || time.Since(e.fetchedAt) < c.ttl {
			continue
		}
		e.fetching = true
		if e.fetchedAt.IsZero() || (e.err != nil && len(e.tools) == 0) {
			wait = append(wait, client)
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = c.fetch(ctx, client)
		}()
	}
	c.mu.Unlock()
	if len(wait) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for _, client := range wait {
			wg.Add(1)
			go func() { defer wg.Done(); _ = c.fetch(ctx, client) }()
		}
		wg.Wait()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	var listed []Listed
	for _, client := range c.clients {
		name := client.Server().Name
		for _, tool := range c.entries[name].tools {
			listed = append(listed, Listed{Server: name, Tool: tool})
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].QualifiedName() < listed[j].QualifiedName() })
	return listed
}

// Call runs server.tool.
func (c *Catalogue) Call(ctx context.Context, qualified string, args map[string]any) (CallResult, error) {
	server, tool, ok := Split(qualified)
	if !ok || c == nil {
		return CallResult{}, errors.New("知らない道具です")
	}
	for _, client := range c.clients {
		if client.Server().Name == server {
			return client.CallTool(ctx, tool, args)
		}
	}
	return CallResult{}, fmt.Errorf("MCPサーバー %s は設定されていません", server)
}
