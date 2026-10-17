package social

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"

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

// HTMLText keeps readable body text when an HTML answer is shared as text.
// Non-content elements are skipped, and block boundaries stay readable.
func HTMLText(source string) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var out strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head", "script", "style", "noscript", "template", "svg", "iframe", "object", "embed":
				return
			case "br", "p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "blockquote", "pre":
				out.WriteString("\n")
			case "td", "th":
				out.WriteString(" ")
			case "img":
				for _, a := range n.Attr {
					if a.Key == "alt" {
						out.WriteString(a.Val)
					}
				}
			}
		}
		if n.Type == html.TextNode {
			out.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && (strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://")) {
					out.WriteString(" (" + a.Val + ")")
				}
			}
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "blockquote", "pre":
				out.WriteString("\n")
			}
		}
	}
	visit(doc)
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
