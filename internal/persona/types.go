package persona

import (
	"regexp"
	"strings"
)

// A persona's memory layout names its fields (分野): the few kinds of thing it
// keeps a summary of. Each named kind is one field and one body; see memory.go.

var typeWord = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]{1,29}\b`)

// notTypes are capitalised words a memory layout may use that are not kinds.
var notTypes = map[string]bool{"OKF": true, "YAML": true, "JSON": true, "ID": true, "URL": true, "Markdown": true, "Web": true, "AI": true}

// allowedTypes are the kinds a memory layout names, in the order named.
func allowedTypes(memory string) []string {
	var out []string
	seen := map[string]bool{}
	for _, word := range typeWord.FindAllString(memory, -1) {
		if notTypes[word] || seen[strings.ToLower(word)] {
			continue
		}
		seen[strings.ToLower(word)] = true
		out = append(out, word)
	}
	return out
}
