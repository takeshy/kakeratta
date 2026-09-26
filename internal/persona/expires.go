package persona

import (
	"regexp"
	"strings"
	"time"
)

// Two dates in a note's front matter are the app's business, and they differ
// in who acts.
//
// expires: the persona meant to be rid of the note by then - the lunch
// candidates it searched up, the route it worked out once. The app deletes
// the note when the time has passed and tells the persona, the way it does
// for a note the user removed. Assistant-made notes get a deadline so that a
// persona which must leave a note behind every remark does not fill its
// memory with its own answers. The user's reaction is what turns one into
// memory: the persona then rewrites the note without the line.
//
// review: the persona was not sure how long something the user said would
// matter - a shop they wanted to try, a topic they were into - and picked a
// day to find out. The app never deletes such a note. When the day has passed
// it wakes the persona, which asks the user and then keeps, extends, or
// forgets the note on their answer.
//
// Both dates move when the note is read while answering a post: see
// extendedDate below.
//
// A note with neither lives as long as the persona keeps it - the app never
// ages one out on its own.

func dateLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + key + `:\s*([^#\n]*?)\s*(?:#.*)?$`)
}

var expiresLine = dateLine("expires")
var reviewLine = dateLine("review")

// frontMatter is the YAML block a note starts with, "---" fences included, or
// nothing when the note has none.
func frontMatter(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return "", false
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return "", false
	}
	return text[:end+3], true
}

// noteExpires is when a note asked to be deleted; noteReview is when it asked
// to be brought up. A bare date means the start of that day where the user is;
// a full timestamp is taken as written. "never", a blank, or a date that does
// not parse mean there is no such date.
func noteExpires(text string, zone *time.Location) (time.Time, bool) {
	return noteDate(text, expiresLine, zone)
}

func noteReview(text string, zone *time.Location) (time.Time, bool) {
	return noteDate(text, reviewLine, zone)
}

func noteDate(text string, line *regexp.Regexp, zone *time.Location) (time.Time, bool) {
	front, ok := frontMatter(text)
	if !ok {
		return time.Time{}, false
	}
	match := line.FindStringSubmatch(front)
	if match == nil {
		return time.Time{}, false
	}
	raw := strings.Trim(strings.TrimSpace(match[1]), `"'`)
	if raw == "" || strings.EqualFold(raw, "never") {
		return time.Time{}, false
	}
	if zone == nil {
		zone = time.UTC
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
		if at, err := time.ParseInLocation(layout, raw, zone); err == nil {
			return at, true
		}
	}
	if at, err := time.ParseInLocation("2006-01-02", raw, zone); err == nil {
		return at, true
	}
	return time.Time{}, false
}

// NoteExpires exposes the deadline to the memory reader, so a note about to
// go can be shown as such.
func NoteExpires(text string, zone *time.Location) (time.Time, bool) {
	return noteExpires(text, zone)
}

// A note a persona reads while answering a post is still in use, so both of
// its dates move: the span the persona gave it - from when it was written to
// its expires or review date - starts again from now. A date still ahead moves
// too when that ends later; reading never brings a date closer. A next-day candidate
// read again today lives until tomorrow; a shop given a few weeks gets a few
// more weeks. Only reading for a post counts: a check-in reading the note it
// is about to ask about must not put off its own question.

// extendedDate moves one front-matter date so that its span, counted from
// written, starts again from now. It reports false when there is no date, the
// span is not positive, or the date would not move later. A bare date keeps
// being a bare date, counted in calendar days where the user is; a timestamp
// keeps its own layout.
func extendedDate(text string, line *regexp.Regexp, written, now time.Time, zone *time.Location) (string, bool) {
	front, ok := frontMatter(text)
	if !ok {
		return text, false
	}
	match := line.FindStringSubmatchIndex(front)
	if match == nil {
		return text, false
	}
	start, end := match[2], match[3]
	raw := strings.TrimSpace(front[start:end])
	quote := ""
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		quote, raw = raw[:1], raw[1:len(raw)-1]
	}
	if zone == nil {
		zone = time.UTC
	}
	var next string
	if day, err := time.ParseInLocation("2006-01-02", raw, zone); err == nil {
		from := written.In(zone)
		days := int(day.Sub(time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, zone)).Hours()/24 + 0.5)
		if days <= 0 {
			return text, false
		}
		today := now.In(zone)
		moved := time.Date(today.Year(), today.Month(), today.Day()+days, 0, 0, 0, 0, zone)
		if !moved.After(day) {
			return text, false
		}
		next = moved.Format("2006-01-02")
	} else {
		found := false
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
			at, err := time.ParseInLocation(layout, raw, zone)
			if err != nil {
				continue
			}
			found = true
			span := at.Sub(written)
			moved := now.Add(span)
			if span <= 0 || !moved.After(at) {
				return text, false
			}
			next = moved.In(at.Location()).Format(layout)
			break
		}
		if !found {
			return text, false
		}
	}
	offset := strings.Index(text, front)
	return text[:offset+start] + quote + next + quote + text[offset+end:], true
}

// extendedDates moves both dates of a note read in use; see extendedDate.
func extendedDates(text string, written, now time.Time, zone *time.Location) (string, bool) {
	text, expires := extendedDate(text, expiresLine, written, now, zone)
	text, review := extendedDate(text, reviewLine, written, now, zone)
	return text, expires || review
}
