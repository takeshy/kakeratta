// Package timeline is the one list everything lands in.
//
// A capture, the line the user wrote under it, a persona's remark, the user's
// answer to that remark - all of it is the same kind of thing in the same
// order. There is no per-persona thread and no inbox: a post nobody reacted to
// sits in the list exactly like one that started a conversation.
package timeline

import (
	"fmt"
	"strings"
	"time"
)

// Kind is what an entry is. Three is all there is.
type Kind string

const (
	// KindPost starts a thread: a user fragment or a persona daily check-in.
	// Speaker identifies its author.
	KindPost Kind = "post"
	// KindRemark is a persona saying something unprompted.
	KindRemark Kind = "remark"
	// KindReply is the user answering, which is itself something the personas
	// see next time.
	KindReply Kind = "reply"
)

// Speaker is who an entry came from. Anything that is not User or App is a
// persona id.
const User = "user"

// App is the speaker of what the app itself posts: the month's summary.
const App = "kakeratta"

// SummaryMonth marks the app's summary of a month.
const SummaryMonth = "month"

// Source is a place a remark got something from, so a claim can be checked.
type Source struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Place is where the user was. It is optional on purpose: a post from a desktop
// has no meaningful position, and the app has to work without one.
type Place struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Label string  `json:"label,omitempty"`
}

// Media is an image that came in with a post, and what the app made of it.
//
// The reading happens once, in the app, and the text is shared. Handing the
// image itself to every persona would mean paying for vision once per persona
// for the same picture; a persona that decides it needs the detail can ask for
// the original.
type Media struct {
	BlobID string `json:"blobId"`
	MIME   string `json:"mime"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`

	Text     string     `json:"text,omitempty"`     // OCR
	Describe string     `json:"describe,omitempty"` // what is in the picture
	Origin   string     `json:"origin,omitempty"`   // where it seems to come from
	Model    string     `json:"model,omitempty"`
	ReadAt   *time.Time `json:"readAt,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// Ready reports whether the app has finished looking at the image. Personas
// wait for it, because the text is the whole of what they get.
func (m *Media) Ready() bool {
	return m == nil || m.ReadAt != nil || m.Error != ""
}

// Entry is one line of the timeline.
type Entry struct {
	RequestHash string    `json:"requestHash,omitempty"`
	ID          string    `json:"id"`
	Kind        Kind      `json:"kind"`
	Speaker     string    `json:"speaker"`
	Text        string    `json:"text"`
	At          time.Time `json:"at"`

	// A post carries what came in with it.
	Media *Media `json:"media,omitempty"`
	Link  string `json:"link,omitempty"`
	Place *Place `json:"place,omitempty"`

	// Root is the post this belongs to; a post's root is itself.
	//
	// It is what a persona is given: the post it was routed, and what has been
	// said under it. Not the timeline - handing over the whole timeline would
	// mean every persona saw everything again, which is exactly what keeping
	// separate memories was for.
	Root string `json:"root,omitempty"`
	// ReplyTo is the entry being answered, for display.
	ReplyTo string   `json:"replyTo,omitempty"`
	Sources []Source `json:"sources,omitempty"`
	// Notes are the paths of the notes a persona wrote in the turn that
	// produced this remark. A remark on a post always leaves a note behind,
	// and this is how the remark leads to it.
	Notes []string `json:"notes,omitempty"`
	// Model is which model a persona answered on: "super", or "ultra" for a
	// reply that asked for a better or deeper answer. It is shown beside the
	// remark so the user can tell the two apart. "agent" marks a remark
	// whose workflow an external agent planned. Empty for the user's own
	// entries, and for a turn that failed - a remark saying it
	// could not answer was not answered on anything.
	Model string `json:"model,omitempty"`

	// Summary marks a post the app wrote to sum up a span: SummaryMonth. The
	// user can edit it like their own post.
	Summary string `json:"summary,omitempty"`

	// Deleted marks a tombstone in the log rather than removing history.
	Deleted bool `json:"deleted,omitempty"`

	// Firestore tracks pending embedding updates without exposing indexing
	// metadata in the API or the local append-only JSON log.
	SearchDirty bool `json:"-"`
}

// Mentions returns the persona ids and names called out in the text.
//
// Being named is the one thing that overrides a persona's own judgement about
// whether to speak: separate domains are what make it worth choosing who to
// ask, and an unanswered question would make the choice pointless.
func Mentions(text string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '、' || r == '。' || r == '　'
	}) {
		if !strings.HasPrefix(field, "@") && !strings.HasPrefix(field, "＠") {
			continue
		}
		name := strings.TrimLeft(field, "@＠")
		name = strings.TrimRight(name, "さん、。!?！？")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// Thread is one post and everything said under it, oldest first.
//
// This is the unit a persona is woken with. What was said under a post is
// shared because that is what makes the exchange work - a considered reply
// cannot pick up on a quick one it never saw - but nothing from other posts
// comes with it.
func Thread(entries []Entry, root string) []Entry {
	var out []Entry
	for _, entry := range entries {
		if entry.ID == root && entry.Deleted {
			return nil
		}
		if entry.Deleted {
			continue
		}
		if entry.ID == root || entry.Root == root {
			out = append(out, entry)
		}
	}
	return out
}

// RootOf returns the post an entry belongs to.
func RootOf(entry Entry) string {
	if entry.Kind == KindPost {
		return entry.ID
	}
	return entry.Root
}

// Render writes entries as the conversation a persona reads.
//
// Everything under the post goes in as it is: other personas' remarks and the
// user's answers included. Nothing is filtered for relevance, because by this
// point the routing has already decided this persona should see it.
func Render(entries []Entry, names map[string]string, in *time.Location) string {
	var lines []string
	for _, entry := range entries {
		if entry.Deleted {
			continue
		}
		lines = append(lines, entry.Render(names, in))
	}
	return strings.Join(lines, "\n\n")
}

// Render writes one entry for a prompt.
func (e Entry) Render(names map[string]string, in *time.Location) string {
	if in == nil {
		in = time.UTC
	}
	who := "あなた（ユーザー）"
	if e.Speaker != User {
		who = names[e.Speaker]
		if who == "" {
			who = e.Speaker
		}
	}
	head := fmt.Sprintf("[%s] %s %s", e.ID, e.At.In(in).Format("2006-01-02 15:04 MST"), who)
	if e.Place != nil {
		head += fmt.Sprintf("（%s）", e.Place.Describe())
	}
	if e.ReplyTo != "" {
		head += fmt.Sprintf("（%s への返事）", e.ReplyTo)
	}
	body := []string{head}
	if text := strings.TrimSpace(e.Text); text != "" {
		body = append(body, text)
	}
	if e.Link != "" {
		body = append(body, "リンク: "+e.Link)
	}
	if e.Media != nil {
		body = append(body, e.Media.Render())
	}
	for _, source := range e.Sources {
		body = append(body, "出典: "+source.Label+" "+source.URL)
	}
	return strings.Join(body, "\n")
}

// Render writes what the app read out of the image.
func (m *Media) Render() string {
	if m == nil {
		return ""
	}
	if m.BlobID == "" {
		// No picture: this is the page behind the post's link.
		if m.Error != "" {
			return "リンク先: 読み取れませんでした（" + m.Error + "）"
		}
		parts := []string{"リンク先:"}
		if m.Describe != "" {
			parts = append(parts, "  ページの題名: "+m.Describe)
		}
		if m.Origin != "" {
			parts = append(parts, "  URL: "+m.Origin)
		}
		if m.Text != "" {
			parts = append(parts, "  ページの文字:\n"+indent(m.Text))
		}
		return strings.Join(parts, "\n")
	}
	if m.Error != "" {
		return "画像: 読み取れませんでした（" + m.Error + "）"
	}
	if m.ReadAt == nil {
		return "画像: まだ読み取っていません"
	}
	parts := []string{"画像:"}
	if m.Describe != "" {
		parts = append(parts, "  写っているもの: "+m.Describe)
	}
	if m.Origin != "" {
		parts = append(parts, "  出典の手がかり: "+m.Origin)
	}
	if m.Text != "" {
		parts = append(parts, "  読み取った文字:\n"+indent(m.Text))
	}
	return strings.Join(parts, "\n")
}

func indent(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, "    "+line)
	}
	return strings.Join(lines, "\n")
}

// Describe writes a place for a prompt. A model cannot do anything with a
// decimal pair on its own, so the label is used when there is one.
func (p Place) Describe() string {
	if p.Label != "" {
		return p.Label
	}
	return fmt.Sprintf("北緯%.4f 東経%.4f", p.Lat, p.Lon)
}
