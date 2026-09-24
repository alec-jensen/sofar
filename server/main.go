package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/alec-jensen/sofar/internal/db"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type server struct {
	db                     *sql.DB
	origin                 string
	production, trustProxy bool
	encryption             cipher.AEAD
	client                 *http.Client
	syncMu                 sync.Mutex
	authMu                 sync.Mutex
	attempts               map[string]attempt
	plaidBase              string
}
type attempt struct {
	Count int
	Until time.Time
}

func main() {
	log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	if len(os.Args) > 1 && os.Args[1] == "vapid" {
		private, public, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatal().Err(err).Msg("generate VAPID keys")
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", public, private)
		return
	}
	origin := env("SOFAR_ORIGIN", "http://localhost:8080")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" {
		log.Fatal().Msg("SOFAR_ORIGIN must be an origin without a trailing slash")
	}
	production := env("SOFAR_ENV", "production") == "production"
	if production && u.Scheme != "https" {
		log.Fatal().Msg("Production requires an HTTPS SOFAR_ORIGIN")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("SOFAR_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		log.Fatal().Msg("SOFAR_ENCRYPTION_KEY must be 32 random bytes in base64")
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	startup, stop := context.WithTimeout(ctx, 30*time.Second)
	d, err := db.Open(startup, os.Getenv("DATABASE_URL"))
	stop()
	if err != nil {
		log.Fatal().Err(err).Msg("open database")
	}
	defer d.Close()
	plaidEnv := env("PLAID_ENV", "sandbox")
	if plaidEnv != "sandbox" && plaidEnv != "production" {
		log.Fatal().Msg("PLAID_ENV must be sandbox or production")
	}
	s := &server{db: d, origin: origin, production: production, trustProxy: os.Getenv("TRUST_PROXY") == "true", encryption: aead, client: &http.Client{Timeout: 45 * time.Second}, attempts: map[string]attempt{}, plaidBase: "https://" + plaidEnv + ".plaid.com"}
	httpServer := &http.Server{Addr: env("SOFAR_ADDR", "127.0.0.1:8080"), Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go s.poll(ctx)
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		httpServer.Shutdown(c)
	}()
	log.Info().Str("address", httpServer.Addr).Msg("sofar is ready")
	if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("serve")
	}
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func (s *server) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/auth/status", s.authStatus)
	m.HandleFunc("POST /api/auth/setup", s.setup)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/plaid/webhook", s.webhook)
	m.Handle("POST /api/auth/logout", s.protect(s.logout))
	m.Handle("POST /api/auth/totp/setup", s.protect(s.totpSetup))
	m.Handle("POST /api/auth/totp/confirm", s.protect(s.totpConfirm))
	m.Handle("GET /api/state", s.protect(s.stateHandler))
	m.Handle("POST /api/actions", s.protect(s.actionHandler))
	m.Handle("POST /api/sync", s.protect(s.syncHandler))
	m.Handle("POST /api/plaid/link-token", s.protect(s.linkToken))
	m.Handle("POST /api/plaid/exchange", s.protect(s.exchange))
	m.Handle("GET /api/push/config", s.protect(s.pushConfig))
	m.Handle("POST /api/push/subscribe", s.protect(s.pushSubscribe))
	m.Handle("POST /api/push/unsubscribe", s.protect(s.pushUnsubscribe))
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			fail(w, 503, "Database unavailable.")
			return
		}
		respond(w, map[string]bool{"ok": true})
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Unknown endpoint.") })
	files := http.FileServer(http.Dir(env("SOFAR_STATIC_DIR", "dist")))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := filepath.Clean(r.URL.Path)
		if strings.Contains(path, "..") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if _, err := os.Stat(filepath.Join(env("SOFAR_STATIC_DIR", "dist"), path)); err != nil {
			if filepath.Ext(path) != "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(env("SOFAR_STATIC_DIR", "dist"), "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				log.Error().Msg("request panic")
				fail(w, 500, "Something went wrong.")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdn.plaid.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://*.plaid.com; connect-src 'self' https://*.plaid.com; frame-src https://*.plaid.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self' otpauth:")
		if s.production {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
			if r.TLS == nil && !(s.trustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
				fail(w, 400, "HTTPS is required.")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/plaid/webhook" {
			origin := r.Header.Get("Origin")
			if origin != s.origin {
				fail(w, 403, "Request origin is not allowed.")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 415, "JSON required.")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		m.ServeHTTP(w, r)
	})
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *server) seal(raw string) string {
	nonce := make([]byte, s.encryption.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(s.encryption.Seal(nonce, nonce, []byte(raw), nil))
}
func (s *server) unseal(raw string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(raw)
	if e != nil || len(b) < s.encryption.NonceSize() {
		return "", errors.New("invalid encrypted value")
	}
	n := s.encryption.NonceSize()
	p, e := s.encryption.Open(nil, b[:n], b[n:], nil)
	return string(p), e
}
