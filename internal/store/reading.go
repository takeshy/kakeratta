package store

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/takeshy/dokking/internal/position"
)

// PersonaNote is something a persona wrote down for itself, with the place in
// the book it was reading when it did.
//
// The position is what lets it say "doesn't that contradict what he said on
// page 42?" two hundred pages later: its notes are its own reading log, the
// way the timeline is the reader's.
type PersonaNote struct {
	Text     string            `json:"text"`
	Position position.Position `json:"position"`
	At       time.Time         `json:"at"`
}

// PersonaState is one persona's memory of one book.
//
// A persona is not a running process: its state sits here, is read when an
// event wakes it, and is written back when it is done. Nothing about it
// survives in memory between events, so there is one copy of the truth.
//
// Notes are never shared between personas. A technical persona has seen the
// whole book, and its notes reaching a novel persona would take the boundary
// apart from the inside.
type PersonaState struct {
	BookID        string            `json:"bookId"`
	PersonaID     string            `json:"personaId"`
	Notes         []PersonaNote     `json:"notes"`
	LastProcessed position.Position `json:"lastProcessed"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

// PersonaState loads a persona's memory, or an empty one if it has none yet.
func (s *Store) PersonaState(bookID, personaID string) PersonaState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := PersonaState{BookID: bookID, PersonaID: personaID}
	_ = readJSON(s.personaPath(bookID, personaID), &state)
	state.BookID, state.PersonaID = bookID, personaID
	return state
}

// SavePersonaState writes it back. Notes and messages are kept even when the
// reader takes a persona out of a book: adding it again should bring back the
// one that was reading along, not a stranger starting from page one.
func (s *Store) SavePersonaState(state PersonaState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.UpdatedAt = time.Now()
	return writeJSON(s.personaPath(state.BookID, state.PersonaID), state)
}

func (s *Store) personaPath(bookID, personaID string) string {
	return filepath.Join(s.bookDir(bookID), "personas", sanitise(personaID)+".json")
}

// SourceFile is where the original file is kept for the books that need it.
//
// A technical book is handed to the model as the file itself, so the file has
// to still be there after the reader moves it or deletes it from wherever they
// downloaded it.
func (s *Store) SourceFile(bookID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches, _ := filepath.Glob(filepath.Join(s.bookDir(bookID), "source.*"))
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

func (s *Store) SaveSourceFile(bookID, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	extension := filepath.Ext(path)
	if extension == "" {
		extension = ".bin"
	}
	target := filepath.Join(s.bookDir(bookID), "source"+extension)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o644)
}

// Day is one square of the calendar: what was read, and what the reader said,
// on one date.
type Day struct {
	Date   string    `json:"date"`
	Chars  int       `json:"chars"`
	Books  []DayBook `json:"books"`
	Events []Event   `json:"events"`
}

// DayBook is one book's share of a day.
type DayBook struct {
	BookID string `json:"bookId"`
	Title  string `json:"title"`
	Chars  int    `json:"chars"`
}

// Calendar folds the reading log into days.
//
// It holds no data of its own. The read entries already say which day each
// stretch was read on, so a day the reader read without writing anything down
// is still a day with something in it.
func (s *Store) Calendar() ([]Day, error) {
	shelf, err := s.Shelf()
	if err != nil {
		return nil, err
	}
	days := map[string]*Day{}
	dayOf := func(date string) *Day {
		day, ok := days[date]
		if !ok {
			day = &Day{Date: date}
			days[date] = day
		}
		return day
	}
	for _, progress := range shelf {
		for date, chars := range progress.State.Days() {
			day := dayOf(date)
			day.Chars += chars
			day.Books = append(day.Books, DayBook{
				BookID: progress.Book.ID, Title: progress.Book.Title, Chars: chars,
			})
		}
		events, _ := s.Timeline(progress.Book.ID)
		for _, event := range events {
			if event.At.IsZero() {
				continue
			}
			day := dayOf(event.At.Format("2006-01-02"))
			day.Events = append(day.Events, event)
		}
	}
	out := make([]Day, 0, len(days))
	for _, day := range days {
		sort.Slice(day.Books, func(i, j int) bool { return day.Books[i].Chars > day.Books[j].Chars })
		sort.Slice(day.Events, func(i, j int) bool { return day.Events[i].At.Before(day.Events[j].At) })
		out = append(out, *day)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}

// TimelineEntry is one of the reader's own entries, with the book it belongs
// to, so the timeline can run across every book at once.
type TimelineEntry struct {
	Event Event  `json:"event"`
	Title string `json:"title"`
}

// AllTimeline is every entry the reader made, newest first, across the shelf.
//
// Only their own words are here. An answer can be had again by reading to the
// same place and asking again; what they thought at page 82 cannot.
func (s *Store) AllTimeline() ([]TimelineEntry, error) {
	shelf, err := s.Shelf()
	if err != nil {
		return nil, err
	}
	var out []TimelineEntry
	for _, progress := range shelf {
		events, err := s.Timeline(progress.Book.ID)
		if err != nil {
			continue
		}
		for _, event := range events {
			out = append(out, TimelineEntry{Event: event, Title: progress.Book.Title})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Event.At.After(out[j].Event.At) })
	return out, nil
}
