package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
	_ "time/tzdata"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DailyCheckIn wakes a persona in the parts of the day it was given: any of
// morning, noon, and evening. It is deliberately "around when" rather than "at
// what time": a fixed time would turn a small routine into an appointment.
type DailyCheckIn struct {
	Slots    []string `json:"slots"`
	TimeZone string   `json:"timeZone"`
}

// Slot is one part of the day a check-in can wake in.
type Slot struct {
	Index int
	Name  string // morning / noon / evening, for the prompt
	Label string // 朝 / 昼 / 晩
	Start string // local HH:MM, inclusive
	End   string // local HH:MM, exclusive
}

// DayParts leave the night out: nobody needs a word about the laundry at 3 a.m.
var DayParts = []Slot{
	{Index: 0, Name: "morning", Label: "朝", Start: "06:00", End: "11:00"},
	{Index: 1, Name: "noon", Label: "昼", Start: "11:00", End: "17:00"},
	{Index: 2, Name: "evening", Label: "晩", Start: "17:00", End: "23:00"},
}

// Enabled reports whether any part of the day was chosen.
func (d DailyCheckIn) Enabled() bool { return len(d.Slots) > 0 }

func (d DailyCheckIn) Validate() error {
	if !d.Enabled() {
		return nil
	}
	for i, name := range d.Slots {
		if !slices.ContainsFunc(DayParts, func(part Slot) bool { return part.Name == name }) {
			return fmt.Errorf("unknown part of the day: %q", name)
		}
		if slices.Contains(d.Slots[:i], name) {
			return fmt.Errorf("part of the day listed twice: %q", name)
		}
	}
	if d.TimeZone == "" || d.TimeZone == "Local" {
		return errors.New("check-in requires an IANA time zone")
	}
	_, err := time.LoadLocation(d.TimeZone)
	return err
}

// Due reports the part of the day now falls in and the claim key for it.
// Only the current part runs: a morning missed while the server was down is
// not replayed at noon.
func (d DailyCheckIn) Due(now time.Time) (string, Slot, bool) {
	if !d.Enabled() || d.Validate() != nil {
		return "", Slot{}, false
	}
	loc, _ := time.LoadLocation(d.TimeZone)
	local := now.In(loc)
	clock := local.Format("15:04")
	for _, slot := range DayParts {
		if clock >= slot.Start && clock < slot.End {
			if !slices.Contains(d.Slots, slot.Name) {
				return "", Slot{}, false
			}
			// "2026-09-15#2" sorts after "2026-09-15#0" and before "2026-09-16#0".
			return fmt.Sprintf("%s#%d", local.Format("2006-01-02"), slot.Index), slot, true
		}
	}
	return "", Slot{}, false
}

// Claim before inference: a failed or silent attempt still counts as that part
// of the day's wake. Separate from memory, so forgetting notes cannot cause a
// second wake.
func (s *Store) ClaimDailyCheckIn(id, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "daily-check-ins.json")
	keys := map[string]string{}
	if err := readJSON(path, &keys); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if keys[id] >= key {
		return false, nil
	}
	keys[id] = key
	if err := writeJSON(path, keys); err != nil {
		return false, err
	}
	return true, nil
}

func (w *FirestoreWorkspace) ClaimDailyCheckIn(id, key string) (bool, error) {
	claimed := false
	ref := w.user.Collection("dailyCheckIns").Doc(id)
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		claimed = false
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			previous, _ := doc.Data()["day"].(string)
			if previous >= key {
				return nil
			}
		}
		if err := tx.Set(ref, map[string]any{"day": key}); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}
