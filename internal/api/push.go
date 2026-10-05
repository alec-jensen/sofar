package api

import (
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/notify"
)

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || !s.Push.Configured() {
		httpx.Fail(w, http.StatusServiceUnavailable, "push_not_configured", "Push is not configured. Add the VAPID keys on your server.")
		return
	}
	httpx.Respond(w, map[string]string{"publicKey": s.Push.Public})
}

func (s *server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint       string       `json:"endpoint"`
		ExpirationTime any          `json:"expirationTime"`
		Keys           webpush.Keys `json:"keys"`
	}
	if httpx.Decode(r, &in) != nil || notify.Subscribe(r.Context(), s.DB, in.Endpoint, in.Keys) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_subscription", "Invalid or unsupported push subscription.")
		return
	}
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if httpx.Decode(r, &in) != nil || !notify.AllowedEndpoint(in.Endpoint) {
		httpx.Fail(w, http.StatusBadRequest, "invalid_subscription", "Invalid push subscription.")
		return
	}
	if err := notify.Unsubscribe(r.Context(), s.DB, in.Endpoint); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "Could not update notification settings.")
		return
	}
	httpx.Respond(w, map[string]bool{"ok": true})
}

// pushDevices reports how many devices currently receive notifications.
func (s *server) pushDevices(w http.ResponseWriter, r *http.Request) {
	n, err := notify.Devices(r.Context(), s.DB)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "Could not read notification settings.")
		return
	}
	httpx.Respond(w, map[string]int{"count": n})
}

// pushRevokeAll stops notifications on every device, for a lost or retired phone.
func (s *server) pushRevokeAll(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid request.")
		return
	}
	if err := notify.RevokeAll(r.Context(), s.DB); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "Could not turn off notifications.")
		return
	}
	httpx.Respond(w, map[string]bool{"ok": true})
}
