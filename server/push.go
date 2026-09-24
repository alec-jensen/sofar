package main

import (
	"context"
	"encoding/json"
	"fmt"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog/log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("VAPID_PUBLIC_KEY") == "" || os.Getenv("VAPID_PRIVATE_KEY") == "" {
		fail(w, 503, "Push is not configured. Add the VAPID keys on your server.")
		return
	}
	respond(w, map[string]string{"publicKey": os.Getenv("VAPID_PUBLIC_KEY")})
}
func allowedPushEndpoint(endpoint string) bool {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := u.Hostname()
	for _, allowed := range []string{"fcm.googleapis.com", "updates.push.services.mozilla.com", "push.services.mozilla.com", "web.push.apple.com", "notify.windows.com"} {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
func (s *server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint       string       `json:"endpoint"`
		ExpirationTime any          `json:"expirationTime"`
		Keys           webpush.Keys `json:"keys"`
	}
	if decode(r, &in) != nil || !allowedPushEndpoint(in.Endpoint) || len(in.Endpoint) > 4096 || in.Keys.Auth == "" || in.Keys.P256dh == "" {
		fail(w, 400, "Invalid or unsupported push subscription.")
		return
	}
	keys, _ := json.Marshal(in.Keys)
	if _, e := s.db.ExecContext(r.Context(), "INSERT INTO push_subscriptions(endpoint,user_id,keys) VALUES($1,1,$2) ON CONFLICT(endpoint) DO UPDATE SET keys=EXCLUDED.keys", in.Endpoint, string(keys)); e != nil {
		fail(w, 500, "Could not save notification settings.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if decode(r, &in) != nil || !allowedPushEndpoint(in.Endpoint) {
		fail(w, 400, "Invalid push subscription.")
		return
	}
	if _, e := s.db.ExecContext(r.Context(), "DELETE FROM push_subscriptions WHERE endpoint=$1 AND user_id=1", in.Endpoint); e != nil {
		fail(w, 500, "Could not update notification settings.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) notify(ctx context.Context) {
	if os.Getenv("VAPID_PRIVATE_KEY") == "" || os.Getenv("VAPID_PUBLIC_KEY") == "" {
		return
	}
	var transactions, recurring, income, mismatches, repayments int
	if e := s.db.QueryRowContext(ctx, "SELECT count(*),count(*) FILTER(WHERE direction='in') FROM transactions WHERE review_status='pending' AND bank_pending=false").Scan(&transactions, &income); e != nil {
		return
	}
	if e := s.db.QueryRowContext(ctx, "SELECT count(*) FILTER(WHERE dismissed=false AND (confirmed=false OR mismatch=true)),count(*) FILTER(WHERE dismissed=false AND mismatch=true) FROM recurring_groups").Scan(&recurring, &mismatches); e != nil {
		return
	}
	s.db.QueryRowContext(ctx, `SELECT count(*) FROM transactions c WHERE c.direction='in' AND c.review_status='pending' AND c.bank_pending=false AND EXISTS(SELECT 1 FROM transactions e WHERE e.direction='out' AND e.review_status='confirmed' AND e.date BETWEEN c.date-60 AND c.date AND e.amount>=c.amount AND e.amount<=c.amount*1.5)`).Scan(&repayments)
	count := transactions + recurring
	if count == 0 {
		return
	}
	body := fmt.Sprintf("%d transactions to review", transactions)
	if recurring > 0 {
		body += fmt.Sprintf(" · %d recurring items", recurring)
	}
	if income > 0 {
		body += fmt.Sprintf(" · %d incoming credits", income)
	}
	if mismatches > 0 {
		body += fmt.Sprintf(" · %d amount changes", mismatches)
	}
	if repayments > 0 {
		body += fmt.Sprintf(" · %d possible repayments", repayments)
	}
	payload, _ := json.Marshal(map[string]any{"title": "a few things to look over", "body": body, "count": count, "url": "/#review", "tag": "sofar-review"})
	rows, e := s.db.QueryContext(ctx, "SELECT endpoint,keys FROM push_subscriptions")
	if e != nil {
		return
	}
	subs := []webpush.Subscription{}
	for rows.Next() {
		var sub webpush.Subscription
		var keys []byte
		if rows.Scan(&sub.Endpoint, &keys) != nil {
			continue
		}
		if json.Unmarshal(keys, &sub.Keys) != nil {
			continue
		}
		subs = append(subs, sub)
	}
	rows.Close()
	for _, sub := range subs {
		if !allowedPushEndpoint(sub.Endpoint) {
			continue
		}
		response, e := webpush.SendNotificationWithContext(ctx, payload, &sub, &webpush.Options{Subscriber: env("VAPID_SUBJECT", "mailto:admin@example.com"), VAPIDPublicKey: os.Getenv("VAPID_PUBLIC_KEY"), VAPIDPrivateKey: os.Getenv("VAPID_PRIVATE_KEY"), TTL: 3600, HTTPClient: s.client, Topic: "sofar-review"})
		if e != nil {
			log.Warn().Msg("push delivery failed")
			continue
		}
		response.Body.Close()
		if response.StatusCode == 404 || response.StatusCode == 410 {
			s.db.ExecContext(ctx, "DELETE FROM push_subscriptions WHERE endpoint=$1", sub.Endpoint)
		} else if response.StatusCode >= 400 {
			log.Warn().Int("status", response.StatusCode).Msg("push provider rejected notification")
		}
	}
}
