package persona

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/takeshy/kakeratta/internal/store"
)

// One thing, one note. A live run kept the same wine under two paths - once
// with the label misread - and the persona went on writing to both. The app
// does not read what a note says, but it can see that two files carry the same
// title, and that is a filing matter, not a judgement about wine.

var titleLine = regexp.MustCompile(`(?m)^title:[ \t]*["']?([^"'\n]+?)["']?[ \t]*$`)

// noteTitle is the title in a note's front matter, or the empty string.
func noteTitle(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return ""
	}
	match := titleLine.FindStringSubmatch(text[:end+3])
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

// sameTitleKey reduces a title to what two spellings of one thing share:
// letters and digits, lower case, no spaces or punctuation.
func sameTitleKey(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nearTitles says whether two title keys are the same thing spelt slightly
// differently: equal, or within two edits of each other when long enough for
// two edits to be a slip rather than a different name.
func nearTitles(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if len([]rune(a)) < 6 || len([]rune(b)) < 6 {
		return false
	}
	return editDistance(a, b) <= 2
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current := make([]int, len(rb)+1)
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(rb)]
}

// duplicateNote says why a note about to be written at path, with this text,
// would file one thing twice: another note already carries the same title, or
// one within a slip of it. A note that links to the other is taken to mean the
// two are deliberately different, and passes. The empty string means no
// objection.
func duplicateNote(notes []store.Note, path, text string) string {
	title := noteTitle(text)
	if title == "" || reservedNote(path) {
		return ""
	}
	key := sameTitleKey(title)
	for _, other := range notes {
		if other.Path == path || reservedNote(other.Path) {
			continue
		}
		otherTitle := noteTitle(other.Text)
		if otherTitle == "" || !nearTitles(key, sameTitleKey(otherTitle)) {
			continue
		}
		if mentions(text, other.Path) {
			continue
		}
		if key == sameTitleKey(otherTitle) {
			return "同じ題名のメモがすでにあります: " + other.Path + "（" + otherTitle + "）。新しく作らず、その path を書き直してください"
		}
		return "よく似た題名のメモがすでにあります: " + other.Path + "（" + otherTitle + "）。同じものなら、その path を書き直してください。綴りの揺れは新しいメモの理由になりません。別のものなら、本文でそのメモにリンクして違いを書いてください"
	}
	return ""
}
