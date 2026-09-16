package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DailyCheckIn deliberately supports only one local time per persona.
type DailyCheckIn struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	TimeZone string `json:"timeZone"`
}

func (d DailyCheckIn) Validate() error {
	if !d.Enabled {
		return nil
	}
	if t, err := time.Parse("15:04", d.Time); err != nil || t.Format("15:04") != d.Time {
		return errors.New("daily check-in time must be HH:MM")
	}
	if d.TimeZone == "" || d.TimeZone == "Local" {
		return errors.New("daily check-in requires an IANA time zone")
	}
	_, err := time.LoadLocation(d.TimeZone)
	return err
}

// Due catches up only today's wake, never a backlog of missed days.
func (d DailyCheckIn) Due(now time.Time) (string, bool) {
	if !d.Enabled || d.Validate() != nil {
		return "", false
	}
	loc, _ := time.LoadLocation(d.TimeZone)
	local := now.In(loc)
	return local.Format("2006-01-02"), local.Format("15:04") >= d.Time
}

// Claim before inference: a failed or silent attempt still counts as today's wake.
// Separate from memory, so forgetting notes cannot cause a second wake.
func (s *Store) ClaimDailyCheckIn(id, day string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.root, "daily-check-ins.json")
	days := map[string]string{}
	if err := readJSON(path, &days); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if days[id] >= day {
		return false, nil
	}
	days[id] = day
	if err := writeJSON(path, days); err != nil {
		return false, err
	}
	return true, nil
}

func (w *FirestoreWorkspace) ClaimDailyCheckIn(id, day string) (bool, error) {
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
			if previous >= day {
				return nil
			}
		}
		if err := tx.Set(ref, map[string]any{"day": day}); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}
