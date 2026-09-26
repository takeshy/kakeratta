package persona

import (
	"regexp"
	"strings"
)

// Tags are the one thing in a note's front matter the app reads for the
// persona's sake as well as the person's: a second way to find a note when the
// index and the file name are not enough. "アルゼンチン" finds every wine from
// there without anyone having filed them under a heading first.

var tagsInline = regexp.MustCompile(`(?m)^tags:\s*\[(.*?)\]\s*(?:#.*)?$`)
var tagsBlock = regexp.MustCompile(`(?m)^tags:\s*(?:#.*)?$((?:\n[ \t]+-[^\n]*)+)`)

// noteTags are the tags in a note's front matter, as written, in order.
func noteTags(text string) []string {
	front, ok := frontMatter(text)
	if !ok {
		return nil
	}
	var raw []string
	if match := tagsInline.FindStringSubmatch(front); match != nil {
		raw = strings.Split(match[1], ",")
	} else if match := tagsBlock.FindStringSubmatch(front); match != nil {
		for _, line := range strings.Split(match[1], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "-") {
				raw = append(raw, strings.TrimPrefix(line, "-"))
			}
		}
	}
	var tags []string
	seen := map[string]bool{}
	for _, tag := range raw {
		tag = strings.Trim(strings.TrimSpace(tag), `"'`)
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		tags = append(tags, tag)
	}
	return tags
}

// NoteTags exposes the frontmatter tags to read-only integrations such as the
// MCP note listing for an external agent. The note body remains owned by the persona.
func NoteTags(text string) []string {
	return noteTags(text)
}

// hasTag says whether a note carries a tag, spelling aside.
func hasTag(tags []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, tag := range tags {
		if strings.ToLower(tag) == want {
			return true
		}
	}
	return false
}
