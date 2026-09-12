package ingest

import (
	"strings"
	"testing"

	"github.com/takeshy/dokking/internal/book"
)

func TestParseTextSplitsChaptersAndOffsets(t *testing.T) {
	parsed := ParseText("test", "author", "第一章 出会い\n\nあいう\nえお\n\n第二章 別れ\n\nかきく\n")
	if len(parsed.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(parsed.Sections))
	}
	if parsed.Sections[0].Title != "第一章 出会い" {
		t.Fatalf("title = %q", parsed.Sections[0].Title)
	}
	// Offsets are counted over plain text and run continuously across blocks.
	first := parsed.Sections[0]
	if first.Blocks[0].Range.Start.Absolute != 0 {
		t.Fatalf("first block starts at %d", first.Blocks[0].Range.Start.Absolute)
	}
	heading := len([]rune("第一章 出会い"))
	if got := first.Blocks[1].Range.Start.Absolute; got != heading {
		t.Fatalf("body starts at %d, want %d", got, heading)
	}
	if parsed.End.Absolute == 0 {
		t.Fatal("book end is zero")
	}
}

func TestAozoraRubyStaysOutOfPlainText(t *testing.T) {
	source := strings.Join([]string{
		"銀河の記録",
		"架空 作者",
		"-------------------------------------------------------",
		"【テキスト中に現れる記号について】",
		"-------------------------------------------------------",
		"",
		"第一章 出発",
		"",
		"　彼は｜銀河《ぎんが》を見上げた。［＃ここから２字下げ］",
		"",
		"底本：「どこかの本」",
		"　　　架空出版",
	}, "\n")
	parsed := ParseAozora("", "", source)
	if parsed.Title != "銀河の記録" || parsed.Author != "架空 作者" {
		t.Fatalf("title/author = %q / %q", parsed.Title, parsed.Author)
	}
	var body *book.Block
	for i := range parsed.Sections[0].Blocks {
		if parsed.Sections[0].Blocks[i].Kind == book.BlockParagraph {
			body = &parsed.Sections[0].Blocks[i]
		}
	}
	if body == nil {
		t.Fatal("no paragraph parsed")
	}
	if strings.Contains(body.Plain, "ぎんが") || strings.Contains(body.Plain, "＃") {
		t.Fatalf("plain text kept markup: %q", body.Plain)
	}
	if len(body.Notes) != 1 || body.Notes[0].Text != "［＃ここから２字下げ］" {
		t.Fatalf("annotation was lost instead of kept for display: %+v", body.Notes)
	}
	if len(body.Ruby) != 1 || body.Ruby[0].Text != "ぎんが" {
		t.Fatalf("ruby = %+v", body.Ruby)
	}
	base := []rune(body.Plain)[body.Ruby[0].Offset : body.Ruby[0].Offset+body.Ruby[0].Length]
	if string(base) != "銀河" {
		t.Fatalf("ruby base = %q", string(base))
	}
	// The colophon is not part of the work.
	if strings.Contains(body.Plain, "底本") {
		t.Fatal("colophon leaked into the body")
	}
}

func TestAozoraRubyWithoutMarkerUsesPrecedingKanji(t *testing.T) {
	plain, ruby, _ := decodeAozoraLine("山田太郎《やまだたろう》は笑った")
	if plain != "山田太郎は笑った" {
		t.Fatalf("plain = %q", plain)
	}
	if len(ruby) != 1 || ruby[0].Offset != 0 || ruby[0].Length != 4 {
		t.Fatalf("ruby = %+v", ruby)
	}
}

func TestContentEndExcludesTrailingMatter(t *testing.T) {
	parsed := ParseText("t", "", "第一章 本文\n\nあいうえお\n\nあとがき\n\n謝辞です\n")
	if len(parsed.Sections) != 2 {
		t.Fatalf("sections = %d", len(parsed.Sections))
	}
	if !parsed.ContentEnd.Before(parsed.End) {
		t.Fatalf("content end %v should precede book end %v", parsed.ContentEnd, parsed.End)
	}
	if parsed.ContentEnd.Absolute != parsed.Sections[1].Range.Start.Absolute {
		t.Fatalf("content end = %d, want %d", parsed.ContentEnd.Absolute, parsed.Sections[1].Range.Start.Absolute)
	}
}

func TestDecodeShiftJIS(t *testing.T) {
	// "本" and "あ" in Shift_JIS, around ASCII.
	raw := []byte{'a', 0x96, 0x7b, 0x82, 0xa0, 'b'}
	if got := decodeText(raw); got != "a本あb" {
		t.Fatalf("decoded %q", got)
	}
}

// Only read-aloud depends on the text layer's quality. A PDF whose characters
// could not be recovered is still perfectly readable by eye, so it is offered
// without the listening half rather than refused.
func TestABrokenTextLayerLosesReadAloudAndNothingElse(t *testing.T) {
	clean, err := Finalise(FromText("本", "", "第一章\n\nあいうえお\n"), "test", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !clean.Speakable {
		t.Fatal("clean text should be readable out loud")
	}
	broken, err := Finalise(FromText("本", "", "第一章\n\n����あ��\n"), "test", "y")
	if err != nil {
		t.Fatal(err)
	}
	if broken.Speakable {
		t.Fatal("a text layer of replacement characters must not be read aloud")
	}
	if len(broken.Sections) == 0 {
		t.Fatal("the book is still there to read by eye")
	}
}
