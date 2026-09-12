package ingest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"

	"github.com/takeshy/dokking/internal/book"
)

// buildPDF assembles a file from object bodies. No cross-reference table is
// written, which also exercises the reader's refusal to depend on one.
func buildPDF(objects ...string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n")
	for index, body := range objects {
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, body)
	}
	out.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return out.Bytes()
}

func stream(dict, content string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(content), content)
}

func flateStream(dict, content string) string {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	_, _ = writer.Write([]byte(content))
	_ = writer.Close()
	return fmt.Sprintf("<< %s /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream",
		dict, compressed.Len(), compressed.String())
}

func TestPDFSimpleFontAndPageOrder(t *testing.T) {
	raw := buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 6 0 R >> >> /Contents 4 0 R >>",
		stream("", "BT /F1 12 Tf 14 TL 72 720 Td (Chapter One) Tj T* (the key was cold) Tj ET"),
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 6 0 R >> >> /Contents 7 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		flateStream("", "BT /F1 12 Tf 72 720 Td [(the lighthouse) -400 (waited)] TJ ET"),
	)
	parsed, err := parsePDFBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := plainOf(parsed)
	for _, want := range []string{"Chapter One", "the key was cold", "the lighthouse waited"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	// Page order comes from the page tree, not from the order of the objects.
	if strings.Index(text, "Chapter One") > strings.Index(text, "lighthouse") {
		t.Fatalf("pages came out in the wrong order:\n%s", text)
	}
	// Every page is marked for display, but the marker is never body text.
	if strings.Contains(text, "p.1") {
		t.Fatal("the page marker leaked into the indexed text")
	}
	found := false
	for _, section := range parsed.Sections {
		for _, block := range section.Blocks {
			for _, note := range block.Notes {
				if note.Text == "p.1" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("no page marker was recorded")
	}
}

func TestPDFIdentityFontUsesToUnicode(t *testing.T) {
	cmap := strings.Join([]string{
		"/CIDInit /ProcSet findresource begin",
		"12 dict begin begincmap",
		"1 begincodespacerange <0000> <FFFF> endcodespacerange",
		"2 beginbfchar",
		"<0001> <706F>",
		"<0002> <53F0>",
		"endbfchar",
		"1 beginbfrange",
		"<0010> <0012> <0041>",
		"endbfrange",
		"endcmap end end",
	}, "\n")
	raw := buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		stream("", "BT /F1 12 Tf 72 720 Td <00010002001000110012> Tj ET"),
		"<< /Type /Font /Subtype /Type0 /BaseFont /Any /Encoding /Identity-H /ToUnicode 6 0 R >>",
		stream("", cmap),
	)
	parsed, err := parsePDFBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The range maps three consecutive codes to three consecutive code points.
	if got := plainOf(parsed); !strings.Contains(got, "灯台ABC") {
		t.Fatalf("decoded %q", got)
	}
}

func TestPDFWithoutTextIsRefused(t *testing.T) {
	raw := buildPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		stream("", "q 300 0 0 200 72 500 cm /Im1 Do Q"),
	)
	if _, err := parsePDFBytes(raw); err == nil {
		t.Fatal("a scanned PDF should be refused, not imported as empty pages")
	} else if !strings.Contains(err.Error(), "テキスト") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestPDFJoinsWrappedJapaneseLines(t *testing.T) {
	// Lines set to a measure of 30 characters: the first two run the full width
	// and were merely wrapped, even though one of them ends a sentence.
	page := strings.Join([]string{
		"男は駅で鍵を拾った。それは古い真鍮の鍵で、持ち手に小さく",
		"「Ｌ」と刻まれていた。ホームの端に、背の高い男が立ってい",
		"た。",
		"\u3000次の段落は行の幅いっぱいまで文字が並んでいるので、",
		"折り返された行として扱われます。",
	}, "\n")
	paragraphs := pdfParagraphs(page)
	if len(paragraphs) != 2 {
		t.Fatalf("paragraphs = %#v", paragraphs)
	}
	if !strings.HasPrefix(paragraphs[0], "男は駅で鍵を拾った。それは古い真鍮の鍵で") {
		t.Fatalf("wrapped lines were not joined: %q", paragraphs[0])
	}
	if strings.Contains(paragraphs[0], " ") {
		t.Fatalf("a space was inserted into Japanese text: %q", paragraphs[0])
	}
	if !strings.HasSuffix(paragraphs[0], "立っていた。") {
		t.Fatalf("the short closing line was cut off: %q", paragraphs[0])
	}
	if paragraphs[1] != "次の段落は行の幅いっぱいまで文字が並んでいるので、折り返された行として扱われます。" {
		t.Fatalf("indent did not start a paragraph: %q", paragraphs[1])
	}
}

func TestPDFParagraphEndsAtAShortClosingLine(t *testing.T) {
	page := strings.Join([]string{
		"灯台の扉の前で、男は待っていた。コートの襟を立てたまま、",
		"何も言わずにこちらを見ている。",
		"結衣は鍵を握りしめた。手のひらが汗ばんでいるのが分かった",
		"が、離す気にはなれなかった。",
	}, "\n")
	paragraphs := pdfParagraphs(page)
	if len(paragraphs) != 2 {
		t.Fatalf("paragraphs = %#v", paragraphs)
	}
}

func TestPDFSplitsAParagraphThatWouldBeUnreadable(t *testing.T) {
	long := strings.Repeat("あいうえお。", 400)
	parts := splitLongParagraphs([]string{long})
	if len(parts) < 2 {
		t.Fatalf("a %d character block was left whole", len([]rune(long)))
	}
	for _, part := range parts {
		if length := len([]rune(part)); length > 900 {
			t.Fatalf("part is %d characters", length)
		}
		if !strings.HasSuffix(part, "。") {
			t.Fatalf("split mid-sentence: %q", part[len(part)-30:])
		}
	}
	if strings.Join(parts, "") != long {
		t.Fatal("text was lost in the split")
	}
}

// plainOf is the body text as the rest of the app would see it.
func plainOf(parsed *book.Book) string {
	var parts []string
	for _, section := range parsed.Sections {
		for _, block := range section.Blocks {
			parts = append(parts, block.Plain)
		}
	}
	return strings.Join(parts, "\n")
}
