package store

import (
	"sync"
	"testing"
	"time"
)

func TestDailyDue(t *testing.T) {
	for _, tc := range []struct {
		zone, at, clock, day string
		due                  bool
	}{
		{"Asia/Tokyo", "2026-09-14T22:59:00Z", "08:00", "2026-09-15", false},
		{"Asia/Tokyo", "2026-09-14T23:00:00Z", "08:00", "2026-09-15", true},
		{"Asia/Tokyo", "2026-09-15T14:59:00Z", "08:00", "2026-09-15", true},
		// Nonexistent spring-forward times catch up; repeated fall-back times use the same day claim.
		{"America/New_York", "2026-03-08T07:00:00Z", "02:30", "2026-03-08", true},
		{"America/New_York", "2026-11-01T06:30:00Z", "01:30", "2026-11-01", true},
	} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		day, due := (DailyCheckIn{Enabled: true, Time: tc.clock, TimeZone: tc.zone}).Due(now)
		if day != tc.day || due != tc.due {
			t.Fatalf("%+v: %s %v", tc, day, due)
		}
	}
	for _, d := range []DailyCheckIn{
		{Enabled: true, Time: "25:00", TimeZone: "Asia/Tokyo"},
		{Enabled: true, Time: "8:00", TimeZone: "Asia/Tokyo"},
		{Enabled: true, Time: "08:00", TimeZone: "bad/zone"},
		{Enabled: true, Time: "08:00"},
	} {
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
	if _, due := (DailyCheckIn{}).Due(time.Now()); due {
		t.Fatal("disabled schedule ran")
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
			ok, err := s.ClaimDailyCheckIn("tech", "2026-09-14")
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
	_ = s.ForgetMemory("tech")
	reopened, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := reopened.ClaimDailyCheckIn("tech", "2026-09-14"); ok || err != nil {
		t.Fatalf("restart: %v %v", ok, err)
	}
	if ok, err := reopened.ClaimDailyCheckIn("tech", "2026-09-15"); !ok || err != nil {
		t.Fatalf("next day: %v %v", ok, err)
	}
}
