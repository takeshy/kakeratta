package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// mcpTools turns the catalogue into Tools. A persona sees "geo.route" next to
// "route" and asks for either the same way; only Run knows where each goes.
func mcpTools(catalogue *mcp.Catalogue) []Tool {
	var out []Tool
	for _, listed := range catalogue.Tools() {
		listed := listed
		name := listed.QualifiedName()
		out = append(out, Tool{
			Name:  name,
			Usage: describeMCP(name, listed.Tool),
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

// describeMCP writes the one usage line a persona is shown, in the same shape
// as the built-in tools: name, what it does, and an example call built from
// the input schema so the model has the argument names in front of it.
func describeMCP(name string, tool mcp.Tool) string {
	description := strings.Join(strings.Fields(tool.Description), " ")
	if description == "" {
		description = strings.TrimSpace(tool.Title)
	}
	if len([]rune(description)) > 300 {
		description = string([]rune(description)[:300]) + "…"
	}
	example, optional := exampleArgs(tool.InputSchema)
	call := map[string]any{"tool": name}
	if len(example) > 0 {
		call["args"] = example
	}
	encoded, _ := json.Marshal(call)
	line := name + " — " + description + " " + string(encoded)
	if len(optional) > 0 {
		line += " （任意: " + strings.Join(optional, ", ") + "）"
	}
	return line
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

// exampleArgs builds a sample argument object: every required property, then
// the optional ones by name. Enums are shown as a|b like the built-in tools do.
func exampleArgs(raw json.RawMessage) (map[string]any, []string) {
	var root schema
	if len(raw) == 0 || json.Unmarshal(raw, &root) != nil || len(root.Properties) == 0 {
		return nil, nil
	}
	required := map[string]bool{}
	for _, name := range root.Required {
		required[name] = true
	}
	example := map[string]any{}
	var optional []string
	for name, property := range root.Properties {
		if required[name] {
			example[name] = placeholder(property, 0)
		} else {
			optional = append(optional, name)
		}
	}
	sort.Strings(optional)
	if len(example) == 0 && len(optional) > 0 {
		// Nothing is required, so show the first few optional ones instead of
		// an empty object that teaches the model nothing.
		for _, name := range optional[:min(3, len(optional))] {
			example[name] = placeholder(root.Properties[name], 0)
		}
		optional = optional[min(3, len(optional)):]
	}
	return example, optional
}

func placeholder(property schema, depth int) any {
	if len(property.Enum) > 0 {
		var names []string
		for _, value := range property.Enum {
			names = append(names, fmt.Sprint(value))
		}
		return strings.Join(names, "|")
	}
	switch property.typeName() {
	case "number", "integer":
		return 0
	case "boolean":
		return false
	case "array":
		if property.Items != nil && depth < 2 {
			return []any{placeholder(*property.Items, depth+1)}
		}
		return []any{}
	case "object":
		nested := map[string]any{}
		if depth < 2 {
			for name, child := range property.Properties {
				nested[name] = placeholder(child, depth+1)
			}
		}
		return nested
	default:
		if property.Description != "" {
			words := strings.Join(strings.Fields(property.Description), " ")
			if len([]rune(words)) > 40 {
				words = string([]rune(words)[:40]) + "…"
			}
			return words
		}
		return "…"
	}
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
