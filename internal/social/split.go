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
	link      string // the URI a link unit opens
}

// Part is one Bluesky post's share of a split text, with its link facets.
type Part struct {
	Text  string
	Links []Link
}

// SplitText splits plain text; see SplitRich.
func SplitText(text, prefix string) ([]string, error) {
	parts, err := SplitRich(text, prefix, nil)
	var out []string
	for _, part := range parts {
		out = append(out, part.Text)
	}
	return out, err
}

// SplitRich keeps link text, URLs and extended graphemes intact.
// Sentence/newline boundaries take precedence over commas/spaces; hard
// wrapping is only for an oversized sentence. links are facets over text;
// bare URLs outside them become links too. Each part's links are offsets
// into that part's text, prefix included.
func SplitRich(text, prefix string, links []Link) ([]Part, error) {
	var units []textUnit
	appendGraphemes := func(text string) {
		g := uniseg.NewGraphemes(text)
		for g.Next() {
			units = append(units, textUnit{text: g.Str(), graphemes: 1})
		}
	}
	appendPlain := func(text string) {
		offset := 0
		for _, match := range urlPattern.FindAllStringIndex(text, -1) {
			appendGraphemes(text[offset:match[0]])
			link := text[match[0]:match[1]]
			units = append(units, textUnit{link, uniseg.GraphemeClusterCount(link), link})
			offset = match[1]
		}
		appendGraphemes(text[offset:])
	}
	offset := 0
	for _, link := range links {
		// A facet that is out of order, overlapping or not on the text is
		// left as plain text rather than misplaced.
		if link.Start < offset || link.End <= link.Start || link.End > len(text) {
			continue
		}
		appendPlain(text[offset:link.Start])
		label := text[link.Start:link.End]
		units = append(units, textUnit{label, uniseg.GraphemeClusterCount(label), link.URI})
		offset = link.End
	}
	appendPlain(text[offset:])
	if len(units) == 0 {
		if uniseg.GraphemeClusterCount(prefix) > 300 || len(prefix) > 3000 {
			return nil, fmt.Errorf("署名が長すぎます")
		}
		return []Part{{Text: strings.TrimSpace(prefix)}}, nil
	}
	var parts []Part
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
			if units[end-1].link != "" {
				continue
			}
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
		var partLinks []Link
		for _, unit := range units[start:end] {
			if unit.link != "" {
				partLinks = append(partLinks, Link{Start: body.Len(), End: body.Len() + len(unit.text), URI: unit.link})
			}
			body.WriteString(unit.text)
		}
		parts = append(parts, Part{Text: body.String(), Links: partLinks})
		start = end
	}
	return parts, nil
}
func isSpace(text string) bool { return strings.IndexFunc(text, unicode.IsSpace) >= 0 }
