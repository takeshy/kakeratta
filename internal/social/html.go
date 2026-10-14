package social

import (
	"regexp"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// HTMLImages are temporary browser-rendered pages, never timeline attachments.
type HTMLImages struct {
	SourceHash string   `json:"sourceHash"`
	Images     []string `json:"images"`
	Alt        string   `json:"alt"`
}

var htmlFence = regexp.MustCompile("(?is)^```(?:html)?\\s*\\n(.*?)\\n```$")
var htmlStart = regexp.MustCompile(`(?is)^(?:<!doctype\s+html\b[^>]*>\s*)?<html\b`)
var htmlEnd = regexp.MustCompile(`(?is)</html>\s*$`)

// Match the browser's complete-document rule; fragments and code examples
// remain text. Only persona documents are converted by the publication plan.
func HTMLDocument(text string) string {
	source := strings.TrimSpace(text)
	if match := htmlFence.FindStringSubmatch(source); match != nil {
		source = strings.TrimSpace(match[1])
	}
	if htmlStart.MatchString(source) && htmlEnd.MatchString(source) {
		return source
	}
	return ""
}

var markdownHeadingLine = regexp.MustCompile(`^#{1,6}\s+\S`)

// InfographicMarkdown matches the browser's rule for laying the infographic
// persona's Markdown out as an infographic: a heading outside code fences.
func InfographicMarkdown(text string) bool {
	fenced := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if !fenced && markdownHeadingLine.MatchString(line) {
			return true
		}
	}
	return false
}

// ImageAnswer is a persona answer that is published as browser-rendered
// pages: a complete HTML document, or the infographic persona's or the
// external agent's Markdown laid out as an infographic, whose diagrams do not survive as text.
func ImageAnswer(e timeline.Entry) bool {
	if e.Speaker == timeline.User {
		return false
	}
	return HTMLDocument(e.Text) != "" || ((e.Speaker == "infographic" || e.Speaker == timeline.Agent) && InfographicMarkdown(e.Text))
}
