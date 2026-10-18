package persona

import (
	"context"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// System wakes have no browser locale. Use the newest explicit language from
// user activity, never an AI reply or the language of an internal instruction.
func recordedLanguage(entries []timeline.Entry) string {
	var latest *timeline.Entry
	for i := range entries {
		e := &entries[i]
		if e.Deleted || e.Speaker != timeline.User || (e.Language != "en" && e.Language != "ja") {
			continue
		}
		if latest == nil || e.At.After(latest.At) || (e.At.Equal(latest.At) && e.ID > latest.ID) {
			latest = e
		}
	}
	if latest == nil {
		return ""
	}
	return latest.Language
}

func (r *Runner) backgroundLanguage(ctx context.Context) string {
	entries, err := r.store.SpeakerEntries(ctx, timeline.User)
	if err != nil {
		return ""
	}
	return recordedLanguage(entries)
}

func backgroundLanguageRule(language string) string {
	switch language {
	case "en":
		return "\nFor this background turn, write user-facing text in English (en). This takes precedence over the language of system instructions, historical AI replies and internal summaries. Do not translate proper names unnecessarily.\n"
	case "ja":
		return "\nFor this background turn, write user-facing text in Japanese (ja). This takes precedence over the language of system instructions, historical AI replies and internal summaries. Do not translate proper names unnecessarily.\n"
	}
	return ""
}
