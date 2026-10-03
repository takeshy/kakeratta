package store

import (
	"errors"
	"time"
)

// PersonEvent identifies a notification derived from personal information.
// Annual dates are calendar dates, not fixed 365-day intervals.
type PersonEvent struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Annual   bool   `json:"annual"`
	Month    int    `json:"month,omitempty"`
	Day      int    `json:"day,omitempty"`
	LeadDays int    `json:"leadDays,omitempty"`
}

func (e PersonEvent) Validate() error {
	if e.Key == "" || len(e.Key) > 64 || e.Name == "" || len([]rune(e.Name)) > 80 || (e.Source != "basicInfo" && e.Source != "entry") {
		return errors.New("invalid person event")
	}
	if !e.Annual {
		return nil
	}
	date := time.Date(2000, time.Month(e.Month), e.Day, 0, 0, 0, 0, time.UTC)
	if e.Month < 1 || e.Month > 12 || e.Day < 1 || int(date.Month()) != e.Month || date.Day() != e.Day || (e.LeadDays != 0 && e.LeadDays != 7) {
		return errors.New("invalid anniversary date")
	}
	return nil
}

// NextAt returns 09:00 in the user's timezone, with February 29 observed on
// February 28 outside leap years. A newly learned imminent date can notify now.
func (e PersonEvent) NextAt(from time.Time, zone *time.Location, imminent bool) time.Time {
	return e.nextAtClock(from, zone, imminent, 9, 0, 0)
}

func (e PersonEvent) nextAtClock(from time.Time, zone *time.Location, imminent bool, hour, minute, second int) time.Time {
	local := from.In(zone)
	for year := local.Year(); ; year++ {
		day := e.Day
		if e.Month == 2 && day == 29 && time.Date(year, 3, 0, 0, 0, 0, 0, zone).Day() == 28 {
			day = 28
		}
		event := time.Date(year, time.Month(e.Month), day, hour, minute, second, 0, zone)
		notification := event.AddDate(0, 0, -e.LeadDays)
		if notification.After(from) {
			return notification.UTC()
		}
		end := time.Date(year, time.Month(e.Month), day+1, 0, 0, 0, 0, zone)
		if imminent && end.After(from) {
			return from.Truncate(time.Minute).Add(time.Minute).UTC()
		}
	}
}
