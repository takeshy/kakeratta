package ingest

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/takeshy/dokking/internal/book"
)

// PDF carries no structure worth trusting: there are no chapters in the file,
// only marks on pages. It is therefore read as text - the same as every other
// format - and its pages are kept only as display markers, never as positions.
// Where the source has headings they become sections; where it has none the
// pages are grouped, and the result is honest about being coarse.
const pagesPerSection = 10

// ParsePDF extracts the text of a PDF and builds the same structure every other
// format produces.
func ParsePDF(path string) (*book.Book, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePDFBytes(raw)
}

func parsePDFBytes(raw []byte) (*book.Book, error) {
	file := newPDFFile(raw)
	pages := file.pages()
	if len(pages) == 0 {
		return nil, fmt.Errorf("ページを読み取れませんでした")
	}
	texts := make([]string, 0, len(pages))
	total := 0
	for _, page := range pages {
		text := file.pageText(page)
		texts = append(texts, text)
		total += len(strings.TrimSpace(text))
	}
	if total == 0 {
		// A scanned PDF is images: there is nothing to read, index or speak, and
		// pretending otherwise would produce a book of empty pages.
		return nil, fmt.Errorf("このPDFにはテキストが含まれていません（画像として保存されたPDFの可能性があります）")
	}

	headings := 0
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if _, ok := headingOf(line); ok {
				headings++
			}
		}
	}
	useHeadings := headings >= 2

	var b builder
	for index, text := range texts {
		pageLabel := fmt.Sprintf("p.%d", index+1)
		if !useHeadings && index%pagesPerSection == 0 {
			last := index + pagesPerSection
			if last > len(texts) {
				last = len(texts)
			}
			b.startSection(fmt.Sprintf("%d–%dページ", index+1, last), 1)
		}
		first := true
		for _, paragraph := range pdfParagraphs(text) {
			if useHeadings {
				if heading, ok := headingOf(paragraph); ok {
					b.startSection(heading, 1)
					b.addBlock(book.BlockHeading, heading, nil, pageNote(first, pageLabel), "")
					first = false
					continue
				}
			}
			b.addBlock(book.BlockParagraph, paragraph, nil, pageNote(first, pageLabel), "")
			first = false
		}
	}
	sections, end := b.finish()
	if len(sections) == 0 {
		return nil, fmt.Errorf("読み取れる本文がありません")
	}
	return &book.Book{
		Format:     book.FormatPDF,
		Title:      strings.TrimSpace(file.documentTitle()),
		Author:     strings.TrimSpace(file.documentAuthor()),
		Sections:   sections,
		End:        end,
		ContentEnd: contentEnd(sections, end),
	}, nil
}

// pageNote marks where a page began. It is a display annotation, so it is not
// part of the text and cannot move a position - but the reader of a PDF thinks
// in pages, and this is how they get them back.
func pageNote(first bool, label string) []book.Note {
	if !first {
		return nil
	}
	return []book.Note{{Offset: 0, Text: label}}
}

// pdfParagraphs joins the lines of a page back into paragraphs.
//
// A line break in a PDF is where the text was wrapped, not where a paragraph
// ended, so single breaks are joined. What does end a paragraph: the blank line
// the content interpreter emits for a large vertical gap, and the indent that
// Japanese typesetting starts a paragraph with.
func pdfParagraphs(text string) []string {
	var paragraphs []string
	var current strings.Builder
	flush := func() {
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
		current.Reset()
	}
	lines := strings.Split(text, "\n")
	full := fullLineLength(lines)
	previousLength := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			previousLength = 0
			continue
		}
		indented := strings.HasPrefix(line, "\u3000") || strings.HasPrefix(line, "   ")
		if indented {
			flush()
		}
		if current.Len() > 0 {
			previous := []rune(strings.TrimSpace(current.String()))
			last := previous[len(previous)-1]
			// A line that ends a sentence well short of the measure ended the
			// paragraph too; one that runs to the measure was merely wrapped.
			if strings.ContainsRune("。．.!！?？」』", last) && previousLength < full*85/100 {
				flush()
			} else if !isCJKRune(last) && !isCJKRune([]rune(trimmed)[0]) {
				// Japanese wraps mid-sentence without a space; Latin needs one.
				current.WriteString(" ")
			}
		}
		current.WriteString(trimmed)
		previousLength = len([]rune(trimmed))
	}
	flush()
	return splitLongParagraphs(paragraphs)
}

// fullLineLength is how long a line runs when it reaches the measure, taken as
// the longest line on the page. Pages of prose are set to one width, so this is
// stable enough to tell a wrapped line from one that ended.
func fullLineLength(lines []string) int {
	longest := 0
	for _, line := range lines {
		if length := len([]rune(strings.TrimSpace(line))); length > longest {
			longest = length
		}
	}
	if longest < 20 {
		longest = 20
	}
	return longest
}

// splitLongParagraphs keeps a block readable and speakable. A PDF whose layout
// hides every paragraph break would otherwise become one block the length of a
// chapter, which no reader wants read aloud in a single breath.
func splitLongParagraphs(paragraphs []string) []string {
	const limit = 900
	var out []string
	for _, paragraph := range paragraphs {
		runes := []rune(paragraph)
		for len(runes) > limit {
			cut := limit
			for i := limit; i > limit/2; i-- {
				if strings.ContainsRune("。．.!！?？」』", runes[i-1]) {
					cut = i
					break
				}
			}
			out = append(out, string(runes[:cut]))
			runes = runes[cut:]
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return out
}

// isCJKRune covers the scripts and punctuation that are set without spaces.
func isCJKRune(r rune) bool {
	if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
		return true
	}
	return strings.ContainsRune("、。，．・「」『』（）〔〕【】…ー〜　！？", r)
}

// pages walks the page tree so the reading order is the document's own.
func (f *pdfFile) pages() []pdfDict {
	var catalog pdfDict
	for number := range f.objects {
		if dict := f.dict(f.object(number)); dict != nil && dict.name("Type") == "Catalog" {
			catalog = dict
			break
		}
	}
	var pages []pdfDict
	seen := map[int]bool{}
	if catalog != nil {
		root := f.dict(catalog["Pages"])
		f.collectPages(root, pdfDict{}, &pages, seen, 0)
	}
	if len(pages) == 0 {
		// No usable page tree: fall back to object order, which is the order a
		// writer produced them in and is right far more often than not.
		for number := 0; number < len(f.objects)+1024; number++ {
			dict := f.dict(f.object(number))
			if dict != nil && dict.name("Type") == "Page" {
				pages = append(pages, dict)
			}
		}
	}
	return pages
}

func (f *pdfFile) collectPages(node pdfDict, inherited pdfDict, pages *[]pdfDict, seen map[int]bool, depth int) {
	if node == nil || depth > 32 {
		return
	}
	merged := pdfDict{}
	for key, value := range inherited {
		merged[key] = value
	}
	for _, key := range []pdfName{"Resources", "MediaBox", "Rotate"} {
		if value, ok := node[key]; ok {
			merged[key] = value
		}
	}
	if node.name("Type") == "Page" {
		page := pdfDict{}
		for key, value := range merged {
			page[key] = value
		}
		for key, value := range node {
			page[key] = value
		}
		*pages = append(*pages, page)
		return
	}
	for _, kid := range f.array(node["Kids"]) {
		if ref, ok := kid.(pdfRef); ok {
			if seen[ref.number] {
				continue
			}
			seen[ref.number] = true
		}
		f.collectPages(f.dict(kid), merged, pages, seen, depth+1)
	}
}

func (f *pdfFile) documentTitle() string  { return f.infoField("Title") }
func (f *pdfFile) documentAuthor() string { return f.infoField("Author") }

func (f *pdfFile) infoField(key pdfName) string {
	for number := range f.objects {
		dict := f.dict(f.object(number))
		if dict == nil || dict["Producer"] == nil && dict["Title"] == nil && dict["Author"] == nil {
			continue
		}
		if value, ok := f.resolve(dict[key]).(pdfString); ok {
			return pdfTextString(value)
		}
	}
	return ""
}

func pdfTextString(raw pdfString) string {
	if len(raw) >= 2 && raw[0] == 0xfe && raw[1] == 0xff {
		return utf16BE(raw[2:])
	}
	var out strings.Builder
	for _, b := range raw {
		if int(b) < len(winAnsi) && winAnsi[b] != 0 {
			out.WriteRune(winAnsi[b])
		}
	}
	return out.String()
}
