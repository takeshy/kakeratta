package persona

import (
	"encoding/json"
	"fmt"

	"github.com/takeshy/kakeratta/internal/tools"
)

// Sample arguments are how a tool is shown to a model: a call it can copy
// rather than a description it has to interpret. They are assembled for the
// tools a persona holds, in the read spec.

// toolSamples are argument objects for the built-in tools, written out the way
// a step carries them.
var toolSamples = map[string]string{
	"web_search":      `{"query":"神谷町 ランチ 営業時間"}`,
	"place_search":    `{"query":"ラーメン","radius":800}`,
	"timeline_search": `{"query":"尿酸値の薬","limit":5}`,
}

// sampleArgs is a tool's sample arguments: the built-in one, or one made from
// the required properties of the schema an MCP server gave.
func sampleArgs(declared tools.Declaration) string {
	if sample, ok := toolSamples[declared.Name]; ok {
		return sample
	}
	properties, _ := declared.Parameters["properties"].(map[string]any)
	required := map[string]bool{}
	if list, ok := declared.Parameters["required"].([]any); ok {
		for _, name := range list {
			if text, ok := name.(string); ok {
				required[text] = true
			}
		}
	}
	sample := map[string]any{}
	for name, raw := range properties {
		if len(required) > 0 && !required[name] {
			continue
		}
		property, _ := raw.(map[string]any)
		switch property["type"] {
		case "number", "integer":
			sample[name] = 1
		case "boolean":
			sample[name] = true
		case "array":
			sample[name] = []any{}
		case "object":
			sample[name] = map[string]any{}
		default:
			if enum, ok := property["enum"].([]any); ok && len(enum) > 0 {
				sample[name] = enum[0]
			} else {
				sample[name] = "…"
			}
		}
	}
	encoded, _ := json.Marshal(sample)
	return string(encoded)
}

// jsonString writes a JSON object as the string a step's args field holds.
func jsonString(object string) string {
	encoded, _ := json.Marshal(object)
	return string(encoded)
}

// searchLimitLine tells a persona with web_search how many searches it has,
// so it words the first query to count rather than finding out at the limit.
func searchLimitLine(set *tools.Set, deep bool) string {
	if !set.Allows("web_search") {
		return ""
	}
	return "\n   web_search はこのうち " + fmt.Sprint(turnSearchLimit(deep)) + " 回までです。同じ語句で検索し直さず、1回目の結果で足りるよう語句を選んでください。"
}
