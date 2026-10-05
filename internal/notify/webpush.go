// Package notify sends Web Push notifications and stores device subscriptions.
// Message content is built elsewhere (M8.8); this package only delivers.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/netguard"
)

// pushHosts are the push services a subscription may point at. A hostile
// endpoint would otherwise make the server call arbitrary hosts.
var pushHosts = []string{"fcm.googleapis.com", "updates.push.services.mozilla.com", "push.services.mozilla.com", "web.push.apple.com", "notify.windows.com"}

// AllowedEndpoint reports whether a subscription endpoint is a known push
// service over HTTPS with no credentials or custom port.
func AllowedEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || len(endpoint) > 4096 {
		return false
	}
	return netguard.HostAllowed(pushHosts, u.Hostname())
}

// ErrNotConfigured means the VAPID keys are missing.
var ErrNotConfigured = errors.New("push is not configured")

// Sender delivers notifications to every stored subscription.
type Sender struct {
	DB      *sql.DB
	Client  *http.Client // use netguard.NewOutboundClient()
	Public  string
	Private string
	Subject string
	Log     zerolog.Logger
}

// Configured reports whether VAPID keys are present.
func (s *Sender) Configured() bool { return s.Public != "" && s.Private != "" }

// Payload is what the service worker receives.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Count int    `json:"count,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// Send delivers a payload to all devices, dropping subscriptions the push
// service reports as gone. It returns how many devices accepted it.
func (s *Sender) Send(ctx context.Context, p Payload, ttlSeconds int) (int, error) {
	if !s.Configured() {
		return 0, ErrNotConfigured
	}
	body, _ := json.Marshal(p)
	rows, err := s.DB.QueryContext(ctx, `SELECT endpoint, keys FROM push_subscriptions`)
	if err != nil {
		return 0, err
	}
	var subs []webpush.Subscription
	for rows.Next() {
		var sub webpush.Subscription
		var keys []byte
		if rows.Scan(&sub.Endpoint, &keys) != nil || json.Unmarshal(keys, &sub.Keys) != nil {
			continue
		}
		subs = append(subs, sub)
	}
	rows.Close()
	sent := 0
	for _, sub := range subs {
		if !AllowedEndpoint(sub.Endpoint) {
			continue
		}
		resp, err := webpush.SendNotificationWithContext(ctx, body, &sub, &webpush.Options{
			Subscriber: s.Subject, VAPIDPublicKey: s.Public, VAPIDPrivateKey: s.Private,
			TTL: ttlSeconds, HTTPClient: s.Client, Topic: p.Tag,
		})
		if err != nil {
			s.Log.Warn().Msg("push delivery failed")
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == 404 || resp.StatusCode == 410:
			s.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=$1`, sub.Endpoint)
		case resp.StatusCode >= 400:
			s.Log.Warn().Int("status", resp.StatusCode).Msg("push provider rejected notification")
		default:
			sent++
		}
	}
	return sent, nil
}

// Subscribe stores or refreshes a device subscription.
func Subscribe(ctx context.Context, db *sql.DB, endpoint string, keys webpush.Keys) error {
	if !AllowedEndpoint(endpoint) || keys.Auth == "" || keys.P256dh == "" {
		return errors.New("invalid or unsupported push subscription")
	}
	raw, _ := json.Marshal(keys)
	_, err := db.ExecContext(ctx, `INSERT INTO push_subscriptions (endpoint, user_id, keys) VALUES ($1,1,$2) ON CONFLICT (endpoint) DO UPDATE SET keys=EXCLUDED.keys`, endpoint, raw)
	return err
}

// Unsubscribe removes one device.
func Unsubscribe(ctx context.Context, db *sql.DB, endpoint string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=$1 AND user_id=1`, endpoint)
	return err
}

// Devices counts subscribed devices.
func Devices(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n)
	return n, err
}

// RevokeAll stops notifications on every device.
func RevokeAll(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM push_subscriptions`)
	return err
}
