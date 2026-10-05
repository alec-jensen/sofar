// Package api is sofar's HTTP surface: the versioned JSON API under /api/v1,
// health endpoints, and the static web app.
package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/accounts"
	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/config"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/importer"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/netguard"
	"github.com/alec-jensen/sofar/internal/notify"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/recurring"
)

// Deps is everything the HTTP layer needs.
type Deps struct {
	Cfg      config.Config
	DB       *sql.DB
	Auth     *auth.Service
	Throttle *auth.Throttle
	Push     *notify.Sender

	// Domain services (nil in tests that do not need them).
	Accounts   *accounts.Service
	Ledger     *ledger.Service
	Categories *categories.Service
	Importer   *importer.Service
	Manual     *manual.Service
	Recompute  *recompute.Service
	Recurring  *recurring.Service
	Budget     *budgetcfg.Service
	Events     *events.Registry
	Log        zerolog.Logger
	// Metrics, when set, is served at /metrics to loopback callers and signed-in users.
	Metrics http.Handler
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
	m.HandleFunc("GET /metrics", s.metrics)

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
	m.Handle("POST /api/v1/push/subscribe", s.protect(s.idempotent(s.pushSubscribe)))
	m.Handle("POST /api/v1/push/unsubscribe", s.protect(s.idempotent(s.pushUnsubscribe)))
	m.Handle("GET /api/v1/push/devices", s.protect(s.pushDevices))
	m.Handle("POST /api/v1/push/revoke-all", s.protect(s.idempotent(s.pushRevokeAll)))

	// ledger
	m.Handle("GET /api/v1/accounts", s.protect(s.listAccounts))
	m.Handle("POST /api/v1/accounts", s.protect(s.idempotent(s.createAccount)))
	m.Handle("PATCH /api/v1/accounts/{id}", s.protect(s.idempotent(s.patchAccount)))
	m.Handle("GET /api/v1/transactions", s.protect(s.listTransactions))
	m.Handle("POST /api/v1/transactions", s.protect(s.idempotent(s.createTransaction)))
	m.Handle("GET /api/v1/transactions/{id}", s.protect(s.getTransaction))
	m.Handle("PATCH /api/v1/transactions/{id}", s.protect(s.idempotent(s.patchTransaction)))
	m.Handle("POST /api/v1/transactions/{id}/split", s.protect(s.idempotent(s.splitTransaction)))
	m.Handle("POST /api/v1/transactions/{id}/hide", s.protect(s.idempotent(s.hideTransaction(false))))
	m.Handle("POST /api/v1/transactions/{id}/unhide", s.protect(s.idempotent(s.hideTransaction(true))))
	m.Handle("GET /api/v1/categories", s.protect(s.listCategories))
	m.Handle("POST /api/v1/categories", s.protect(s.idempotent(s.createCategory)))
	m.Handle("PATCH /api/v1/categories/{id}", s.protect(s.idempotent(s.renameCategory)))
	m.Handle("POST /api/v1/categories/{id}/merge", s.protect(s.idempotent(s.mergeCategory)))
	m.Handle("DELETE /api/v1/categories/{id}", s.protect(s.idempotent(s.deleteCategory)))
	m.Handle("POST /api/v1/category-groups", s.protect(s.idempotent(s.createGroup)))
	m.Handle("POST /api/v1/imports", s.protect(s.createImport))
	m.Handle("GET /api/v1/imports/{id}", s.protect(s.getImport))
	m.Handle("POST /api/v1/imports/{id}/apply", s.protect(s.idempotent(s.applyImport)))
	m.Handle("POST /api/v1/imports/{id}/undo", s.protect(s.idempotent(s.undoImport)))
	m.Handle("POST /api/v1/events/{id}/undo", s.protect(s.idempotent(s.undoEvent)))
	m.Handle("POST /api/v1/events/{id}/redo", s.protect(s.idempotent(s.redoEvent)))

	m.Handle("GET /api/v1/home", s.protect(s.home))
	m.Handle("GET /api/v1/months/{ym}/explain", s.protect(s.explain))
	m.Handle("POST /api/v1/months/{ym}/plan-override", s.protect(s.idempotent(s.planOverride)))

	m.Handle("GET /api/v1/recurring", s.protect(s.listRecurring))
	m.Handle("POST /api/v1/recurring", s.protect(s.idempotent(s.createRecurring)))
	m.Handle("POST /api/v1/recurring/candidates/confirm", s.protect(s.idempotent(s.confirmCandidate)))
	m.Handle("POST /api/v1/recurring/candidates/dismiss", s.protect(s.idempotent(s.dismissCandidate)))
	m.Handle("POST /api/v1/recurring/{id}/end", s.protect(s.idempotent(s.endRecurring)))
	m.Handle("POST /api/v1/transactions/{id}/recurring", s.protect(s.idempotent(s.markRecurring)))
	m.Handle("GET /api/v1/months/{ym}/forecast", s.protect(s.forecast))

	m.Handle("GET /api/v1/settings/savings", s.protect(s.getSavings))
	m.Handle("PUT /api/v1/settings/savings", s.protect(s.idempotent(s.putSavings)))
	m.Handle("GET /api/v1/settings/ef", s.protect(s.getEF))
	m.Handle("PUT /api/v1/settings/ef", s.protect(s.idempotent(s.putEF)))
	m.Handle("GET /api/v1/goals", s.protect(s.listGoals))
	m.Handle("POST /api/v1/goals", s.protect(s.idempotent(s.createGoal)))
	m.Handle("PATCH /api/v1/goals/{id}", s.protect(s.idempotent(s.patchGoal)))
	m.Handle("POST /api/v1/goals/{id}/complete", s.protect(s.idempotent(s.finishGoal(false))))
	m.Handle("POST /api/v1/goals/{id}/abandon", s.protect(s.idempotent(s.finishGoal(true))))
	m.Handle("GET /api/v1/months/{ym}/pot", s.protect(s.pot))
	m.Handle("POST /api/v1/months/{ym}/allocations", s.protect(s.idempotent(s.allocate)))
	m.Handle("POST /api/v1/months/{ym}/allocations/{event}/make-permanent", s.protect(s.idempotent(s.makePermanent)))
	m.Handle("POST /api/v1/months/{ym}/standing/{event}/remove", s.protect(s.idempotent(s.removeStanding)))
	m.Handle("POST /api/v1/months/{ym}/spend-base", s.protect(s.idempotent(s.editSpendBase)))
	m.Handle("POST /api/v1/months/{ym}/spend-base/unpin", s.protect(s.idempotent(s.unpinSpendBase)))
	m.Handle("POST /api/v1/months/{ym}/moved-it", s.protect(s.idempotent(s.movedIt)))
	m.Handle("GET /api/v1/overspends", s.protect(s.listOverspends))
	m.Handle("POST /api/v1/overspends/{ym}/covers", s.protect(s.idempotent(s.cover)))
	m.Handle("POST /api/v1/uncovered/settle", s.protect(s.idempotent(s.settle)))

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

// metrics is reachable from this machine (a scraper on loopback) or by a
// signed-in owner, never anonymously from the network.
func (s *server) metrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		httpx.Fail(w, http.StatusNotFound, "not_found", "Unknown endpoint.")
		return
	}
	if !netguard.IsLoopbackPeer(r) {
		ok := false
		if s.Auth != nil {
			var err error
			ok, err = s.Auth.Authenticate(r.Context(), s.token(r))
			ok = ok && err == nil
		}
		if !ok {
			httpx.Fail(w, http.StatusUnauthorized, "unauthenticated", "Please sign in to continue.")
			return
		}
	}
	s.Metrics.ServeHTTP(w, r)
}
