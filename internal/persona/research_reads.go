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

const entriesReadSpec = `read の args は JSON オブジェクトです。JSONを文字列にしてエスケープしません。
投稿IDが分かっている本文だけを読むには {"type":"entries","tool":"","args":{"ids":["投稿ID"]}} を使います。10件までまとめて読めます。entries に query/from/to は渡せません。期間・語句で投稿を探すときは timeline_search を使います。`

const timelinePeriodReadSpec = `期間内の投稿を確認する例: {"type":"tool","tool":"timeline_search","args":{"from":"2026-10-07 06:30","to":"2026-10-07 14:00"}}。query は省略できます。日時には対象発言の from/to を使い、例の日付をそのまま使いません。これは entries ではありません。`

// Reject an ID read masquerading as a search before spending a read. This
// otherwise leaves the model repeating the same host refusal until exhaustion.
func validateEntryReads(steps []ReadStep, c readCapabilities) error {
	for _, step := range steps {
		if step.Type != "entries" {
			continue
		}
		args, err := argsOf(step.Args)
		ids := entryIDs(args)
		valid := err == nil && len(ids) > 0 && len(ids) <= maxEntryReads
		for key := range args {
			if key != "ids" && key != "id" {
				valid = false
			}
		}
		if value, present := args["ids"]; present {
			items, ok := value.([]any)
			valid = valid && ok && len(items) > 0 && len(items) <= maxEntryReads
			for _, item := range items {
				id, ok := item.(string)
				valid = valid && ok && strings.TrimSpace(id) != ""
			}
		}
		if value, present := args["id"]; present {
			id, ok := value.(string)
			valid = valid && ok && strings.TrimSpace(id) != ""
		}
		if valid {
			continue
		}
		for _, tool := range c.tools {
			if tool.Name == "timeline_search" {
				return fmt.Errorf(`entries は投稿ID専用です。期間・語句の検索は {"type":"tool","tool":"timeline_search","args":{"from":"…","to":"…"}} を使います。entries を同じ検索引数で繰り返さないでください。`)
			}
		}
		return fmt.Errorf("entries には投稿IDを ids で指定してください。現在 timeline_search は利用できません。期間・語句を entries に渡しても検索できません。取得できない記録を未実施と判断せず、同じ引数を繰り返さないでください。")
	}
	return nil
}

func (c readCapabilities) instruction() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n# 現在のRead\nread は残り %d 回。過去の説明より、この利用可能なreadと残数を優先してください。追加調査が不要なら finish_read してください。\n%s\n%s\n", c.readsRemaining, c.searchStatus(), entriesReadSpec)
	if c.records {
		b.WriteString(recordsReadSpec)
	}
	for _, d := range c.tools {
		fmt.Fprintf(&b, "\n%s: %s\n例: {\"type\":\"tool\",\"tool\":%q,\"args\":%s}\n", d.Name, d.Description, d.Name, sampleArgs(d))
		if d.Name == "timeline_search" {
			b.WriteString(timelinePeriodReadSpec + "\n")
		}
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
	// Each tool has its own branch, binding its name to its argument schema.
	branch := func(kind, name string, args map[string]any) map[string]any {
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"type": map[string]any{"type": "string", "enum": []string{kind}},
				"tool": map[string]any{"type": "string", "enum": []string{name}},
				"args": args,
			},
			"required": []string{"type", "tool", "args"},
		}
	}
	ids := map[string]any{"type": "array", "minItems": 1, "maxItems": maxEntryReads, "items": map[string]any{"type": "string", "minLength": 1}, "description": "本文を読む既知の投稿ID。期間や検索語は指定しません。"}
	choices := []any{branch("entries", "", map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"ids": ids}, "required": []string{"ids"},
	})}
	if c.records {
		choices = append(choices, branch("records", "", map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"tag": stringField("記憶のタグで絞る。不要なら省略"), "query": stringField("記憶を探す語句。不要なら省略"),
				"before": stringField("この月より前の記憶。例: 2026-09。不要なら省略"), "ids": ids,
			},
		}))
	}
	for _, d := range c.tools {
		args := d.Parameters
		if len(args) == 0 {
			args = map[string]any{"type": "object"}
		}
		prefix := fmt.Sprintf("#/properties/steps/items/anyOf/%d/properties/args", len(choices))
		choices = append(choices, branch("tool", d.Name, nestedReadSchema(args, prefix).(map[string]any)))
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

// Local schema references originally point to the tool schema's root. Rebase
// them to the nested args schema without modifying shared MCP declarations.
func nestedReadSchema(value any, prefix string) any {
	switch value := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(value))
		for key, child := range value {
			if ref, ok := child.(string); key == "$ref" && ok && (ref == "#" || strings.HasPrefix(ref, "#/")) {
				copy[key] = prefix + strings.TrimPrefix(ref, "#")
			} else {
				copy[key] = nestedReadSchema(child, prefix)
			}
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for i, child := range value {
			copy[i] = nestedReadSchema(child, prefix)
		}
		return copy
	default:
		return value
	}
}
