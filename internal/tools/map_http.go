package tools

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Public geocoding and routing endpoints allow one request per second.
// Share the limiter across users and personas in this server process.
var mapRequests = struct {
	sync.Mutex
	next map[string]time.Time
}{next: map[string]time.Time{}}

func doMapRequest(request *http.Request) (*http.Response, error) {
	host := strings.ToLower(request.URL.Hostname())
	switch host {
	case "nominatim.openstreetmap.org", "router.project-osrm.org", "routing.openstreetmap.de":
		mapRequests.Lock()
		slot := mapRequests.next[host]
		if now := time.Now(); slot.Before(now) {
			slot = now
		}
		mapRequests.next[host] = slot.Add(time.Second)
		mapRequests.Unlock()
		timer := time.NewTimer(time.Until(slot))
		select {
		case <-request.Context().Done():
			timer.Stop()
			return nil, request.Context().Err()
		case <-timer.C:
		}
	}
	return httpClient.Do(request)
}
