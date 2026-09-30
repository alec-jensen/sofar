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

// clientIP is the address used for rate limiting. Behind a trusted reverse
// proxy every connection arrives from the proxy, so the caller's address is the
// last X-Forwarded-For entry (the one the proxy itself appended). This assumes
// the proxy is the outermost hop; without TRUST_PROXY the header is ignored.
func (s *server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.trustProxy {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if ip, e := netip.ParseAddr(strings.TrimSpace(parts[i])); e == nil {
				return ip.Unmap().String()
			}
		}
	}
	return host
}
