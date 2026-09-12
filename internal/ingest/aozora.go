package ingest

import (
	"strings"
	"unicode"

	"github.com/takeshy/dokking/internal/book"
)

// Aozora Bunko keeps two texts in one file: what is shown, and what is read.
// Ruby and editorial annotations belong to the first and must be kept out of
// the second, or a search for a word would match a reading note and a position
// would land inside markup. Display keeps them; Plain, which every offset is
// counted over, does not.
func ParseAozora(title, author, text string) *book.Book {
	lines := strings.Split(normaliseNewlines(text), "\n")
	headTitle, headAuthor, body := splitAozoraHeader(lines)
	if strings.TrimSpace(title) == "" {
		title = headTitle
	}
	if strings.TrimSpace(author) == "" {
		author = headAuthor
	}

	var b builder
	paragraph := []string{}
	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		joined := strings.Join(paragraph, "")
		paragraph = paragraph[:0]
		plain, ruby, notes := decodeAozoraLine(joined)
		b.addBlock(book.BlockParagraph, plain, ruby, notes, "")
	}
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		plainLine, _, _ := decodeAozoraLine(line)
		if heading, ok := headingOf(plainLine); ok {
			flush()
			b.startSection(heading, 1)
			plain, ruby, notes := decodeAozoraLine(strings.TrimSpace(line))
			b.addBlock(book.BlockHeading, plain, ruby, notes, "")
			continue
		}
		paragraph = append(paragraph, strings.TrimSpace(line))
	}
	flush()
	sections, end := b.finish()
	return &book.Book{
		Title:      strings.TrimSpace(title),
		Author:     strings.TrimSpace(author),
		Format:     book.FormatAozora,
		Sections:   sections,
		End:        end,
		ContentEnd: contentEnd(sections, end),
	}
}

// splitAozoraHeader separates the title/author block and the ruled block of
// notation notes from the body, and stops the body at the colophon.
func splitAozoraHeader(lines []string) (title, author string, body []string) {
	ruled := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return len([]rune(trimmed)) >= 10 && strings.Count(trimmed, "-") == len([]rune(trimmed))
	}
	head := []string{}
	index := 0
	for ; index < len(lines) && index < 12; index++ {
		if ruled(lines[index]) {
			break
		}
		if trimmed := strings.TrimSpace(lines[index]); trimmed != "" {
			head = append(head, trimmed)
		}
	}
	if index < len(lines) && ruled(lines[index]) {
		index++
		for ; index < len(lines); index++ {
			if ruled(lines[index]) {
				index++
				break
			}
		}
	} else {
		index = 0
		head = nil
	}
	if len(head) > 0 {
		title = head[0]
	}
	if len(head) > 1 {
		author = head[len(head)-1]
	}
	for ; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		// The colophon states the printed edition the file was made from: it is
		// not part of the work and must not count towards finishing it.
		if strings.HasPrefix(trimmed, "底本：") || strings.HasPrefix(trimmed, "底本:") {
			break
		}
		body = append(body, lines[index])
	}
	return title, author, body
}

// decodeAozoraLine returns the body text and, with offsets into it, the ruby
// readings and editorial annotations that belong to the display only.
func decodeAozoraLine(line string) (plain string, ruby []book.Ruby, notes []book.Note) {
	runes := []rune(line)
	var body []rune
	base := -1 // where the current ruby base started in body, after a ｜
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '｜':
			base = len(body)
		case '［':
			// ［＃...］ is an editorial instruction, not the work's text.
			if end := findRune(runes, i, '］'); end > i && i+1 < len(runes) && runes[i+1] == '＃' {
				notes = append(notes, book.Note{Offset: len(body), Text: string(runes[i : end+1])})
				i = end
				continue
			}
			body = append(body, runes[i])
		case '《':
			end := findRune(runes, i, '》')
			if end <= i {
				body = append(body, runes[i])
				continue
			}
			reading := string(runes[i+1 : end])
			start := base
			if start < 0 {
				start = rubyBaseStart(body)
			}
			if start >= 0 && start < len(body) {
				ruby = append(ruby, book.Ruby{
					Offset: start,
					Length: len(body) - start,
					Text:   reading,
				})
			}
			base = -1
			i = end
		default:
			body = append(body, runes[i])
		}
	}
	return string(body), ruby, notes
}

func findRune(runes []rune, from int, target rune) int {
	for i := from + 1; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// rubyBaseStart finds the run the reading applies to when no ｜ marked it: the
// trailing kanji, or the trailing katakana when there is none.
func rubyBaseStart(body []rune) int {
	if len(body) == 0 {
		return -1
	}
	class := func(r rune) int {
		switch {
		case unicode.In(r, unicode.Han), r == '々', r == '〆':
			return 1
		case unicode.In(r, unicode.Katakana):
			return 2
		default:
			return 0
		}
	}
	kind := class(body[len(body)-1])
	if kind == 0 {
		return -1
	}
	start := len(body)
	for start > 0 && class(body[start-1]) == kind {
		start--
	}
	return start
}
