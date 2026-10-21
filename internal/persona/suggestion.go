package persona

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/takeshy/kakeratta/internal/timeline"
)

// A new persona suggested by the daily pass (memory_tidy.go) rides the
// check-in as two lines of its text, so a turn carried on later - an ask the
// external agent answers - still knows what it suggested. The check-in post
// carries the suggestion (Entry.Suggestion), and the buttons under it answer:
// "make it" has the server draft the persona and copy what of the suggesting
// persona's memory belongs to it (SeedMemory); "don't" only records the answer.

func (s personaSuggestion) line() string {
	return "\n\nSuggested new persona: " + s.Topic + "\nWhy: " + s.Reason
}

var suggestionLines = regexp.MustCompile(`(?m)^Suggested new persona: (.+)\nWhy: (.+)$`)

// suggestionOf is the new persona a check-in was asked to suggest, or nil.
func suggestionOf(trigger timeline.Entry, wakeInstructions string) *personaSuggestion {
	if !strings.Contains(wakeInstructions, personaSuggestInstructions) {
		return nil
	}
	match := suggestionLines.FindStringSubmatch(trigger.Text)
	if match == nil {
		return nil
	}
	return &personaSuggestion{Topic: strings.TrimSpace(match[1]), Reason: strings.TrimSpace(match[2])}
}

// fallback is what the post says when the persona said nothing: the buttons
// need a post to sit under.
func (s personaSuggestion) fallback(language string) string {
	if language == "en" {
		return fmt.Sprintf("Shall we set up a new persona for %s? (%s)", s.Topic, s.Reason)
	}
	return fmt.Sprintf("「%s」の担当を新しく作って分けませんか？（%s）", s.Topic, s.Reason)
}

// attachSuggestion marks the check-in post with what it suggested, so the
// buttons under it can answer.
func (r *Runner) attachSuggestion(remark *timeline.Entry, s personaSuggestion) {
	remark.Suggestion = &timeline.PersonaSuggestion{Topic: s.Topic, Reason: s.Reason, Status: timeline.SuggestionAsked}
	if err := r.store.UpdateEntry(*remark); err == nil {
		r.emit("entry", *remark)
	}
}

// ErrNoSuggestion is a post that is not waiting for an answer about a new
// persona.
var ErrNoSuggestion = fmt.Errorf("この投稿は新しい担当の提案を待っていません")

// SuggestionAsked is the post that suggested a new persona, still unanswered.
func (r *Runner) SuggestionAsked(entryID string) (timeline.Entry, error) {
	entry, ok := r.store.Entry(entryID)
	if !ok || entry.Deleted || entry.Suggestion == nil || entry.Suggestion.Status != timeline.SuggestionAsked {
		return entry, ErrNoSuggestion
	}
	return entry, nil
}

// AnswerSuggestion records the user's answer on the post: declined, or
// created with the persona made for it.
func (r *Runner) AnswerSuggestion(entryID, status, personaID string) (timeline.Entry, error) {
	entry, err := r.SuggestionAsked(entryID)
	if err != nil {
		return entry, err
	}
	if status != timeline.SuggestionCreated && status != timeline.SuggestionDeclined {
		return entry, fmt.Errorf("答えは created か declined です")
	}
	answered := *entry.Suggestion
	answered.Status, answered.Persona = status, personaID
	entry.Suggestion = &answered
	if err := r.store.UpdateEntry(entry); err != nil {
		return entry, err
	}
	return entry, nil
}

// LockSuggestion holds the post while its persona is made, here or in another
// process, so a second press does not make it twice.
func (r *Runner) LockSuggestion(ctx context.Context, entryID string) (context.Context, func(), error) {
	return r.store.Lock(ctx, "suggestion:"+entryID)
}

// SuggestionMemory is a persona's memory as the drafting of the suggested
// persona is shown it: facts and records, each with the id to pick it by.
func (r *Runner) SuggestionMemory(personaID string) string {
	_, settings := r.clientOf()
	zone := settings.Location()
	m := r.loadMemo(personaID, zone)
	return "事実:\n" + renderFacts(m, zone) + "\n\n記録:\n" + listRecords(m.Records, zone)
}

// SeedMemory copies the facts and records picked by id from one persona's
// memory into another's, word for word: nothing is written that the source did
// not hold, and the source keeps them. The new persona's daily pass then reads
// the records as new. It returns how many facts and records were copied.
func (r *Runner) SeedMemory(ctx context.Context, from, to string, factIDs, recordIDs []string) (int, int, error) {
	_, settings := r.clientOf()
	zone := settings.Location()
	source := r.loadMemo(from, zone)
	held, release, err := r.lockKeeper(ctx, to)
	if err != nil {
		return 0, 0, err
	}
	defer release()
	target := r.loadMemo(to, zone)
	facts, records := 0, 0
	for _, f := range source.Facts {
		if slices.Contains(factIDs, f.ID) && !slices.ContainsFunc(target.Facts, func(g fact) bool { return g.ID == f.ID }) {
			target.Facts = append(target.Facts, f)
			facts++
		}
	}
	for _, rec := range source.Records {
		if slices.Contains(recordIDs, rec.ID) && !slices.ContainsFunc(target.Records, func(g record) bool { return g.ID == rec.ID }) {
			rec.Asked = ""
			target.Records = append(target.Records, rec)
			records++
		}
	}
	if facts+records == 0 {
		return 0, 0, nil
	}
	return facts, records, r.saveMemo(held, to, target, zone)
}
