package persona

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A review is the app bringing a note back to the user's attention before
// the persona lets it go. The persona picked the day; the app only keeps it.
//
// Reviews ride inside the persona's daily check-in, which every active persona
// has, so that a memory nobody asked to keep does not sit unquestioned.

const reviewInstructions = `

## Review（見直し時期が来たメモ）
frontmatter の review に書いた日付が過ぎたメモがあります。どれかは対象発言に入っています（古い順、最大3件）。本体は消しません。
そのうち1件を選び、notes_read で読んでから、ユーザーに短く一言だけ聞いてください（「そろそろ忘れていい？ 草加の店の話」程度）。複数を一度に聞きません。
同じターンの Note フェーズで、聞いたメモの review を1週間先の日付に書き直してください。書き直さないと毎日同じことを聞くことになります。
聞くことがない・まだ判断できないなら Answer の text を空にし、review だけ先送りしても構いません。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

const reviewReplyInstructions = `

## Review への返事
あなたが「そろそろ忘れていい？」と聞いた投稿に、ユーザーが返事をしました。対象のメモの path は下にあります。書き直すなら先に notes_read で読んでください。
返事のとおりにメモを扱ってください。残す（「まだ要る」「忘れないで」）なら frontmatter から review を外して書き直す（Note フェーズの write）。延ばす（「来月また」「もう少し」）なら review を先の日付にして書き直す。もういい（「忘れて」）なら捨てる（Note フェーズの forget）。
返事が曖昧なら一度だけ聞き返して構いません。一言で済ませ、確認したことを長く報告しないでください。
`

// reviewLimit is how many due notes one wake is handed. Oldest first, so a
// backlog drains from the far end without the prompt growing with it.
const reviewLimit = 3

// dueNote is a note whose review date has passed.
type dueNote struct {
	Path string
	At   time.Time
}

// reviewsDue lists the persona's notes whose review date has passed, oldest
// first, at most reviewLimit of them. A note that also carries expires is
// left out: the sweep deletes it, and asking about it would be noise.
func (r *Runner) reviewsDue(skill Skill, now time.Time, zone *time.Location) []dueNote {
	var due []dueNote
	after := ""
	for {
		page, err := r.store.MemoryPage(skill.ID, after, expirePage, func(note store.Note) bool {
			if note.Path == "index.md" {
				return false
			}
			if _, expires := noteExpires(note.Text, zone); expires {
				return false
			}
			at, ok := noteReview(note.Text, zone)
			return ok && !at.After(now)
		})
		if err != nil {
			r.emit("error", map[string]any{"where": "review", "message": err.Error()})
			break
		}
		for _, note := range page.Notes {
			at, _ := noteReview(note.Text, zone)
			due = append(due, dueNote{Path: note.Path, At: at})
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].At.Equal(due[j].At) {
			return due[i].At.Before(due[j].At)
		}
		return due[i].Path < due[j].Path
	})
	if len(due) > reviewLimit {
		due = due[:reviewLimit]
	}
	return due
}

// reviewList is how due notes are shown to the persona: one per line, with
// the day it chose.
func reviewList(due []dueNote, zone *time.Location) string {
	lines := make([]string, 0, len(due))
	for _, note := range due {
		lines = append(lines, fmt.Sprintf("%s (review: %s)", note.Path, note.At.In(zone).Format("2006-01-02")))
	}
	return strings.Join(lines, "\n")
}

// askedNotes are the notes a persona's own post rested on that still carry a
// review date: the ones it asked about, found without reading the remark.
func (r *Runner) askedNotes(skill Skill, post timeline.Entry, zone *time.Location) []string {
	var paths []string
	for _, path := range post.Notes {
		note, ok := r.store.Note(skill.ID, path)
		if !ok {
			continue
		}
		if _, has := noteReview(note.Text, zone); has {
			paths = append(paths, path)
		}
	}
	return paths
}

// reviewReply is what a persona is told when the user answers a post of its
// own: which notes it had asked about, and which others are due. Empty when
// there is nothing under review, so an answer to a check-in reads as usual.
func (r *Runner) reviewReply(skill Skill, post timeline.Entry, settings store.Settings) string {
	zone := settings.Location()
	asked := r.askedNotes(skill, post, zone)
	others := r.reviewsDue(skill, time.Now(), zone)
	if len(asked) == 0 && len(others) == 0 {
		return ""
	}
	text := reviewReplyInstructions
	if len(asked) > 0 {
		text += "聞いたメモ: " + strings.Join(asked, ", ") + "\n"
	}
	if len(others) > 0 {
		text += "ほかに見直し時期が来ているメモ:\n" + reviewList(others, zone) + "\n"
	}
	return text
}
