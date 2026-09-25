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
