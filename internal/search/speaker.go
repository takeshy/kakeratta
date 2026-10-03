package search

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrSpeaker is a speaker that names nobody.
var ErrSpeaker = errors.New("発言者を指定してください")

// BySpeaker lists what one speaker said, newest first, a page at a time: the
// user, or a persona by its id. It is the list a name on the timeline opens.
// The hits are those a search returns, so the same list draws them.
func (s *Service) BySpeaker(ctx context.Context, speaker string, limit, offset int, filters ...func(string, time.Time) bool) (Result, error) {
	speaker = strings.TrimSpace(speaker)
	if speaker == "" || len(speaker) > 200 {
		return Result{}, ErrSpeaker
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	entries, err := s.workspace.SpeakerEntries(ctx, speaker)
	if err != nil {
		return Result{}, err
	}
	if len(filters) > 0 {
		kept := entries[:0]
		for _, e := range entries {
			if filters[0](e.ID, e.At) {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	result := Result{Hits: []Hit{}, Total: len(entries)}
	if offset >= len(entries) {
		return result, nil
	}
	for _, entry := range entries[offset:min(len(entries), offset+limit)] {
		text := Text(entry)
		result.Hits = append(result.Hits, Hit{Entry: entry, Snippet: excerpt(text, ""), Passage: text})
	}
	return result, nil
}
