package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// A check-in's minute is inside 07:00-21:00 where the user is, the same for
// the same day, drifts by at most an hour a day - wrapping from 21:00 to
// 07:00 - and spreads across personas and across the whole window.
func TestCheckInAtDriftsInsideTheWindow(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	first := time.Date(2026, 1, 1, 15, 30, 0, 0, tokyo)
	minutes := map[string]bool{}
	for p := 0; p < 30; p++ {
		persona := fmt.Sprintf("persona-%d", p)
		previous := time.Time{}
		for d := 0; d < 120; d++ {
			day := first.AddDate(0, 0, d)
			at := CheckInAt("workspace", persona, day).In(tokyo)
			if at.Hour() < 7 || at.Hour() >= 21 || at.Format("2006-01-02") != day.Format("2006-01-02") {
				t.Fatalf("%s on %s: %s", persona, day.Format("2006-01-02"), at)
			}
			if again := CheckInAt("workspace", persona, day.Add(5*time.Hour)); !again.Equal(at) {
				t.Fatalf("the same day moved: %s then %s", at, again)
			}
			if !previous.IsZero() {
				// The window is a ring: 20:50 then 07:10 is twenty minutes.
				clock := func(t time.Time) int { return t.Hour()*60 + t.Minute() - 7*60 }
				diff := ((clock(at)-clock(previous))%840 + 840) % 840
				if diff > 420 {
					diff = 840 - diff
				}
				if diff > 60 {
					t.Fatalf("%s jumped %d minutes on %s", persona, diff, day.Format("2006-01-02"))
				}
			}
			previous = at
		}
		minutes[CheckInAt("workspace", persona, first).In(tokyo).Format("15:04")] = true
	}
	if len(minutes) < 20 {
		t.Fatalf("personas bunch up: %d distinct minutes of 30", len(minutes))
	}
	// Over many personas and days, every hour of the window is used about
	// equally: the edges are not starved.
	hours := map[int]int{}
	total := 0
	for p := 0; p < 200; p++ {
		for d := 0; d < 60; d++ {
			hours[CheckInAt("workspace", fmt.Sprintf("spread-%d", p), first.AddDate(0, 0, d)).In(tokyo).Hour()]++
			total++
		}
	}
	for hour := 7; hour < 21; hour++ {
		if share := float64(hours[hour]) / float64(total); share < 0.04 || share > 0.11 {
			t.Fatalf("hour %d holds %.3f of check-ins: %v", hour, share, hours)
		}
	}
	if closes := CheckInCloses(first); closes.In(tokyo).Format("15:04") != "21:00" {
		t.Fatalf("closes = %s", closes)
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

// A day the minute wraps forward from the evening to the morning is skipped,
// and never two days running.
func TestCheckInSkipsTheDayItWrapsForward(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	first := time.Date(2026, 1, 1, 12, 0, 0, 0, tokyo)
	skips := 0
	for p := 0; p < 50; p++ {
		persona := fmt.Sprintf("skip-%d", p)
		for d := 1; d < 200; d++ {
			day := first.AddDate(0, 0, d)
			yesterday := CheckInAt("w", persona, day.AddDate(0, 0, -1)).In(tokyo)
			today := CheckInAt("w", persona, day).In(tokyo)
			wrapped := today.Hour() < 8 && yesterday.Hour() >= 20
			if CheckInSkipped("w", persona, day) {
				skips++
				if !wrapped {
					t.Fatalf("%s skipped %s without wrapping: %s then %s", persona, day.Format("01-02"), yesterday.Format("15:04"), today.Format("15:04"))
				}
				if CheckInSkipped("w", persona, day.AddDate(0, 0, 1)) {
					t.Fatalf("%s skipped two days running", persona)
				}
			}
		}
	}
	if skips == 0 {
		t.Fatal("no day ever wrapped")
	}
}

// The claim records when the check-in ran and hands back the previous run.
func TestTheClaimRemembersWhenTheLastCheckInRan(t *testing.T) {
	s := open(t)
	first := time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC)
	ok, previous, err := s.ClaimDailyCheckInAt("tech", "2026-09-24", first)
	if err != nil || !ok || !previous.IsZero() {
		t.Fatalf("first claim: %v %s %v", ok, previous, err)
	}
	if ok, _, _ := s.ClaimDailyCheckInAt("tech", "2026-09-24", first.Add(time.Hour)); ok {
		t.Fatal("claimed the same day twice")
	}
	ok, previous, err = s.ClaimDailyCheckInAt("tech", "2026-09-26", first.AddDate(0, 0, 2))
	if err != nil || !ok || !previous.Equal(first) {
		t.Fatalf("next claim: %v %s %v", ok, previous, err)
	}
}

// A time the user set is the account's every day, inside or outside the
// app's window, never skipped, and open until midnight; without one the
// app's drifting minute holds.
func TestDailyAtUsesTheSetTime(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		d := day.AddDate(0, 0, i)
		at, closes, ok := Settings{DailyTime: "06:30"}.DailyAt("ws", d)
		if !ok || !at.Equal(d.Add(6*time.Hour+30*time.Minute)) || !closes.Equal(d.AddDate(0, 0, 1)) {
			t.Fatalf("%s: at=%s closes=%s ok=%v", d, at, closes, ok)
		}
		at, closes, ok = Settings{}.DailyAt("ws", d)
		if !at.Equal(CheckInAt("ws", DailyID, d)) || !closes.Equal(CheckInCloses(d)) || ok != (!CheckInSkipped("ws", DailyID, d) || d.Day() == SummaryDay("ws")) {
			t.Fatalf("%s: automatic at=%s closes=%s ok=%v", d, at, closes, ok)
		}
	}
	for value, valid := range map[string]bool{"": true, "00:00": true, "23:59": true, "7:30": false, "24:00": false, "12:60": false, "noon": false} {
		if ValidDailyTime(value) != valid {
			t.Errorf("ValidDailyTime(%q) = %v", value, !valid)
		}
	}
}
