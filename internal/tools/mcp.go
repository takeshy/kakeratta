package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// mcpTools exposes the configured catalogue under server-qualified tool names.
func mcpTools(catalogue *mcp.Catalogue) []Tool {
	var out []Tool
	for _, listed := range catalogue.Tools() {
		listed := listed
		name := listed.QualifiedName()
		out = append(out, Tool{
			Name:       name,
			ReadOnly:   listed.Annotations.ReadOnlyHint,
			Usage:      describeMCP(name, listed.Tool),
			Parameters: parametersOf(listed.InputSchema),
			run: func(ctx context.Context, r *Registry, args Args, _ *timeline.Place) (Result, error) {
				if err := checkArgs(listed.InputSchema, args); err != nil {
					return Result{}, err
				}
				result, err := r.config.MCP.Call(ctx, name, map[string]any(args))
				if err != nil {
					return Result{}, err
				}
				return fromMCP(result)
			},
		})
	}
	return out
}

// parametersOf hands a server's own schema straight through: it is already
// JSON Schema, and rewriting it here could only lose something.
func parametersOf(raw json.RawMessage) map[string]any {
	var parameters map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &parameters) != nil {
		return nil
	}
	if _, ok := parameters["type"]; !ok {
		parameters["type"] = "object"
	}
	return parameters
}

// describeMCP writes the one usage line a persona is shown, in the same shape
// as the built-in tools: the name and what it does. The argument names are not
// spelled out here because the server's own schema travels with the tool.
func describeMCP(name string, tool mcp.Tool) string {
	description := strings.Join(strings.Fields(tool.Description), " ")
	if description == "" {
		description = strings.TrimSpace(tool.Title)
	}
	if len([]rune(description)) > 300 {
		description = string([]rune(description)[:300]) + "…"
	}
	return name + " — " + description
}

type schema struct {
	Type        any               `json:"type"`
	Description string            `json:"description"`
	Enum        []any             `json:"enum"`
	Properties  map[string]schema `json:"properties"`
	Required    []string          `json:"required"`
	Items       *schema           `json:"items"`
}

func (s schema) typeName() string {
	switch value := s.Type.(type) {
	case string:
		return value
	case []any:
		for _, candidate := range value {
			if name, ok := candidate.(string); ok && name != "null" {
				return name
			}
		}
	}
	if len(s.Properties) > 0 {
		return "object"
	}
	return ""
}

// checkArgs refuses a call the server would refuse anyway, but says why in the
// persona's language: a missing required argument or a wrong basic type.
func checkArgs(raw json.RawMessage, args Args) error {
	var root schema
	if len(raw) == 0 || json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var missing []string
	for _, name := range root.Required {
		if _, ok := args[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("引数が足りません: %s", strings.Join(missing, ", "))
	}
	for name, property := range root.Properties {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		if want := property.typeName(); want != "" && !matchesType(want, value) {
			return fmt.Errorf("引数 %s は %s で渡してください", name, want)
		}
	}
	return nil
}

func matchesType(want string, value any) bool {
	switch want {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	}
	return true
}

// fromMCP flattens a tool result into prompt text and links. Text goes in as
// written; resource links become sources, since a link is what lets a reader
// check a claim. A result the tool marked as an error is reported as one.
func fromMCP(result mcp.CallResult) (Result, error) {
	var lines []string
	var sources []timeline.Source
	for _, item := range result.Content {
		switch item.Type {
		case "text":
			if text := strings.TrimSpace(item.Text); text != "" {
				lines = append(lines, text)
			}
		case "resource_link":
			label := firstNonEmpty(item.Title, item.Name, item.Description, item.URI)
			lines = append(lines, "- "+label+": "+item.URI)
			if isWebURL(item.URI) {
				sources = append(sources, timeline.Source{Label: label, URL: item.URI})
			}
		case "resource":
			if item.Resource == nil {
				continue
			}
			if text := strings.TrimSpace(item.Resource.Text); text != "" {
				lines = append(lines, text)
			}
			if isWebURL(item.Resource.URI) {
				sources = append(sources, timeline.Source{Label: item.Resource.URI, URL: item.Resource.URI})
			}
		case "image", "audio":
			lines = append(lines, "（"+item.Type+" は表示できません）")
		}
	}
	if len(lines) == 0 && len(result.StructuredContent) > 0 {
		lines = append(lines, string(result.StructuredContent))
	}
	text := strings.Join(lines, "\n")
	const limit = 16000
	if runes := []rune(text); len(runes) > limit {
		text = string(runes[:limit]) + "\n…（長いので省略）"
	}
	if result.IsError {
		if text == "" {
			text = "理由は示されませんでした"
		}
		return Result{}, fmt.Errorf("道具がエラーを返しました: %s", text)
	}
	if text == "" {
		text = "結果は空でした。"
	}
	return Result{Text: text, Sources: sources}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isWebURL(value string) bool {
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")
}
