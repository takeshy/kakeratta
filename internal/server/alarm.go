package server

import (
	"sync"
	"time"
)

// localAlarm wakes one workspace from a timer in this process: the alarm of a
// server that runs alone, and of any deployment without Cloud Tasks. The
// earliest time asked for stands; when it fires it queues the due reaction,
// which asks for the next.
type localAlarm struct {
	mu      sync.Mutex
	working sync.WaitGroup
	timer   *time.Timer
	at      time.Time
	fire    func()
	stopped bool
}

func newLocalAlarm(fire func()) *localAlarm { return &localAlarm{fire: fire} }

// Set asks to be woken at at. A wake already set for an earlier time stands:
// it will set the next one itself.
func (a *localAlarm) Set(at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return nil
	}
	if a.timer != nil && !a.at.IsZero() && !at.Before(a.at) {
		return nil
	}
	if a.timer != nil {
		a.timer.Stop()
	}
	a.at = at
	var timer *time.Timer
	timer = time.AfterFunc(time.Until(at), func() {
		a.mu.Lock()
		if a.stopped || a.timer != timer {
			a.mu.Unlock()
			return
		}
		a.timer, a.at = nil, time.Time{}
		a.working.Add(1)
		a.mu.Unlock()
		defer a.working.Done()
		a.fire()
	})
	a.timer = timer
	return nil
}

// Stop ends the alarm for good and waits for any callback already in progress.
func (a *localAlarm) Stop() {
	a.mu.Lock()
	a.stopped = true
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.mu.Unlock()
	a.working.Wait()
}
