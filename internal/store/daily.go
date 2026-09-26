package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// How a check-in talks. CheckInMinimal speaks only about what needs the
// user's decision; CheckInShare also looks for something worth sharing.
const (
	CheckInMinimal = "minimal"
	CheckInShare   = "share"
)

// DailyCheckIn is what the user chooses about a persona's check-in: only how
// much it says. When it runs is the app's to choose; Time and TimeZone are
// what earlier versions saved, and are no longer read.
type DailyCheckIn struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
	// Talk is CheckInMinimal or CheckInShare; see CheckInFor.
	Talk string `json:"talk,omitempty"`
}

// Minimal reports whether the check-in speaks only about what needs the
// user's decision.
func (d DailyCheckIn) Minimal() bool { return d.Talk != CheckInShare }

// CheckInFor is a persona's daily check-in as it runs. Every active persona
// checks in once a day; the user chooses only how much it says, and it
// shares unless the user chose minimal.
func (s Settings) CheckInFor(personaID string) DailyCheckIn {
	out := DailyCheckIn{Enabled: true, Talk: CheckInShare}
	if s.DailyCheckIns[personaID].Talk == CheckInMinimal {
		out.Talk = CheckInMinimal
	}
	return out
}

// Check-ins run between these hours of the user's day, each persona at its
// own time, so that neither one user's personas nor every user's wake at once.
const (
	checkInOpens  = 7 * time.Hour
	checkInCloses = 21 * time.Hour
)

// checkInDrift is how many days apart the fixed points of a persona's
// check-in minute are. The window is taken as a ring - past 21:00 comes 07:00
// - and between two points the minute moves the shorter way round in equal
// steps. Two points are at most half the ring apart, so it drifts by at most
// 7 hours over 7 days: an hour a day.
const checkInDrift = 7

// checkInEpoch is where the fixed points are counted from.
var checkInEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// CheckInAt is when a persona checks in on the local day of day: a minute in
// the window that moves by at most an hour from one day to the next, wrapping
// from the end of the window to its start. Every checkInDrift days the minute
// is fixed by a hash of the workspace, the persona and that point; the days
// between walk the shorter way round from one point to the next. Taking the
// window as a ring keeps the minutes spread evenly: walking straight between
// points would crowd them toward the middle of the day.
// Nothing is remembered - the same day always gives the same minute, so a
// restart does not move it - and personas and workspaces spread across the
// window because their points differ.
func CheckInAt(workspace, personaID string, day time.Time) time.Time {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return start.Add(checkInOpens + time.Duration(checkInMinute(workspace, personaID, day))*time.Minute)
}

// CheckInSkipped reports whether the persona's minute wrapped forward past
// the end of the window since the day before - 20:50 yesterday, 07:10 today.
// That day has no check-in: the next is tomorrow's, so two do not come ten
// hours apart. The day after a skip never wraps again, so no two days in a
// row are skipped.
func CheckInSkipped(workspace, personaID string, day time.Time) bool {
	span := int64((checkInCloses - checkInOpens) / time.Minute)
	yesterday := checkInMinute(workspace, personaID, day.AddDate(0, 0, -1))
	today := checkInMinute(workspace, personaID, day)
	step := ((today-yesterday)%span + span) % span
	return step > 0 && step <= span/2 && today < yesterday
}

// checkInMinute is the persona's minute into the window on the local day of
// day; see CheckInAt.
func checkInMinute(workspace, personaID string, day time.Time) int64 {
	index := int64(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).Sub(checkInEpoch) / (24 * time.Hour))
	span := int64((checkInCloses - checkInOpens) / time.Minute)
	point := func(n int64) int64 {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", workspace, personaID, n)))
		return int64(binary.BigEndian.Uint64(sum[:8]) % uint64(span))
	}
	n, step := index/checkInDrift, index%checkInDrift
	if step < 0 {
		n, step = n-1, step+checkInDrift
	}
	from, to := point(n), point(n+1)
	gap := ((to-from)%span + span) % span
	if gap > span/2 {
		gap -= span
	}
	return ((from+gap*step/checkInDrift)%span + span) % span
}

// CheckInCloses is when the window shuts on the local day of day. A check-in
// not run by then - the server was down - is skipped, not run at night.
func CheckInCloses(day time.Time) time.Time {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return start.Add(checkInCloses)
}

// Claim before inference: a failed or silent attempt still counts as today's wake.
// Separate from memory, so forgetting notes cannot cause a second wake.
func (s *Store) ClaimDailyCheckIn(id, day string) (bool, error) {
	claimed, _, err := s.ClaimDailyCheckInAt(id, day, time.Now())
	return claimed, err
}

// ClaimDailyCheckInAt claims the day's check-in and records when it ran. It
// hands back when the previous check-in ran - the zero time if none is known
// - so the new one can read everything since, whatever days lay between.
func (s *Store) ClaimDailyCheckInAt(id, day string, at time.Time) (bool, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "daily-check-ins.json")
	days := map[string]string{}
	if err := readJSON(path, &days); err != nil && !os.IsNotExist(err) {
		return false, time.Time{}, err
	}
	if days[id] >= day {
		return false, time.Time{}, nil
	}
	timesPath := filepath.Join(s.root, "daily-check-in-times.json")
	times := map[string]time.Time{}
	if err := readJSON(timesPath, &times); err != nil && !os.IsNotExist(err) {
		return false, time.Time{}, err
	}
	previous := times[id]
	days[id] = day
	if err := writeJSON(path, days); err != nil {
		return false, time.Time{}, err
	}
	times[id] = at.UTC()
	if err := writeJSON(timesPath, times); err != nil {
		return false, time.Time{}, err
	}
	return true, previous, nil
}

func (w *FirestoreWorkspace) ClaimDailyCheckIn(id, day string) (bool, error) {
	claimed, _, err := w.ClaimDailyCheckInAt(id, day, time.Now())
	return claimed, err
}

func (w *FirestoreWorkspace) ClaimDailyCheckInAt(id, day string, at time.Time) (bool, time.Time, error) {
	claimed := false
	var previous time.Time
	ref := w.user.Collection("dailyCheckIns").Doc(id)
	err := w.client.RunTransaction(context.Background(), func(ctx context.Context, tx *firestore.Transaction) error {
		claimed, previous = false, time.Time{}
		doc, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			data := doc.Data()
			if last, _ := data["day"].(string); last >= day {
				return nil
			}
			previous, _ = data["at"].(time.Time)
		}
		if err := tx.Set(ref, map[string]any{"day": day, "at": at.UTC()}); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, previous, err
}
