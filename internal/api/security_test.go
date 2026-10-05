package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/config"
	"github.com/alec-jensen/sofar/internal/netguard"
)

func handler(cfg config.Config) http.Handler {
	return New(Deps{Cfg: cfg, Log: zerolog.Nop()})
}

func prod() config.Config {
	return config.Config{Origin: "https://money.example.com", Production: true, StaticDir: "testdata-missing"}
}

func TestS17_CSRFAndHTTPSRejectBeforeHandler(t *testing.T) {
	h := handler(prod())
	for name, tc := range map[string]struct {
		origin string
		https  bool
		want   int
	}{
		"foreign origin":     {"https://evil.test", true, 403},
		"plain http":         {"https://money.example.com", false, 400},
		"missing origin":     {"", true, 403},
		"good origin, https": {"https://money.example.com", true, 404}, // reaches the router: unknown endpoint
	} {
		target := "http://money.example.com/api/v1/nope"
		if tc.https {
			target = "https://money.example.com/api/v1/nope"
		}
		r := httptest.NewRequest("POST", target, strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d", name, w.Code, tc.want)
		}
	}
}

func TestS17_JSONRequiredForWrites(t *testing.T) {
	h := handler(config.Config{Origin: "http://localhost:8080", StaticDir: "x"})
	r := httptest.NewRequest("POST", "/api/v1/nope", strings.NewReader("a=b"))
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Errorf("got %d want 415", w.Code)
	}
}

func TestS19_19_TrustedProxiesLimitWhoMayVouchForHTTPS(t *testing.T) {
	nets, err := netguard.ParseProxyNets("100.100.63.10, 10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := prod()
	cfg.TrustProxy, cfg.TrustedProxies = true, nets
	h := handler(cfg)
	req := func(remote string) *http.Request {
		r := httptest.NewRequest("GET", "/api/v1/nope", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("100.100.63.10:4000"))
	if w.Code != 404 {
		t.Fatalf("a request via the trusted proxy should reach the app, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("203.0.113.5:80"))
	if w.Code != 400 {
		t.Fatalf("a direct connection claiming HTTPS must be refused, got %d", w.Code)
	}
}

func TestS19_18_HealthChecksWorkOverPlainHTTPOnLoopbackOnly(t *testing.T) {
	h := handler(prod())
	for remote, want := range map[string]int{"127.0.0.1:5000": 200, "203.0.113.5:80": 400} {
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: got %d want %d", remote, w.Code, want)
		}
	}
}

func TestS17_SecurityHeadersAndNoStoreOnAPI(t *testing.T) {
	h := handler(config.Config{Origin: "http://localhost:8080", StaticDir: "x"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/nope", nil))
	for header, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store"} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("CSP missing frame-ancestors")
	}
	if !strings.Contains(w.Body.String(), `"code":"not_found"`) {
		t.Errorf("unknown endpoints answer with the standard error shape: %s", w.Body.String())
	}
}
