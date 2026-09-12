package timeline

import (
	"strings"
	"testing"
	"time"
)

func at(minute int) time.Time {
	return time.Date(2026, 9, 12, 17, minute, 0, 0, time.UTC)
}

func TestMentionsFindTheOneBeingAsked(t *testing.T) {
	cases := map[string][]string{
		"@たべ これ美味しそう":        {"たべ"},
		"＠みち 20時に渋谷に着きたい":    {"みち"},
		"@tech @book どっちも":   {"tech", "book"},
		"メールは a@example.com": nil,
		"ただの一言":              nil,
	}
	for text, want := range cases {
		got := Mentions(text)
		if len(got) != len(want) {
			t.Errorf("Mentions(%q) = %v, want %v", text, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Mentions(%q) = %v, want %v", text, got, want)
			}
		}
	}
}

// A persona gets its post and what was said under it. Nothing from any other
// post comes with it: handing over the whole timeline would mean every persona
// saw everything again, which is what separate memories were for.
func TestThreadIsOnePostAndNothingElse(t *testing.T) {
	entries := []Entry{
		{ID: "p1", Kind: KindPost, Speaker: User, Text: "ラーメン", At: at(1)},
		{ID: "p2", Kind: KindPost, Speaker: User, Text: "技術記事", At: at(2)},
		{ID: "r1", Kind: KindRemark, Speaker: "gourmet", Root: "p1", Text: "うまそう", At: at(3)},
		{ID: "r2", Kind: KindRemark, Speaker: "tech", Root: "p2", Text: "古いAPI", At: at(4)},
		{ID: "u1", Kind: KindReply, Speaker: User, Root: "p1", ReplyTo: "r1", Text: "でしょ", At: at(5)},
	}
	thread := Thread(entries, "p1")
	if len(thread) != 3 {
		t.Fatalf("thread = %d entries, want the post and the two under it", len(thread))
	}
	for _, entry := range thread {
		if strings.Contains(entry.Text, "技術") || strings.Contains(entry.Text, "古いAPI") {
			t.Fatalf("another post leaked into the thread: %+v", entry)
		}
	}
}

func TestDeletedEntriesAreGone(t *testing.T) {
	entries := []Entry{
		{ID: "p1", Kind: KindPost, Speaker: User, Text: "残る", At: at(1)},
		{ID: "r1", Kind: KindRemark, Speaker: "tech", Root: "p1", Text: "消える", At: at(2), Deleted: true},
	}
	if len(Thread(entries, "p1")) != 1 {
		t.Fatal("a deleted entry came back")
	}
	if strings.Contains(Render(entries, nil), "消える") {
		t.Fatal("a deleted entry reached a prompt")
	}
}

// What the app read out of a capture is what every persona gets. It has to be
// in the rendering, and it has to be marked as not ready until it is.
func TestRenderCarriesWhatWasReadOutOfTheCapture(t *testing.T) {
	read := at(1)
	entry := Entry{
		ID: "p1", Kind: KindPost, Speaker: User, Text: "この一節", At: at(1),
		Media: &Media{Text: "本文の引用", Describe: "電子書籍の画面", Origin: "Kindle", ReadAt: &read},
	}
	rendered := entry.Render(nil)
	for _, want := range []string{"この一節", "本文の引用", "電子書籍の画面", "Kindle"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("%q missing from:\n%s", want, rendered)
		}
	}

	pending := Entry{ID: "p2", Kind: KindPost, Media: &Media{BlobID: "b"}}
	if !strings.Contains(pending.Render(nil), "まだ読み取っていません") {
		t.Fatal("an unread capture should say so rather than look empty")
	}
	if pending.Media.Ready() {
		t.Fatal("an unread capture is not ready")
	}
}

// A post with no position has to work: there is nothing meaningful to attach to
// one made from a desktop.
func TestAPostWithoutAPlaceStillRenders(t *testing.T) {
	entry := Entry{ID: "p1", Kind: KindPost, Speaker: User, Text: "お腹すいた", At: at(1)}
	if strings.Contains(entry.Render(nil), "北緯") {
		t.Fatal("a place appeared out of nowhere")
	}
	placed := entry
	placed.Place = &Place{Lat: 35.658, Lon: 139.7016, Label: "渋谷"}
	if !strings.Contains(placed.Render(nil), "渋谷") {
		t.Fatal("the label is what a model can use, and it is missing")
	}
}
