package server

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

func pushOptions() *webpush.Options {
	return &webpush.Options{VAPIDPublicKey: os.Getenv("KAKERATTA_PUSH_PUBLIC_KEY"), VAPIDPrivateKey: os.Getenv("KAKERATTA_PUSH_PRIVATE_KEY"), Subscriber: os.Getenv("KAKERATTA_PUSH_SUBJECT"), TTL: 3600, Urgency: webpush.UrgencyHigh,
		HTTPClient: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func pushReady(options *webpush.Options) bool {
	private, err := base64.RawURLEncoding.DecodeString(options.VAPIDPrivateKey)
	if err != nil {
		return false
	}
	key, err := ecdh.P256().NewPrivateKey(private)
	if err != nil {
		return false
	}
	public, err := base64.RawURLEncoding.DecodeString(options.VAPIDPublicKey)
	if err != nil || base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) != base64.RawURLEncoding.EncodeToString(public) {
		return false
	}
	subject, err := url.Parse(options.Subscriber)
	return err == nil && ((subject.Scheme == "mailto" && subject.Opaque != "") || (subject.Scheme == "https" && subject.Host != ""))
}

func (s *Server) pushConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	options := pushOptions()
	key := ""
	if pushReady(options) {
		key = options.VAPIDPublicKey
	}
	subscribed := false
	if endpoint := r.URL.Query().Get("endpoint"); validPushEndpoint(endpoint) {
		_, subscribed, _ = s.store.Record(r.Context(), store.RecordPush, pushID(endpoint))
	}
	writeJSON(w, 200, map[string]any{"available": key != "", "publicKey": key, "subscribed": subscribed})
}

// Only browser push providers are accepted; a supplied endpoint must never
// turn a scheduled reminder into a request to an arbitrary service.
func validPushEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || len(endpoint) > 2048 {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || host == "web.push.apple.com" || strings.HasSuffix(host, ".notify.windows.com")
}

func pushID(endpoint string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint))) }

func (s *Server) savePush(w http.ResponseWriter, r *http.Request) {
	if !pushReady(pushOptions()) {
		fail(w, 503, "Push notifications are not configured")
		return
	}
	var subscription webpush.Subscription
	if readJSONLimit(r, &subscription, 4096) != nil || !validPushEndpoint(subscription.Endpoint) {
		fail(w, 400, "Invalid push subscription")
		return
	}
	auth, authErr := base64.RawURLEncoding.DecodeString(subscription.Keys.Auth)
	public, keyErr := base64.RawURLEncoding.DecodeString(subscription.Keys.P256dh)
	_, curveErr := ecdh.P256().NewPublicKey(public)
	if authErr != nil || len(auth) != 16 || keyErr != nil || curveErr != nil {
		fail(w, 400, "Invalid push keys")
		return
	}
	raw, _ := json.Marshal(subscription)
	// Serialise registrations across instances and cap the number of devices.
	ctx, release, err := s.store.Lock(r.Context(), "push-subscriptions")
	if err != nil {
		fail(w, 500, "Could not save push subscription")
		return
	}
	defer release()
	records, err := s.store.Records(ctx, store.RecordPush)
	if err != nil {
		fail(w, 500, "Could not read push subscriptions")
		return
	}
	id := pushID(subscription.Endpoint)
	if _, exists := records[id]; !exists && len(records) >= 20 {
		fail(w, 409, "Too many push devices")
		return
	}
	err = s.store.UpdateRecord(ctx, store.RecordPush, id, time.Time{}, func([]byte, bool) ([]byte, error) { return raw, nil })
	if err != nil {
		fail(w, 500, "Could not save push subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deletePush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if readJSONLimit(r, &body, 4096) != nil || !validPushEndpoint(body.Endpoint) {
		fail(w, 400, "Invalid push endpoint")
		return
	}
	if err := s.store.UpdateRecord(r.Context(), store.RecordPush, pushID(body.Endpoint), time.Time{}, func([]byte, bool) ([]byte, error) { return nil, nil }); err != nil {
		fail(w, 500, "Could not remove push subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sendReminder(data any) {
	entry, ok := data.(timeline.Entry)
	if !ok || entry.Deleted || entry.Speaker == timeline.User || entry.ID == "" {
		return
	}
	options := pushOptions()
	if !pushReady(options) {
		return
	}
	// webpush-go adds mailto: to non-HTTPS subscribers itself.
	options.Subscriber = strings.TrimPrefix(options.Subscriber, "mailto:")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	records, err := s.store.Records(ctx, store.RecordPush)
	if err != nil {
		log.Printf("push: could not read subscriptions: %v", err)
		return
	}
	title := []rune(strings.Join(strings.Fields(entry.Text), " "))
	if len(title) > 120 {
		title = append(title[:120], '…')
	}
	if len(title) == 0 {
		title = []rune("Reminder / リマインダー")
	}
	payload, _ := json.Marshal(map[string]string{"title": string(title), "body": "kakeratta", "entry": entry.ID, "workspace": s.mcpWorkspace()})
	options.Topic = pushID(entry.ID)[:32]
	var deliveries sync.WaitGroup
	for id, raw := range records {
		deliveries.Go(func() {
			var subscription webpush.Subscription
			if json.Unmarshal(raw, &subscription) != nil || !validPushEndpoint(subscription.Endpoint) {
				return
			}
			response, err := webpush.SendNotificationWithContext(ctx, payload, &subscription, options)
			if err != nil {
				log.Print("push: delivery failed")
				return
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode == 404 || response.StatusCode == 410 {
				_ = s.store.UpdateRecord(ctx, store.RecordPush, id, time.Time{}, func(current []byte, exists bool) ([]byte, error) {
					// Do not delete a subscription refreshed during delivery.
					if string(current) == string(raw) {
						return nil, nil
					}
					return current, nil
				})
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				log.Printf("push: provider returned %d", response.StatusCode)
			}
		})
	}
	deliveries.Wait()
}
