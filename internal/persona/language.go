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

// factLanguageRule tells the memory tidy which language the facts it writes
// are read in: the user's own, as the memory view shows them. Without it a
// persona written in Japanese folded English records into Japanese facts.
func factLanguageRule(language string) string {
	switch language {
	case "en":
		return "\n事実（facts の text）は英語 (en) で書いてください。ユーザーは英語で投稿し、記憶も英語で読みます。担当の指示や既存の事実が日本語でも英語で書き、固有名詞は無理に訳さないでください。\n"
	case "ja":
		return "\n事実（facts の text）は日本語 (ja) で書いてください。ユーザーは日本語で投稿し、記憶も日本語で読みます。担当の指示や既存の事実が英語でも日本語で書き、固有名詞は無理に訳さないでください。\n"
	}
	return ""
}

// couldNotAnswer is what a persona called by name says when its turn failed,
// in the language the post was written in.
func couldNotAnswer(language string) string {
	if language == "en" {
		return "I could not answer this just now."
	}
	return "今はうまく答えられませんでした。"
}

// scheduleNoteLanguageRule tells the schedule phase which language a new
// schedule's note is read in: the schedule list shows it to the user.
func scheduleNoteLanguageRule(language string) string {
	switch language {
	case "en":
		return "\n予定の note は英語 (en) で書いてください。ユーザーは英語で投稿し、予定一覧も英語で読みます。固有名詞は無理に訳さないでください。\n"
	case "ja":
		return "\n予定の note は日本語 (ja) で書いてください。ユーザーは日本語で投稿し、予定一覧も日本語で読みます。固有名詞は無理に訳さないでください。\n"
	}
	return ""
}
