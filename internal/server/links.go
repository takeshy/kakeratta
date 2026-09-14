package server

import (
	"regexp"
	"strings"
)

// A URL in the middle of a sentence is still the page the post is about. Only
// ASCII is taken, because Japanese text often runs straight on after a URL.
var linkPattern = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)

// linkIn finds the first http/https URL in a post's text.
func linkIn(text string) string {
	link := linkPattern.FindString(text)
	for link != "" {
		last := link[len(link)-1]
		unbalanced := last == ')' && strings.Count(link, "(") < strings.Count(link, ")")
		if strings.IndexByte(".,;:!?'\"", last) < 0 && !unbalanced {
			break
		}
		link = link[:len(link)-1]
	}
	if strings.HasSuffix(link, "://") {
		return ""
	}
	return link
}
