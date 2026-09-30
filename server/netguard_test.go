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
