// Package api is sofar's HTTP surface: the versioned JSON API under /api/v1,
// health endpoints, and the static web app.
package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/config"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/notify"
)

// Deps is everything the HTTP layer needs.
type Deps struct {
	Cfg      config.Config
	DB       *sql.DB
	Auth     *auth.Service
	Throttle *auth.Throttle
	Push     *notify.Sender
	Log      zerolog.Logger
	// Ready reports whether the database is reachable and migrations are current.
	Ready func(ctx context.Context) error
}

type server struct {
	Deps
	mux *http.ServeMux
}

// New builds the full HTTP handler.
func New(d Deps) http.Handler {
	if d.Throttle == nil {
		d.Throttle = auth.NewThrottle()
	}
	s := &server{Deps: d, mux: http.NewServeMux()}
	s.routes()
	return s.middleware(s.mux)
}

func (s *server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("GET /readyz", s.readyz)

	// auth (public)
	m.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	m.HandleFunc("POST /api/v1/auth/setup", s.setup)
	m.HandleFunc("POST /api/v1/auth/login", s.login)
	// auth (signed in)
	m.Handle("POST /api/v1/auth/logout", s.protect(s.logout))
	m.Handle("POST /api/v1/auth/totp/setup", s.protect(s.totpSetup))
	m.Handle("POST /api/v1/auth/totp/confirm", s.protect(s.totpConfirm))
	m.Handle("POST /api/v1/auth/totp/disable", s.protect(s.totpDisable))
	m.Handle("POST /api/v1/auth/password", s.protect(s.changePassword))
	m.Handle("POST /api/v1/auth/recovery-codes", s.protect(s.recoveryCodes))

	// push
	m.Handle("GET /api/v1/push/config", s.protect(s.pushConfig))
	m.Handle("POST /api/v1/push/subscribe", s.protect(s.pushSubscribe))
	m.Handle("POST /api/v1/push/unsubscribe", s.protect(s.pushUnsubscribe))
	m.Handle("GET /api/v1/push/devices", s.protect(s.pushDevices))
	m.Handle("POST /api/v1/push/revoke-all", s.protect(s.pushRevokeAll))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Unknown endpoint.")
	})
	m.HandleFunc("/", s.static)
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.Ready != nil {
		if err := s.Ready(r.Context()); err != nil {
			httpx.Fail(w, http.StatusServiceUnavailable, "not_ready", "Not ready.")
			return
		}
	}
	httpx.Respond(w, map[string]bool{"ok": true})
}
