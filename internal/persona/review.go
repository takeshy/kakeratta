package persona

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A record carries up to two dates, set by the note keeper from what it is,
// and they differ in who acts.
//
// expires: the record is of a kakera that served its moment - "where for
// lunch today?", "how do I get to Shibuya?" - and is no use once the day is
// out. The app deletes the record when the day has come (expireRecords). The
// fields are not touched.
//
// review: the record is of something the user said whose lifetime nobody
// knows yet - a shop they wanted to try, a topic they were into. The app never
// deletes it. When the day has passed, the persona is handed it in its daily
// check-in and asks the user; the user's reply to that post decides whether
// it stays, moves, or goes.
//
// Both dates move when the persona reads the record while answering a post:
// see extendRecords.

const reviewInstructions = `

## Review（見直し時期が来た記録）
見直しの日が過ぎたかけらの記録があります。どれかは対象発言に入っています（古い順、最大3件）。本体は消しません。
そのうち1件を選び、ユーザーに短く一言だけ聞いてください（「そろそろ忘れていい？ 草加の店の話」程度）。複数を一度に聞きません。
メモ係は、返事のあとに、聞いた記録の review を1週間先の日付に書き直してください。書き直さないと毎日同じことを聞くことになります。
聞くことがない・まだ判断できないなら Answer の text を空にし、review だけ先送りしても構いません。
発言するならユーザーの記録やメモの言語に合わせてください。システム通知の言語には合わせないでください。
`

const reviewReplyInstructions = `

## Review への返事
あなたが「そろそろ忘れていい？」と聞いた投稿に、ユーザーが返事をしました。聞いた記録は下にあります。記録の書き直しは、返事のあとにメモ係が行います。
返事のとおりに扱います。残す（「まだ要る」「忘れないで」）なら review を外す。延ばす（「来月また」「もう少し」）なら review を先の日付にする。もういい（「忘れて」）なら記録を忘れる。
返事が曖昧なら一度だけ聞き返して構いません。一言で済ませ、確認したことを長く報告しないでください。
`

// reviewLimit is how many due records one wake is handed. Oldest first, so a
// backlog drains from the far end without the prompt growing with it.
const reviewLimit = 3

// reviewsDue are the persona's records whose review day has come, oldest
// first, at most reviewLimit. A record that also expires is left out: the
// sweep deletes it, and asking about it would be noise.
func (r *Runner) reviewsDue(personaID string, now time.Time, zone *time.Location) []record {
	var due []record
	for _, rec := range r.records(personaID, zone) {
		if rec.Review.set() && !rec.Expires.set() && !rec.Review.Day.After(now) {
			due = append(due, rec)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		if !due[i].Review.Day.Equal(due[j].Review.Day) {
			return due[i].Review.Day.Before(due[j].Review.Day)
		}
		return due[i].At.Before(due[j].At)
	})
	if len(due) > reviewLimit {
		due = due[:reviewLimit]
	}
	return due
}

// askedRecords are the records a persona's post asked about.
func (r *Runner) askedRecords(personaID, postID string, zone *time.Location) []record {
	if postID == "" {
		return nil
	}
	var out []record
	for _, rec := range r.records(personaID, zone) {
		if rec.Asked == postID {
			out = append(out, rec)
		}
	}
	return out
}

// reviewReply is what a persona is told when the user answers a post of its
// own that asked about records. Empty when it asked about none, so an answer
// to a check-in reads as usual.
func (r *Runner) reviewReply(skill Skill, post timeline.Entry, settings store.Settings) string {
	zone := settings.Location()
	asked := r.askedRecords(skill.ID, post.ID, zone)
	if len(asked) == 0 {
		return ""
	}
	return reviewReplyInstructions + "聞いた記録:\n" + listRecords(asked, zone) + "\n"
}

// handedRecords are the records the keeper may change the dates of, or
// forget, in this turn: the ones due for review in a check-in, or the ones a
// post of the persona's asked about when the user replies to it.
func (r *Runner) handedRecords(k keeperTurn) []record {
	switch {
	case strings.Contains(k.wake, reviewReplyInstructions):
		return r.askedRecords(k.skill.ID, k.trigger.ReplyTo, k.here)
	case strings.Contains(k.wake, reviewInstructions):
		return r.reviewsDue(k.skill.ID, time.Now(), k.here)
	}
	return nil
}

// expireRecords deletes the records whose expires day has come, for every
// active persona, and returns the earliest such day still to come, or the zero
// time, so the next sweep can be set for then. Nobody is woken: a record is a
// line of its own, and nothing else rests on it.
func (r *Runner) expireRecords(ctx context.Context, now time.Time, settings store.Settings) (next time.Time) {
	zone := settings.Location()
	for _, skill := range r.skills(ctx).Active(settings.Active) {
		var gone []string
		err := func() error {
			held, release, err := r.lockKeeper(ctx, skill.ID)
			if err != nil {
				return err
			}
			defer release()
			kakera := map[string]bool{}
			err = r.eachRecord(held, skill.ID, zone, func(rec *record) (bool, bool) {
				if !rec.Expires.set() {
					return true, false
				}
				if rec.Expires.Day.After(now) {
					if next.IsZero() || rec.Expires.Day.Before(next) {
						next = rec.Expires.Day
					}
					return true, false
				}
				gone = append(gone, rec.ID)
				kakera[r.kakeraOf(*rec)] = true
				return false, true
			})
			if err != nil {
				return err
			}
			// A kakera that served its moment leaves no fact resting on it alone.
			_, err = r.dropFactSourcesHeld(held, skill.ID, kakera, zone)
			return err
		}()
		if err != nil {
			r.emit("error", map[string]any{"where": "expire", "message": err.Error()})
		}
		if len(gone) > 0 {
			r.emit("expired", map[string]any{"persona": skill.ID, "records": gone})
		}
	}
	return next
}

// reviewList is how due records are shown in a check-in's trigger.
func reviewList(due []record, zone *time.Location) string {
	lines := make([]string, 0, len(due))
	for _, rec := range due {
		lines = append(lines, fmt.Sprintf("%s (review: %s)", rec.line(zone), rec.Review.Day.In(zone).Format("2006-01-02")))
	}
	return strings.Join(lines, "\n")
}
