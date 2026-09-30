package main

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

var errBlockedAddress = errors.New("connection to a non-public address was blocked")

var reservedRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, cidr := range []string{
		"0.0.0.0/8",       // "this" network
		"100.64.0.0/10",   // carrier-grade NAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved
		"64:ff9b::/96",    // NAT64
		"2001:db8::/32",   // documentation
	} {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}()

// publicIP reports whether an address is on the public internet. Loopback,
// private, link-local (including cloud metadata addresses), multicast, and
// reserved ranges are not.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range reservedRanges {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// newOutboundClient is used for every request sofar makes to the outside
// world (SimpleFIN and push providers). It refuses to connect to non-public
// addresses at dial time, which also defeats DNS rebinding, and it never
// follows redirects.
func newOutboundClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errBlockedAddress
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !publicIP(ip) {
				return errBlockedAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       60 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// bridgeHostsFromEnv lists the domains a SimpleFIN setup token or access URL may
// point at: simplefin.org and its subdomains, unless SIMPLEFIN_ALLOWED_HOSTS
// names a self-hosted bridge.
func bridgeHostsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("SIMPLEFIN_ALLOWED_HOSTS"))
	if raw == "" {
		return []string{"simplefin.org"}
	}
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func (s *server) bridgeHostOK(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range s.bridgeHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// parseProxyNets reads TRUSTED_PROXIES: a comma-separated list of addresses or
// CIDR ranges (for example "100.100.63.10" or "100.64.0.0/10").
func parseProxyNets(raw string) ([]netip.Prefix, error) {
	var nets []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			nets = append(nets, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(part)
		if err != nil {
			return nil, errors.New("TRUSTED_PROXIES has an entry that is not an address or CIDR range: " + part)
		}
		nets = append(nets, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
	}
	return nets, nil
}

// proxyTrusted reports whether forwarding headers (X-Forwarded-For and
// X-Forwarded-Proto) may be believed for this request. That needs TRUST_PROXY,
// and, when TRUSTED_PROXIES is set, the connection must come from one of those
// addresses. Otherwise anyone who can reach the port could claim to be the proxy.
func (s *server) proxyTrusted(r *http.Request) bool {
	if !s.trustProxy {
		return false
	}
	if len(s.proxyNets) == 0 {
		return true
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	for _, n := range s.proxyNets {
		if n.Contains(peer.Addr().Unmap()) {
			return true
		}
	}
	return false
}

// clientIP is the address used for rate limiting. Behind a trusted reverse
// proxy every connection arrives from the proxy, so the caller's address is the
// last X-Forwarded-For entry (the one the proxy itself appended). This assumes
// the proxy is the outermost hop; without TRUST_PROXY the header is ignored.
func (s *server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.proxyTrusted(r) {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if ip, e := netip.ParseAddr(strings.TrimSpace(parts[i])); e == nil {
				return ip.Unmap().String()
			}
		}
	}
	return host
}
