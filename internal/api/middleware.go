package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/httpx"
	"github.com/alec-jensen/sofar/internal/logging"
	"github.com/alec-jensen/sofar/internal/netguard"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self' otpauth:"

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// middleware applies, in order: panic recovery, security headers, the HTTPS
// requirement, the CSRF origin check, a body size cap, and request logging
// (method, path and status only: never query strings, bodies or cookies).
func (s *server) middleware(next http.Handler) http.Handler {
	proxies := s.Cfg.Proxies()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recover() != nil {
				s.Log.Error().Msg("request panic")
				httpx.Fail(sw, http.StatusInternalServerError, "internal", "Something went wrong.")
			}
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				s.Log.Debug().Str("method", r.Method).Str("path", logging.Scrub(r.URL.Path)).Int("status", sw.status).Dur("took", time.Since(start)).Msg("request")
			}
		}()
		h := sw.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", csp)
		if s.Cfg.Production {
			h.Set("Strict-Transport-Security", "max-age=31536000")
			// Container health checks reach the app over plain HTTP on loopback.
			probe := (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") && netguard.IsLoopbackPeer(r)
			if !probe && r.TLS == nil && !(proxies.Trusted(r) && r.Header.Get("X-Forwarded-Proto") == "https") {
				httpx.Fail(sw, http.StatusBadRequest, "https_required", "HTTPS is required.")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("Origin") != s.Cfg.Origin {
				httpx.Fail(sw, http.StatusForbidden, "bad_origin", "Request origin is not allowed.")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				httpx.Fail(sw, http.StatusUnsupportedMediaType, "json_required", "JSON required.")
				return
			}
		}
		limit := int64(1 << 20)
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/imports" {
			limit = maxImportBody // file uploads
		}
		r.Body = http.MaxBytesReader(sw, r.Body, limit)
		next.ServeHTTP(sw, r)
	})
}
