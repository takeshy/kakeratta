package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/takeshy/dokking/internal/book"
)

// FromFile parses a book from disk, choosing the parser by extension and, for
// text files, by what the text itself looks like.
func FromFile(path string) (*book.Book, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		parsed, err := ParseEPUB(path)
		if err != nil {
			return nil, err
		}
		return finalise(parsed, path, fileFingerprint(path))
	case ".pdf":
		parsed, err := ParsePDF(path)
		if err != nil {
			return nil, err
		}
		return finalise(parsed, path, fileFingerprint(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := decodeText(raw)
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parsed := FromText(title, "", text)
	return finalise(parsed, path, contentFingerprint(text))
}

// FromText parses pasted or embedded text, detecting Aozora Bunko notation.
func FromText(title, author, text string) *book.Book {
	if looksAozora(text) {
		return ParseAozora(title, author, text)
	}
	return ParseText(title, author, text)
}

// Finalise stamps identity onto a parsed book. The id is derived from the
// content, so the same public-domain file always ingests as the same book and a
// prepared result can be shared.
func Finalise(parsed *book.Book, source, content string) (*book.Book, error) {
	return finalise(parsed, source, contentFingerprint(content))
}

func finalise(parsed *book.Book, source, fingerprint string) (*book.Book, error) {
	if parsed == nil || len(parsed.Sections) == 0 {
		return nil, fmt.Errorf("読み取れる本文がありません")
	}
	parsed.ID = fingerprint
	parsed.Source = source
	parsed.AddedAt = time.Now()
	if parsed.Title == "" {
		parsed.Title = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	if parsed.Title == "" {
		parsed.Title = "無題"
	}
	parsed.Speakable = speakable(parsed)
	return parsed, nil
}

// speakable reports whether the text is clean enough to read out loud.
//
// A PDF with no ToUnicode CMap comes through as replacement characters: legible
// enough to find a passage by eye, nonsense to a speech synthesiser. Those
// books get the reading half of the app and not the listening half.
func speakable(parsed *book.Book) bool {
	broken, total := 0, 0
	for _, section := range parsed.Sections {
		for _, block := range section.Blocks {
			for _, r := range block.Plain {
				total++
				if r == '\uFFFD' {
					broken++
				}
			}
		}
	}
	if total == 0 {
		return false
	}
	return broken*50 < total
}

func looksAozora(text string) bool {
	head := text
	if len(head) > 8000 {
		head = head[:8000]
	}
	markers := 0
	for _, marker := range []string{"《", "［＃", "底本：", "｜"} {
		if strings.Contains(text, marker) {
			markers++
		}
	}
	return markers >= 2
}

func contentFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func fileFingerprint(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contentFingerprint(path)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// decodeText accepts UTF-8 and falls back to Shift_JIS, which is what the
// Aozora Bunko downloads are encoded in.
func decodeText(raw []byte) string {
	text := strings.TrimPrefix(string(raw), "\ufeff")
	if utf8.ValidString(text) {
		return text
	}
	return decodeShiftJIS(raw)
}
