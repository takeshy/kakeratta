package persona

import (
	"fmt"
	"strings"

	"github.com/takeshy/kakeratta/internal/llm"
	"github.com/takeshy/kakeratta/internal/timeline"
	"github.com/takeshy/kakeratta/internal/tools"
)

// readCapabilities is a snapshot, rebuilt from the execution budget before
// every research request. It never owns or increments budget counters.
type readCapabilities struct {
	records         bool
	tools           []tools.Declaration
	readsRemaining  int
	searchRemaining int
}

func currentReadCapabilities(skill Skill, set *tools.Set, budget *lookups, at *timeline.Place, readsRemaining int) readCapabilities {
	c := readCapabilities{records: skill.Remembers(), readsRemaining: readsRemaining}
	if set.ReadAvailable("web_search", at) {
		c.searchRemaining = budget.SearchRemaining()
	}
	for _, d := range set.Declarations() {
		if budget.left() <= 0 || !set.ReadAvailable(d.Name, at) || (d.Name == "web_search" && c.searchRemaining == 0) {
			continue
		}
		c.tools = append(c.tools, d)
	}
	return c
}

func (c readCapabilities) searchStatus() string {
	text := fmt.Sprintf("Web検索: 残り %d 回\n", c.searchRemaining)
	switch c.searchRemaining {
	case 0:
		text += "これ以上Web検索は利用できません。現在得られている情報や、その他の利用可能なreadで十分なら finish_read してください。\n"
	case 1:
		text += "最後のWeb検索枠です。必要な場合だけ使用してください。\n"
	}
	return text
}

const entriesReadSpec = `投稿IDで本文を読むには {"type":"entries","tool":"","args":"{\"ids\":[\"投稿ID\"]}"} を使います。10件までまとめて読めます。`

func (c readCapabilities) instruction() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n# 現在のRead\nread は残り %d 回。過去の説明より、この利用可能なreadと残数を優先してください。追加調査が不要なら finish_read してください。\n%s\n%s\n", c.readsRemaining, c.searchStatus(), entriesReadSpec)
	if c.records {
		b.WriteString(recordsReadSpec)
	}
	for _, d := range c.tools {
		fmt.Fprintf(&b, "\n%s: %s\n%s\n例: {\"type\":\"tool\",\"tool\":%q,\"args\":%s}\n", d.Name, d.Description, argsTable(d), d.Name, jsonString(sampleArgs(d)))
	}
	return b.String()
}

func (c readCapabilities) footer() string {
	names := []string{"entries"}
	if c.records {
		names = append(names, "records")
	}
	for _, d := range c.tools {
		names = append(names, d.Name)
	}
	text := fmt.Sprintf("\n\nread は残り %d 回。\n%s利用可能なread: %s\n", c.readsRemaining, c.searchStatus(), strings.Join(names, " / "))
	if c.readsRemaining <= 0 {
		text += readsLastNote
	}
	return text
}

func (c readCapabilities) tool() llm.Tool {
	// Separate branches bind the operation type to its permitted tool names.
	// args remains a JSON string, preserving the existing read wire format.
	branch := func(kind string, names []string, args string) map[string]any {
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"type": map[string]any{"type": "string", "enum": []string{kind}},
				"tool": map[string]any{"type": "string", "enum": names},
				"args": stringField(args),
			},
			"required": []string{"type", "tool", "args"},
		}
	}
	choices := []any{branch("entries", []string{""}, `投稿IDを指定したJSON文字列。例: {"ids":["投稿ID"]}。10件まで。`)}
	if c.records {
		choices = append(choices, branch("records", []string{""}, `記憶を検索するJSON文字列。tag、query、before、またはidsを指定。`))
	}
	if len(c.tools) > 0 {
		var names []string
		for _, d := range c.tools {
			names = append(names, d.Name)
		}
		choices = append(choices, branch("tool", names, "引数をJSONオブジェクトとして書いた文字列。現在のReadの説明に従ってください。"))
	}
	return llm.Tool{
		Name: readTool.Name, Description: "現在利用可能な手段で調べます。何も書き換えません。\n" + c.searchStatus(),
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"steps": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"anyOf": choices}}},
			"required":   []string{"steps"},
		},
	}
}
