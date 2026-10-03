package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Schedule is when to wake a persona, and why.
//
// The app holds this the way it holds the endpoint, the active personas and the
// check-in time: it is what the app needs in order to call anyone. The contents
// are the persona's own - it asks for a schedule, with a note of what it is
// for, and it is the one that moves or cancels it afterwards.
type Schedule struct {
	ID    string `json:"id"`
	Skill string `json:"skill"`
	Daily bool   `json:"daily"`
	// Nil preserves notification-on behavior for existing schedules.
	Notify *bool `json:"notify,omitempty"`
	// TimeEdited preserves an explicitly chosen reminder time during calendar sync.
	TimeEdited bool `json:"timeEdited,omitempty"`
	// UserRequested distinguishes an explicit scheduled task from an autonomous check.
	// Missing values in older schedules retain the autonomous-check behavior.
	UserRequested bool      `json:"userRequested,omitempty"`
	At            time.Time `json:"at"`
	TimeZone      string    `json:"timeZone"`
	Note          string    `json:"note"`
	// NotePath is the note a persona wrote alongside the schedule when memory
	// was one note per thing. It is kept for schedules saved then and no
	// longer set.
	NotePath    string       `json:"notePath,omitempty"`
	PersonEvent *PersonEvent `json:"personEvent,omitempty"`
}

// NotificationsEnabled applies the legacy default without rewriting saved data.
func (s Schedule) NotificationsEnabled() bool { return s.Notify == nil || *s.Notify }

// SchedulePatch updates only the next time and push preference, retaining identity and recurrence.
type SchedulePatch struct {
	At     *time.Time `json:"at,omitempty"`
	Notify *bool      `json:"notify,omitempty"`
}

func (p SchedulePatch) Apply(s Schedule, now time.Time) (Schedule, error) {
	if p.At != nil {
		if !p.At.After(now) {
			return Schedule{}, ErrScheduleTime
		}
		s.At = p.At.UTC()
		s.TimeEdited = true
	}
	if p.Notify != nil {
		value := *p.Notify
		s.Notify = &value
	}
	return s, nil
}

// MaxSchedulesPerSkill caps what one persona can queue: every wake is a model
// call, and a persona that sets them in a loop would pay for it every minute.
const MaxSchedulesPerSkill = 20

// ScheduleGrace drops a wake nobody could run in time instead of replaying it.
// Coming back from downtime should not spend the morning saying yesterday.
const ScheduleGrace = 10 * time.Minute

var (
	ErrScheduleTime     = errors.New("schedule は未来の時刻を指定してください")
	ErrScheduleNote     = errors.New("schedule には何のためかを書いてください（200文字以内）")
	ErrScheduleZone     = errors.New("毎日繰り返す schedule には IANA のタイムゾーンが必要です")
	ErrScheduleTooMany  = errors.New("この担当の schedule は上限に達しています。不要なものを cancel で消してください")
	ErrScheduleNotFound = errors.New("その schedule は見つかりません")
)

// Validate checks a schedule before it is stored, with now passed in so the
// caller decides what "too late" means.
func (s Schedule) Validate(now time.Time) error {
	if s.At.IsZero() || !s.At.After(now) {
		return ErrScheduleTime
	}
	if note := []rune(s.Note); len(note) == 0 || len(note) > 200 {
		return ErrScheduleNote
	}
	if s.PersonEvent != nil {
		if err := s.PersonEvent.Validate(); err != nil {
			return err
		}
		if s.Daily {
			return errors.New("person events cannot repeat daily")
		}
	}
	if s.Daily || s.PersonEvent != nil {
		if s.TimeZone == "" || s.TimeZone == "Local" {
			return ErrScheduleZone
		}
		if _, err := time.LoadLocation(s.TimeZone); err != nil {
			return ErrScheduleZone
		}
	}
	return nil
}

// Next advances a recurring schedule using calendar dates in its timezone,
// keeping the local clock time across daylight-saving changes.
func (s Schedule) Next(from time.Time) (time.Time, bool) {
	if s.PersonEvent != nil && s.PersonEvent.Annual {
		if s.PersonEvent.Validate() != nil {
			return time.Time{}, false
		}
		zone, err := time.LoadLocation(s.TimeZone)
		if err != nil {
			zone = time.UTC
		}
		if s.TimeEdited {
			clock := s.At.In(zone)
			return s.PersonEvent.nextAtClock(from, zone, false, clock.Hour(), clock.Minute(), clock.Second()), true
		}
		return s.PersonEvent.NextAt(from, zone, false), true
	}
	if !s.Daily {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	next := s.At.In(loc).AddDate(0, 0, 1)
	for !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC(), true
}

func schedulesPath(root string) string { return filepath.Join(root, "schedules.json") }

func readSchedules(root string) (map[string]Schedule, error) {
	schedules := map[string]Schedule{}
	if err := readJSON(schedulesPath(root), &schedules); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return schedules, nil
}

func sortedSchedules(schedules map[string]Schedule, skill string) []Schedule {
	out := make([]Schedule, 0, len(schedules))
	for id, schedule := range schedules {
		if skill != "" && schedule.Skill != skill {
			continue
		}
		schedule.ID = id
		out = append(out, schedule)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}

// Schedules lists one persona's schedules, or every one when skill is empty.
func (s *Store) Schedules(skill string) ([]Schedule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return nil, err
	}
	return sortedSchedules(schedules, skill), nil
}

// SaveSchedule stores a new schedule and returns it with its id filled in.
func (s *Store) SaveSchedule(schedule Schedule) (Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return Schedule{}, err
	}
	if schedule.ID == "" {
		schedule.ID = NewID("s")
	}
	count := 0
	for id, existing := range schedules {
		if existing.Skill == schedule.Skill && id != schedule.ID {
			count++
		}
	}
	if count >= MaxSchedulesPerSkill {
		return Schedule{}, ErrScheduleTooMany
	}
	schedule = preservePersonSchedule(schedule, schedules[schedule.ID])
	schedules[schedule.ID] = schedule
	if err := writeJSON(schedulesPath(s.root), schedules); err != nil {
		return Schedule{}, err
	}
	return schedule, nil
}

// DeleteSchedule removes one of a persona's own schedules.
func (s *Store) DeleteSchedule(skill, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return err
	}
	schedule, ok := schedules[id]
	if !ok || (skill != "" && schedule.Skill != skill) {
		return ErrScheduleNotFound
	}
	delete(schedules, id)
	return writeJSON(schedulesPath(s.root), schedules)
}

// DeleteSchedules drops everything one persona had queued. The app owns this
// store, so it may clear its own bookkeeping when a persona is paused, removed,
// or has its whole memory forgotten.
func (s *Store) DeleteSchedules(skill string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return err
	}
	removed := false
	for id, schedule := range schedules {
		if schedule.Skill == skill {
			delete(schedules, id)
			removed = true
		}
	}
	if !removed {
		return nil
	}
	return writeJSON(schedulesPath(s.root), schedules)
}

// ClaimSchedule takes a due schedule before the model is called, so a silent or
// failed wake is not repeated. A daily schedule moves to its next day; a
// one-off is removed. The claim fails when someone else already took it.
func (s *Store) ClaimSchedule(id string, at time.Time, now time.Time) (Schedule, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return Schedule{}, false, err
	}
	schedule, ok := schedules[id]
	if !ok || !schedule.At.Equal(at) {
		return Schedule{}, false, nil
	}
	schedule.ID = id
	if next, repeats := schedule.Next(now); repeats {
		advanced := schedule
		advanced.At = next
		schedules[id] = advanced
	} else {
		delete(schedules, id)
	}
	if err := writeJSON(schedulesPath(s.root), schedules); err != nil {
		return Schedule{}, false, err
	}
	return schedule, true, nil
}

func (w *FirestoreWorkspace) Schedules(skill string) ([]Schedule, error) {
	docs, err := w.user.Collection("schedules").Documents(context.Background()).GetAll()
	if err != nil {
		return nil, err
	}
	schedules := map[string]Schedule{}
	for _, doc := range docs {
		var schedule Schedule
		if doc.DataTo(&schedule) != nil {
			continue
		}
		schedules[doc.Ref.ID] = schedule
	}
	return sortedSchedules(schedules, skill), nil
}

func (w *FirestoreWorkspace) SaveSchedule(schedule Schedule) (Schedule, error) {
	if schedule.ID == "" {
		schedule.ID = NewID("s")
	}
	existing, err := w.Schedules(schedule.Skill)
	if err != nil {
		return Schedule{}, err
	}
	count := 0
	for _, one := range existing {
		if one.ID != schedule.ID {
			count++
		}
	}
	if count >= MaxSchedulesPerSkill {
		return Schedule{}, ErrScheduleTooMany
	}
	ref := w.user.Collection("schedules").Doc(sanitise(schedule.ID))
	proposed := schedule
	err = w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		schedule = proposed
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			var current Schedule
			if err := doc.DataTo(&current); err != nil {
				return err
			}
			schedule = preservePersonSchedule(schedule, current)
		}
		return tx.Set(ref, schedule)
	})
	return schedule, err
}

func (w *FirestoreWorkspace) DeleteSchedule(skill, id string) error {
	ref := w.user.Collection("schedules").Doc(sanitise(id))
	doc, err := ref.Get(context.Background())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrScheduleNotFound
		}
		return err
	}
	var schedule Schedule
	if doc.DataTo(&schedule) == nil && skill != "" && schedule.Skill != skill {
		return ErrScheduleNotFound
	}
	_, err = ref.Delete(context.Background())
	return err
}

func (w *FirestoreWorkspace) DeleteSchedules(skill string) error {
	schedules, err := w.Schedules(skill)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if _, err := w.user.Collection("schedules").Doc(sanitise(schedule.ID)).Delete(context.Background()); err != nil {
			return err
		}
	}
	return nil
}

func (w *FirestoreWorkspace) ClaimSchedule(id string, at time.Time, now time.Time) (Schedule, bool, error) {
	var claimed Schedule
	taken := false
	ref := w.user.Collection("schedules").Doc(sanitise(id))
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		taken = false
		doc, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		}
		var schedule Schedule
		if err := doc.DataTo(&schedule); err != nil {
			return err
		}
		schedule.ID = id
		if !schedule.At.Equal(at) {
			return nil
		}
		if next, repeats := schedule.Next(now); repeats {
			advanced := schedule
			advanced.At = next
			if err := tx.Set(ref, advanced); err != nil {
				return err
			}
		} else if err := tx.Delete(ref); err != nil {
			return err
		}
		claimed = schedule
		taken = true
		return nil
	})
	return claimed, taken, err
}

// UpdateSchedule is atomic with claims: a fired one-off cannot be recreated by an edit.
func (s *Store) UpdateSchedule(skill, id string, patch SchedulePatch) (Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schedules, err := readSchedules(s.root)
	if err != nil {
		return Schedule{}, err
	}
	current, ok := schedules[id]
	if !ok || current.Skill != skill {
		return Schedule{}, ErrScheduleNotFound
	}
	updated, err := patch.Apply(current, time.Now())
	if err != nil {
		return Schedule{}, err
	}
	updated.ID = id
	schedules[id] = updated
	if err := writeJSON(schedulesPath(s.root), schedules); err != nil {
		return Schedule{}, err
	}
	return updated, nil
}

func (w *FirestoreWorkspace) UpdateSchedule(skill, id string, patch SchedulePatch) (Schedule, error) {
	var updated Schedule
	ref := w.user.Collection("schedules").Doc(sanitise(id))
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return ErrScheduleNotFound
		}
		if err != nil {
			return err
		}
		var current Schedule
		if err := doc.DataTo(&current); err != nil {
			return err
		}
		if current.Skill != skill {
			return ErrScheduleNotFound
		}
		updated, err = patch.Apply(current, time.Now())
		if err != nil {
			return err
		}
		updated.ID = id
		return tx.Set(ref, updated)
	})
	return updated, err
}

// Calendar extraction may run alongside an edit. Merge user preferences at the write,
// and retain an annual occurrence already advanced by a claim.
func preservePersonSchedule(next, current Schedule) Schedule {
	if next.PersonEvent == nil || current.PersonEvent == nil || next.Skill != current.Skill {
		return next
	}
	next.Notify = current.Notify
	a, b := next.PersonEvent, current.PersonEvent
	if next.TimeZone == current.TimeZone && a.Annual == b.Annual && a.Month == b.Month && a.Day == b.Day && a.LeadDays == b.LeadDays {
		if a.Annual || current.TimeEdited {
			next.At = current.At
		}
		next.TimeEdited = current.TimeEdited
	}
	return next
}
