package ingest

import (
	"regexp"
	"strings"

	"github.com/takeshy/dokking/internal/book"
)

// Headings in plain text are a convention, not a structure, so only the shapes
// that are unambiguous on their own line are recognised.
var headingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^#{1,6}\s+(.+)$`),
	regexp.MustCompile(`^\s*(第[0-9０-９一二三四五六七八九十百]+[章話部節篇編巻].*)$`),
	regexp.MustCompile(`^\s*((?i:chapter|part|book)\s+[0-9ivxlcIVXLC]+.*)$`),
	regexp.MustCompile(`^\s*([0-9]{1,3}[.、]\s*\S.{0,40})$`),
	regexp.MustCompile(`^\s*(プロローグ|エピローグ|序章|終章|序|跋|あとがき|後書き|解説|奥付)\s*$`),
}

func headingOf(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || len([]rune(trimmed)) > 60 {
		return "", false
	}
	for _, pattern := range headingPatterns {
		if match := pattern.FindStringSubmatch(trimmed); match != nil {
			return strings.TrimSpace(match[1]), true
		}
	}
	return "", false
}

// ParseText reads plain text: blank lines separate paragraphs, and a line that
// looks like a conventional heading starts a section.
func ParseText(title, author, text string) *book.Book {
	var b builder
	paragraph := []string{}
	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		joined := strings.Join(paragraph, "")
		paragraph = paragraph[:0]
		b.addBlock(book.BlockParagraph, joined, nil, nil, "")
	}
	for _, line := range strings.Split(normaliseNewlines(text), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if heading, ok := headingOf(line); ok {
			flush()
			b.startSection(heading, 1)
			b.addBlock(book.BlockHeading, heading, nil, nil, "")
			continue
		}
		paragraph = append(paragraph, strings.TrimSpace(line))
	}
	flush()
	sections, end := b.finish()
	return &book.Book{
		Title:      strings.TrimSpace(title),
		Author:     strings.TrimSpace(author),
		Format:     book.FormatText,
		Sections:   sections,
		End:        end,
		ContentEnd: contentEnd(sections, end),
	}
}

func normaliseNewlines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}
