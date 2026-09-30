package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestPublicIPRejectsInternalAddresses(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.9", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "198.18.0.5", "255.255.255.255"} {
		if publicIP(netip.MustParseAddr(bad)) {
			t.Errorf("%s should not be treated as public", bad)
		}
	}
	for _, good := range []string{"8.8.8.8", "1.1.1.1", "104.16.0.1", "2606:4700::1111"} {
		if !publicIP(netip.MustParseAddr(good)) {
			t.Errorf("%s should be public", good)
		}
	}
}

func TestOutboundClientRefusesInternalHostsAndRedirects(t *testing.T) {
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer internal.Close()
	c := newOutboundClient()
	if resp, err := c.Get(internal.URL); err == nil || !strings.Contains(err.Error(), "blocked") {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("a loopback destination must be blocked, got %v", err)
	}
	if err := c.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("redirects must not be followed")
	}
}

func TestBridgeHostAllowlist(t *testing.T) {
	s := &server{bridgeHosts: bridgeHostsFromEnv()}
	for host, want := range map[string]bool{
		"bridge.simplefin.org": true, "beta-bridge.simplefin.org": true, "simplefin.org": true, "SimpleFIN.org.": true,
		"evil.test": false, "simplefin.org.evil.test": false, "notsimplefin.org": false, "127.0.0.1": false, "169.254.169.254": false,
	} {
		if got := s.bridgeHostOK(host); got != want {
			t.Errorf("bridgeHostOK(%q) = %v, want %v", host, got, want)
		}
	}
	t.Setenv("SIMPLEFIN_ALLOWED_HOSTS", "bridge.example.com, sfin.home.test")
	custom := &server{bridgeHosts: bridgeHostsFromEnv()}
	if !custom.bridgeHostOK("sfin.home.test") || custom.bridgeHostOK("bridge.simplefin.org") {
		t.Error("a configured host list must replace the default")
	}
}

func TestClientIPBehindTrustedProxy(t *testing.T) {
	req := func(remote, forwarded string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	direct := &server{}
	if got := direct.clientIP(req("203.0.113.9:5000", "1.2.3.4")); got != "203.0.113.9" {
		t.Errorf("without TRUST_PROXY the header must be ignored, got %s", got)
	}
	proxied := &server{trustProxy: true}
	// the proxy appends the real caller last; anything before it is attacker-controlled
	if got := proxied.clientIP(req("127.0.0.1:1", "6.6.6.6, 198.51.100.20")); got != "198.51.100.20" {
		t.Errorf("want the entry the proxy appended, got %s", got)
	}
	if got := proxied.clientIP(req("127.0.0.1:1", "")); got != "127.0.0.1" {
		t.Errorf("no header falls back to the connection address, got %s", got)
	}
	if got := proxied.clientIP(req("127.0.0.1:1", "garbage")); got != "127.0.0.1" {
		t.Errorf("an unparseable header falls back to the connection address, got %s", got)
	}
}

func TestRateLimitIsPerCallerNotPerProxy(t *testing.T) {
	s := &server{trustProxy: true, attempts: map[string]attempt{}}
	from := func(ip string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "127.0.0.1:9"
		r.Header.Set("X-Forwarded-For", ip)
		return r
	}
	for i := 0; i < 10; i++ {
		if !s.limit(from("198.51.100.1")) {
			t.Fatalf("attempt %d from the attacker should still be allowed", i+1)
		}
	}
	if s.limit(from("198.51.100.1")) {
		t.Fatal("the attacker should be locked out after ten failures")
	}
	if !s.limit(from("198.51.100.2")) {
		t.Fatal("the owner on another address must not be locked out by the attacker")
	}
	s.succeeded(from("198.51.100.1"))
	if !s.limit(from("198.51.100.1")) {
		t.Fatal("a successful login clears that caller's failures")
	}
}

func TestTrustedProxiesLimitWhoMayVouchForHTTPSAndAddress(t *testing.T) {
	nets, err := parseProxyNets("100.100.63.10, 10.0.0.0/8, fd00::/8")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{origin: "https://money.example.com", production: true, trustProxy: true, proxyNets: nets}
	req := func(remote string) *http.Request {
		r := httptest.NewRequest("GET", "/api/nope", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "198.51.100.7")
		return r
	}
	for remote, wantTrusted := range map[string]bool{
		"100.100.63.10:4000": true, "10.9.8.7:1": true, "[fd12::1]:2": true,
		"100.100.63.11:4000": false, "203.0.113.5:80": false, "192.168.1.5:1": false,
	} {
		if got := s.proxyTrusted(req(remote)); got != wantTrusted {
			t.Errorf("proxyTrusted(%s) = %v, want %v", remote, got, wantTrusted)
		}
	}
	// A trusted proxy can vouch for HTTPS and supply the caller's address...
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req("100.100.63.10:4000"))
	if w.Code != 404 {
		t.Fatalf("a request via the trusted proxy should reach the app, got %d", w.Code)
	}
	if got := s.clientIP(req("100.100.63.10:4000")); got != "198.51.100.7" {
		t.Errorf("the trusted proxy's caller address should be used, got %s", got)
	}
	// ...but anyone else who reaches the port directly cannot.
	w = httptest.NewRecorder()
	s.routes().ServeHTTP(w, req("203.0.113.5:80"))
	if w.Code != 400 {
		t.Fatalf("a direct connection claiming HTTPS must be refused, got %d", w.Code)
	}
	if got := s.clientIP(req("203.0.113.5:80")); got != "203.0.113.5" {
		t.Errorf("a direct caller must not be able to choose its own address, got %s", got)
	}
}

func TestParseProxyNetsRejectsGarbage(t *testing.T) {
	if _, err := parseProxyNets("100.100.63.10, not-an-ip"); err == nil {
		t.Error("an invalid entry must be an error, not silently ignored")
	}
	if nets, err := parseProxyNets(""); err != nil || len(nets) != 0 {
		t.Error("empty means no restriction")
	}
}
