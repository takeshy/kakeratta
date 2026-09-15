package store

import (
	"sync"
	"testing"
	"time"
)

var allParts = []string{"morning", "noon", "evening"}

func TestDailyDue(t *testing.T) {
	for _, tc := range []struct {
		zone, at, key, slot string
		due                 bool
	}{
		// Asia/Tokyo is UTC+9.
		{"Asia/Tokyo", "2026-09-14T20:59:00Z", "", "", false},
		{"Asia/Tokyo", "2026-09-14T21:00:00Z", "2026-09-15#0", "morning", true},
		{"Asia/Tokyo", "2026-09-15T01:59:00Z", "2026-09-15#0", "morning", true},
		{"Asia/Tokyo", "2026-09-15T02:00:00Z", "2026-09-15#1", "noon", true},
		{"Asia/Tokyo", "2026-09-15T08:00:00Z", "2026-09-15#2", "evening", true},
		{"Asia/Tokyo", "2026-09-15T13:59:00Z", "2026-09-15#2", "evening", true},
		{"Asia/Tokyo", "2026-09-15T14:00:00Z", "", "", false},
		// After the spring-forward switch New York is UTC-4.
		{"America/New_York", "2026-03-08T12:00:00Z", "2026-03-08#0", "morning", true},
	} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		key, slot, due := (DailyCheckIn{Slots: allParts, TimeZone: tc.zone}).Due(now)
		if key != tc.key || slot.Name != tc.slot || due != tc.due {
			t.Fatalf("%+v: %q %q %v", tc, key, slot.Name, due)
		}
	}
	for _, d := range []DailyCheckIn{
		{Slots: []string{"morning"}, TimeZone: "bad/zone"},
		{Slots: []string{"morning"}},
		{Slots: []string{"morning"}, TimeZone: "Local"},
		{Slots: []string{"night"}, TimeZone: "Asia/Tokyo"},
		{Slots: []string{"morning", "morning"}, TimeZone: "Asia/Tokyo"},
	} {
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
	if _, _, due := (DailyCheckIn{TimeZone: "Asia/Tokyo"}).Due(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)); due {
		t.Fatal("disabled schedule ran")
	}
	// Only the chosen parts wake: evening alone skips the morning.
	evening := DailyCheckIn{Slots: []string{"evening"}, TimeZone: "Asia/Tokyo"}
	if _, _, due := evening.Due(time.Date(2026, 9, 14, 22, 0, 0, 0, time.UTC)); due {
		t.Fatal("an unchosen morning woke")
	}
	if key, _, due := evening.Due(time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)); !due || key != "2026-09-15#2" {
		t.Fatalf("the chosen evening did not wake: %q %v", key, due)
	}
}

func TestDailyClaimSurvivesRestartAndForgetting(t *testing.T) {
	s := open(t)
	var wg sync.WaitGroup
	claims := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimDailyCheckIn("routine", "2026-09-14#0")
			if err != nil {
				t.Error(err)
			}
			claims <- ok
		}()
	}
	wg.Wait()
	close(claims)
	count := 0
	for claimed := range claims {
		if claimed {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claims = %d", count)
	}
	_ = s.ForgetMemory("routine")
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := reopened.ClaimDailyCheckIn("routine", "2026-09-14#0"); ok || err != nil {
		t.Fatalf("restart: %v %v", ok, err)
	}
	if ok, err := reopened.ClaimDailyCheckIn("routine", "2026-09-14#1"); !ok || err != nil {
		t.Fatalf("next part of the day: %v %v", ok, err)
	}
	if ok, err := reopened.ClaimDailyCheckIn("routine", "2026-09-14#0"); ok || err != nil {
		t.Fatalf("an earlier part of the day ran again: %v %v", ok, err)
	}
}
