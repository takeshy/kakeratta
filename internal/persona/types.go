package persona

import (
	"regexp"
	"strings"
)

// A persona's memory layout names the few kinds of note it keeps. The app does
// not judge what belongs in which; it only sees that a note carries a kind the
// layout never named - WineInfo beside Wine, Explanation beside anything - and
// that is how one thing ends up filed twice. So a note's kind has to be one of
// the named ones, when any are named.

var typeWord = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_-]{1,29}\b`)
var typeField = regexp.MustCompile(`(?m)^type:\s*["']?([^"'\n#]+?)["']?\s*(?:#.*)?$`)

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

// noteType is the kind in a note's front matter, or the empty string.
// NoteType exposes a note's frontmatter type to the memory reader, which can
// narrow the notes to one kind.
func NoteType(text string) string {
	return noteType(text)
}

func noteType(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return ""
	}
	match := typeField.FindStringSubmatch(text[:end+3])
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

// typeNotAllowed says why a note at path, with this text, carries a kind the
// persona's memory layout does not name. The empty string means no objection:
// no layout, no kinds named in it, no kind on the note, or a kind that is named.
func typeNotAllowed(memory, path, text string) string {
	if reservedNote(path) {
		return ""
	}
	allowed := allowedTypes(memory)
	if len(allowed) == 0 {
		return ""
	}
	kind := noteType(text)
	if kind == "" {
		return ""
	}
	for _, name := range allowed {
		if strings.EqualFold(name, kind) {
			return ""
		}
	}
	return "型 " + kind + " はあなたのメモの分類にありません（" + strings.Join(allowed, " / ") + "）。分類にある型で書くか、同じ対象のメモがあればそれを書き直してください"
}
