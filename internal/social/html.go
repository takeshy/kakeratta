package social

import (
	"regexp"
	"strings"
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
