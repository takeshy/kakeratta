package social

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

const continuation = "つづき:\n"

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

type textUnit struct {
	text      string
	graphemes int
}

// Keep URLs and extended graphemes intact. Sentence/newline boundaries take
// precedence over commas/spaces; hard wrapping is only for an oversized sentence.
func SplitText(text, prefix string) ([]string, error) {
	var units []textUnit
	appendGraphemes := func(text string) {
		g := uniseg.NewGraphemes(text)
		for g.Next() {
			units = append(units, textUnit{g.Str(), 1})
		}
	}
	offset := 0
	for _, match := range urlPattern.FindAllStringIndex(text, -1) {
		appendGraphemes(text[offset:match[0]])
		link := text[match[0]:match[1]]
		units = append(units, textUnit{link, uniseg.GraphemeClusterCount(link)})
		offset = match[1]
	}
	appendGraphemes(text[offset:])
	if len(units) == 0 {
		if uniseg.GraphemeClusterCount(prefix) > 300 || len(prefix) > 3000 {
			return nil, fmt.Errorf("署名が長すぎます")
		}
		return []string{strings.TrimSpace(prefix)}, nil
	}
	var parts []string
	for start := 0; start < len(units); {
		heading := prefix
		if len(parts) > 0 {
			heading = continuation + prefix
		}
		count, size := uniseg.GraphemeClusterCount(heading), len(heading)
		end, sentence, soft := start, start, start
		for end < len(units) && count+units[end].graphemes <= 300 && size+len(units[end].text) <= 3000 {
			unit := units[end].text
			count += units[end].graphemes
			size += len(unit)
			end++
			if strings.ContainsAny(unit, "\n。！？!?") || (unit == "." && (end == len(units) || isSpace(units[end].text))) {
				sentence = end
			} else if strings.ContainsAny(unit, "、，,;；") || isSpace(unit) {
				soft = end
			}
			// Keep closing quotation marks with the sentence they finish.
			if sentence == end-1 && strings.ContainsAny(unit, "」』）)]\"”’") {
				sentence = end
			}
		}
		if end == start {
			return nil, fmt.Errorf("URL・署名または1文字が長すぎて300文字以内に分割できません")
		}
		if end < len(units) {
			if sentence > start {
				end = sentence
			} else if soft > start {
				end = soft
			}
		}
		var body strings.Builder
		body.WriteString(heading)
		for _, unit := range units[start:end] {
			body.WriteString(unit.text)
		}
		parts = append(parts, body.String())
		start = end
	}
	return parts, nil
}
func isSpace(text string) bool { return strings.IndexFunc(text, unicode.IsSpace) >= 0 }
