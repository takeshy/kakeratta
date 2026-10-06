package social

import (
	"regexp"
	"strings"
	"unicode"
)

// Link is a Bluesky link facet: the text in [Start, End) bytes of a post's
// text opens URI. Bluesky has no markup; links, mentions and tags are the
// rich text a post can carry.
type Link struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	URI   string `json:"uri"`
}

var (
	markdownHeading = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$`)
	markdownItem    = regexp.MustCompile(`^(\s*)(?:([-*+])|(\d+[.)]))\s+(.*)$`)
	markdownTask    = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	markdownRule    = regexp.MustCompile(`^\s*\|?\s*:?-{3,}`)
	markdownCallout = regexp.MustCompile(`^\[!([A-Za-z0-9_-]+)\][+-]?\s*(.*)$`)
	markdownBreak   = regexp.MustCompile(`^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
	// The inline forms the timeline renders: labelled links, emphasis,
	// deletion and code. A bare URL stays as it is and is linked later.
	markdownInline = regexp.MustCompile(`\[([^\]\n]+)\]\(([^\s)]+)\)|\*\*([^*\n]+)\*\*|__([^_\n]+)__|~~([^~\n]+)~~|` + "`([^`\\n]+)`")
)

// FromMarkdown turns a persona's Markdown answer into the plain text a
// Bluesky post can show, keeping what reads: heading and item lines, table
// rows, quotations, flowcharts as arrows, and labelled links as link facets.
// Markup that would only show as punctuation is dropped.
func FromMarkdown(source string) (string, []Link) {
	var out strings.Builder
	var links []Link
	blank := true // the output ends in a blank line (or is empty)
	line := func(text string) {
		out.WriteString(text)
		out.WriteString("\n")
		blank = false
	}
	gap := func() {
		if !blank {
			out.WriteString("\n")
			blank = true
		}
	}
	// inline writes text with its links resolved into facets.
	inline := func(prefix, text string) {
		out.WriteString(prefix)
		start := 0
		for _, match := range markdownInline.FindAllStringSubmatchIndex(text, -1) {
			out.WriteString(text[start:match[0]])
			group := func(n int) string {
				if match[2*n] < 0 {
					return ""
				}
				return text[match[2*n]:match[2*n+1]]
			}
			switch {
			case match[2] >= 0:
				label, uri := group(1), group(2)
				if safeURI(uri) {
					links = append(links, Link{Start: out.Len(), End: out.Len() + len(label), URI: uri})
				}
				out.WriteString(label)
			default:
				for n := 3; n <= 6; n++ {
					if value := group(n); value != "" {
						out.WriteString(value)
						break
					}
				}
			}
			start = match[1]
		}
		out.WriteString(text[start:])
		out.WriteString("\n")
		blank = false
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		text := lines[i]
		trimmed := strings.TrimSpace(text)
		switch {
		case trimmed == "":
			gap()
		case strings.HasPrefix(trimmed, "```"):
			language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
			var code []string
			for i+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```") {
				i++
				code = append(code, lines[i])
			}
			i++ // the closing fence
			if strings.HasPrefix(language, "mermaid") {
				if arrows := mermaidArrows(code); len(arrows) > 0 {
					for _, arrow := range arrows {
						line(arrow)
					}
				}
				continue
			}
			for _, row := range code {
				line(row)
			}
		case markdownHeading.MatchString(text):
			gap()
			inline("", markdownHeading.FindStringSubmatch(text)[1])
		case markdownItem.MatchString(text):
			match := markdownItem.FindStringSubmatch(text)
			indent := strings.Repeat("　", min(len(match[1])/2, 3))
			body := match[4]
			marker := "・"
			if match[3] != "" {
				marker = match[3] + " "
			}
			if task := markdownTask.FindStringSubmatch(body); task != nil {
				marker, body = "☐ ", task[2]
				if strings.TrimSpace(task[1]) != "" {
					marker = "☑ "
				}
			}
			inline(indent+marker, body)
		case strings.Contains(text, "|") && i+1 < len(lines) && markdownRule.MatchString(lines[i+1]):
			inline("", tableRow(text))
			i++
			for i+1 < len(lines) && strings.Contains(lines[i+1], "|") {
				i++
				inline("・", tableRow(lines[i]))
			}
		case strings.HasPrefix(trimmed, ">"):
			quote := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			if callout := markdownCallout.FindStringSubmatch(quote); callout != nil {
				if title := strings.TrimSpace(callout[2]); title != "" {
					inline("", title)
				}
				continue
			}
			inline("＞ ", quote)
		case markdownBreak.MatchString(text):
			gap()
		default:
			inline("", text)
		}
	}
	// Trimming the front moves every facet with it.
	result := out.String()
	lead := len(result) - len(strings.TrimLeftFunc(result, unicode.IsSpace))
	for i := range links {
		links[i].Start -= lead
		links[i].End -= lead
	}
	return strings.TrimSpace(result), links
}

func tableRow(row string) string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return strings.Join(cells, " ｜ ")
}

var (
	mermaidNode     = regexp.MustCompile(`^([\w.-]+)(?:\(\((.*?)\)\)|\[(.*?)\]|\((.*?)\)|\{(.*?)\})?$`)
	mermaidEdge     = regexp.MustCompile(`^(.+?)\s*(?:--\s*([^>-]+?)\s*)?(-->|---|-\.->|==>)\s*(.+?)\s*;?$`)
	mermaidActor    = regexp.MustCompile(`(?i)^(?:participant|actor)\s+(\S+)(?:\s+as\s+(.+))?$`)
	mermaidMessage  = regexp.MustCompile(`^(\S+?)\s*(-{1,2}>+|-->>|->>)\s*(\S+?)\s*:\s*(.+)$`)
	mermaidMarkup   = regexp.MustCompile(`<[^>]*>`)
	mermaidQuoted   = regexp.MustCompile(`^['"]|['"]$`)
	mermaidPipeEdge = regexp.MustCompile(`\s*(-->|---|-\.->|==>)\s*\|([^|]*)\|\s*`)
)

// mermaidArrows reads a flowchart or sequence diagram as "A → B" lines, the
// way the timeline's renderer reads them. Anything else yields nothing.
func mermaidArrows(code []string) []string {
	var lines []string
	for _, line := range code {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "%%") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	clean := func(value string) string {
		return strings.TrimSpace(mermaidQuoted.ReplaceAllString(mermaidMarkup.ReplaceAllString(strings.TrimSpace(value), ""), ""))
	}
	head := strings.ToLower(lines[0])
	var out []string
	switch {
	case strings.HasPrefix(head, "flowchart") || strings.HasPrefix(head, "graph"):
		labels := map[string]string{}
		name := func(raw string) string {
			match := mermaidNode.FindStringSubmatch(strings.TrimSpace(raw))
			if match == nil {
				return clean(raw)
			}
			for _, label := range match[2:] {
				if label != "" {
					labels[match[1]] = clean(label)
				}
			}
			if label, ok := labels[match[1]]; ok {
				return label
			}
			return match[1]
		}
		for _, line := range lines[1:] {
			// A pipe-labelled edge reads like a "-- label -->" one.
			line = mermaidPipeEdge.ReplaceAllString(line, " -- $2 $1 ")
			match := mermaidEdge.FindStringSubmatch(line)
			if match == nil {
				name(strings.TrimSuffix(line, ";"))
				continue
			}
			arrow := " → "
			if match[3] == "---" {
				arrow = " ― "
			}
			edge := name(match[1]) + arrow + name(match[4])
			if label := clean(match[2]); label != "" {
				edge += "（" + label + "）"
			}
			out = append(out, "・"+edge)
		}
	case strings.HasPrefix(head, "sequencediagram"):
		labels := map[string]string{}
		for _, line := range lines[1:] {
			if actor := mermaidActor.FindStringSubmatch(line); actor != nil {
				labels[actor[1]] = clean(actor[1])
				if actor[2] != "" {
					labels[actor[1]] = clean(actor[2])
				}
				continue
			}
			if message := mermaidMessage.FindStringSubmatch(line); message != nil {
				from, to := message[1], message[3]
				if label, ok := labels[from]; ok {
					from = label
				}
				if label, ok := labels[to]; ok {
					to = label
				}
				out = append(out, "・"+from+" → "+to+": "+clean(message[4]))
			}
		}
	}
	return out
}

func safeURI(uri string) bool {
	return strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://")
}
