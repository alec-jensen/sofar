package netguard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestS17_PublicIPRejectsInternalAddresses(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.9", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "198.18.0.5", "255.255.255.255"} {
		if PublicIP(netip.MustParseAddr(bad)) {
			t.Errorf("%s should not be treated as public", bad)
		}
	}
	for _, good := range []string{"8.8.8.8", "1.1.1.1", "104.16.0.1", "2606:4700::1111"} {
		if !PublicIP(netip.MustParseAddr(good)) {
			t.Errorf("%s should be public", good)
		}
	}
}

func TestS17_OutboundClientRefusesInternalHostsAndRedirects(t *testing.T) {
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer internal.Close()
	c := NewOutboundClient()
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

func TestS15_HostAllowlist(t *testing.T) {
	allowed := []string{"simplefin.org"}
	for host, want := range map[string]bool{
		"bridge.simplefin.org": true, "beta-bridge.simplefin.org": true, "simplefin.org": true, "SimpleFIN.org.": true,
		"evil.test": false, "simplefin.org.evil.test": false, "notsimplefin.org": false, "127.0.0.1": false, "169.254.169.254": false,
	} {
		if got := HostAllowed(allowed, host); got != want {
			t.Errorf("HostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func req(remote, forwarded string) *http.Request {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = remote
	if forwarded != "" {
		r.Header.Set("X-Forwarded-For", forwarded)
	}
	return r
}

func TestS19_19_ClientIPBehindTrustedProxy(t *testing.T) {
	direct := Proxies{}
	if got := direct.ClientIP(req("203.0.113.9:5000", "1.2.3.4")); got != "203.0.113.9" {
		t.Errorf("without TRUST_PROXY the header must be ignored, got %s", got)
	}
	proxied := Proxies{Trust: true}
	// the proxy appends the real caller last; anything before it is attacker-controlled
	if got := proxied.ClientIP(req("127.0.0.1:1", "6.6.6.6, 198.51.100.20")); got != "198.51.100.20" {
		t.Errorf("want the entry the proxy appended, got %s", got)
	}
	if got := proxied.ClientIP(req("127.0.0.1:1", "")); got != "127.0.0.1" {
		t.Errorf("no header falls back to the connection address, got %s", got)
	}
	if got := proxied.ClientIP(req("127.0.0.1:1", "garbage")); got != "127.0.0.1" {
		t.Errorf("an unparseable header falls back to the connection address, got %s", got)
	}
}

func TestS19_19_TrustedProxiesLimitWhoMayVouch(t *testing.T) {
	nets, err := ParseProxyNets("100.100.63.10, 10.0.0.0/8, fd00::/8")
	if err != nil {
		t.Fatal(err)
	}
	p := Proxies{Trust: true, Nets: nets}
	for remote, want := range map[string]bool{
		"100.100.63.10:4000": true, "10.9.8.7:1": true, "[fd12::1]:2": true,
		"100.100.63.11:4000": false, "203.0.113.5:80": false, "192.168.1.5:1": false,
	} {
		if got := p.Trusted(req(remote, "198.51.100.7")); got != want {
			t.Errorf("Trusted(%s) = %v, want %v", remote, got, want)
		}
	}
	if got := p.ClientIP(req("100.100.63.10:4000", "198.51.100.7")); got != "198.51.100.7" {
		t.Errorf("the trusted proxy's caller address should be used, got %s", got)
	}
	if got := p.ClientIP(req("203.0.113.5:80", "198.51.100.7")); got != "203.0.113.5" {
		t.Errorf("a direct caller must not be able to choose its own address, got %s", got)
	}
}

func TestParseProxyNetsRejectsGarbage(t *testing.T) {
	if _, err := ParseProxyNets("100.100.63.10, not-an-ip"); err == nil {
		t.Error("an invalid entry must be an error, not silently ignored")
	}
	if nets, err := ParseProxyNets(""); err != nil || len(nets) != 0 {
		t.Error("empty means no restriction")
	}
}

func TestIsLoopbackPeer(t *testing.T) {
	if !IsLoopbackPeer(req("127.0.0.1:5", "")) {
		t.Error("loopback peer without headers is loopback")
	}
	if IsLoopbackPeer(req("127.0.0.1:5", "1.2.3.4")) {
		t.Error("a proxied request is not a loopback peer")
	}
	if IsLoopbackPeer(req("203.0.113.5:5", "")) {
		t.Error("a remote peer is not loopback")
	}
}
