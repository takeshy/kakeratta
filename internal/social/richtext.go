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
	mermaidNode = regexp.MustCompile(`^([\p{L}\p{N}_.]+(?:-[\p{L}\p{N}_][\p{L}\p{N}_.]*)*)\s*(?:\(\(([\s\S]*?)\)\)|\[\[([\s\S]*?)\]\]|\[\(([\s\S]*?)\)\]|\(\[([\s\S]*?)\]\)|\[([\s\S]*?)\]|\(([\s\S]*?)\)|\{\{([\s\S]*?)\}\}|\{([\s\S]*?)\}|>([\s\S]*?)\])?\s*`)
	mermaidArrow   = regexp.MustCompile(`^(?:(--|==|-\.)\s+([^|\n]+?)\s+)?(-{2,}>|\.-+>|-\.+->|-{3,}|\.-+|-\.+-|={2,}>|={3,})\s*(?:\|([^|]*)\|)?\s*`)
	mermaidAnd     = regexp.MustCompile(`^&\s*`)
	mermaidIgnored = regexp.MustCompile(`(?i)^(?:subgraph\b|end$|direction\s|style\s|classDef\s|class\s|linkStyle\s|click\s)`)
	mermaidActor   = regexp.MustCompile(`(?i)^(?:participant|actor)\s+(.+?)(?:\s+as\s+(.+))?$`)
	mermaidMessage = regexp.MustCompile(`^(.+?)\s*(-->>|->>|-->|->|--x|-x|--\)|-\))\s*[+-]?\s*(.+?)\s*:\s*(.*)$`)
	mermaidNote    = regexp.MustCompile(`(?i)^note\s+(?:over|left of|right of)\s+[^:]+?\s*:\s*(.*)$`)
	mermaidControl = regexp.MustCompile(`(?i)^(?:loop|alt|else|opt|par|and|critical|break|end|rect|activate|deactivate|autonumber|box)\b`)
	mermaidBreak   = regexp.MustCompile(`(?i)<br\s*/?>`)
	mermaidMarkup  = regexp.MustCompile(`<[^>]*>`)
	mermaidQuoted  = regexp.MustCompile(`^['"]|['"]$`)
)

// mermaidLabel is a label on one line: quotes, markup and breaks go.
func mermaidLabel(value string) string {
	value = mermaidBreak.ReplaceAllString(strings.TrimSpace(value), " ")
	value = mermaidQuoted.ReplaceAllString(mermaidMarkup.ReplaceAllString(value, ""), "")
	return strings.Join(strings.Fields(value), " ")
}

// mermaidStatements joins a label that runs over several lines into its
// statement, as the timeline's renderer does.
func mermaidStatements(code []string) []string {
	var out []string
	pending, depth := "", 0
	for _, raw := range code {
		line := strings.TrimSpace(raw)
		if pending == "" && (line == "" || strings.HasPrefix(line, "%%")) {
			continue
		}
		if pending != "" {
			pending += "\n" + line
		} else {
			pending = line
		}
		for _, char := range line {
			switch char {
			case '[', '(', '{':
				depth++
			case ']', ')', '}':
				depth = max(0, depth-1)
			}
		}
		if depth > 0 && strings.Count(pending, "\n") < 5 {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimSpace(pending), ";"))
		pending, depth = "", 0
	}
	if pending != "" {
		out = append(out, pending)
	}
	return out
}

// mermaidArrows reads a flowchart or sequence diagram as "A → B" lines, the
// way the timeline's renderer reads them. Anything else yields nothing.
func mermaidArrows(code []string) []string {
	statements := mermaidStatements(code)
	if len(statements) == 0 {
		return nil
	}
	head := strings.ToLower(statements[0])
	switch {
	case strings.HasPrefix(head, "flowchart") || strings.HasPrefix(head, "graph"):
		return flowchartArrows(statements[1:])
	case strings.HasPrefix(head, "sequencediagram"):
		return sequenceArrows(statements[1:])
	}
	return nil
}

func flowchartArrows(statements []string) []string {
	labels := map[string]string{}
	// group reads one node, or several joined by "&", from the front of text.
	group := func(text string) ([]string, string, bool) {
		var ids []string
		for {
			match := mermaidNode.FindStringSubmatch(text)
			if match == nil {
				return nil, text, false
			}
			for _, given := range match[2:] {
				if given != "" {
					labels[match[1]] = mermaidLabel(given)
				}
			}
			ids = append(ids, match[1])
			text = text[len(match[0]):]
			and := mermaidAnd.FindString(text)
			if and == "" {
				return ids, text, true
			}
			text = text[len(and):]
		}
	}
	name := func(id string) string {
		if label := labels[id]; label != "" {
			return label
		}
		return id
	}
	type edge struct {
		from, to, label string
		directed        bool
	}
	var edges []edge
	for _, statement := range statements {
		if mermaidIgnored.MatchString(statement) {
			continue
		}
		from, text, ok := group(statement)
		if !ok {
			return nil
		}
		for text != "" {
			arrow := mermaidArrow.FindStringSubmatch(text)
			if arrow == nil {
				return nil
			}
			to, rest, ok := group(text[len(arrow[0]):])
			if !ok {
				return nil
			}
			label := arrow[2]
			if label == "" {
				label = arrow[4]
			}
			for _, a := range from {
				for _, b := range to {
					edges = append(edges, edge{a, b, mermaidLabel(label), strings.HasSuffix(arrow[3], ">")})
				}
			}
			from, text = to, rest
		}
	}
	// Labels may be given after an id is first used, so names come last.
	var out []string
	for _, e := range edges {
		arrow := " → "
		if !e.directed {
			arrow = " ― "
		}
		line := "・" + name(e.from) + arrow + name(e.to)
		if e.label != "" {
			line += "（" + e.label + "）"
		}
		out = append(out, line)
	}
	return out
}

func sequenceArrows(statements []string) []string {
	labels := map[string]string{}
	name := func(id string) string {
		if label, ok := labels[id]; ok {
			return label
		}
		return id
	}
	var out []string
	continues := false
	for _, line := range statements {
		if actor := mermaidActor.FindStringSubmatch(line); actor != nil {
			labels[actor[1]] = mermaidLabel(actor[1])
			if actor[2] != "" {
				labels[actor[1]] = mermaidLabel(actor[2])
			}
			continues = false
			continue
		}
		if note := mermaidNote.FindStringSubmatch(line); note != nil {
			out = append(out, "・📝 "+mermaidLabel(note[1]))
			continues = true
			continue
		}
		if message := mermaidMessage.FindStringSubmatch(line); message != nil {
			out = append(out, "・"+name(message[1])+" → "+name(message[3])+": "+mermaidLabel(message[4]))
			continues = true
			continue
		}
		if mermaidControl.MatchString(line) {
			continue
		}
		// A line that is none of these continues the message or note above.
		if continues && len(out) > 0 {
			out[len(out)-1] += " " + mermaidLabel(line)
		}
	}
	return out
}

func safeURI(uri string) bool {
	return strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://")
}
