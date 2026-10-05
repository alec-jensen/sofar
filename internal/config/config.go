// Package config reads sofar's settings from the environment. Every secret
// also accepts a NAME_FILE variable pointing at a file that holds the value.
// spec: ADR-0016.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/alec-jensen/sofar/internal/netguard"
)

// Config is the validated runtime configuration.
type Config struct {
	DatabaseURL    string
	EncryptionKey  []byte
	SetupKey       string
	Origin         string
	Bind           string
	Production     bool
	TrustProxy     bool
	TrustedProxies []netip.Prefix
	HomeTZ         *time.Location
	HomeTZName     string
	VAPIDPublic    string
	VAPIDPrivate   string
	VAPIDSubject   string
	BridgeHosts    []string
	StaticDir      string
	LogLevel       string

	secrets map[string]bool // which secrets were set, for Redacted
}

// Load builds a Config. env and readFile are injected so tests need no real
// environment or filesystem.
func Load(env func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	var c Config
	c.secrets = map[string]bool{}

	// value returns NAME_FILE's contents when set (file wins), else NAME.
	value := func(name string, aliases ...string) (string, error) {
		for _, n := range append([]string{name}, aliases...) {
			if path := strings.TrimSpace(env(n + "_FILE")); path != "" {
				b, err := readFile(path)
				if err != nil {
					return "", fmt.Errorf("%s_FILE: %w", n, err)
				}
				return strings.TrimSpace(string(b)), nil
			}
			if v := strings.TrimSpace(env(n)); v != "" {
				return v, nil
			}
		}
		return "", nil
	}
	secret := func(name string) (string, error) {
		v, err := value(name)
		c.secrets[name] = v != ""
		return v, err
	}
	var err error

	if c.DatabaseURL, err = secret("DATABASE_URL"); err != nil {
		return c, err
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}

	rawKey, err := secret("SOFAR_ENCRYPTION_KEY")
	if err != nil {
		return c, err
	}
	if c.EncryptionKey, err = base64.StdEncoding.DecodeString(rawKey); err != nil || len(c.EncryptionKey) != 32 {
		return c, errors.New("SOFAR_ENCRYPTION_KEY must be 32 random bytes in base64")
	}

	if c.SetupKey, err = secret("SOFAR_SETUP_KEY"); err != nil {
		return c, err
	}
	if c.VAPIDPrivate, err = secret("VAPID_PRIVATE_KEY"); err != nil {
		return c, err
	}
	if c.VAPIDPublic, err = value("VAPID_PUBLIC_KEY"); err != nil {
		return c, err
	}
	c.VAPIDSubject = orDefault(env("VAPID_SUBJECT"), "")

	origin, err := value("SOFAR_ORIGIN", "SOFAR_BASE_URL")
	if err != nil {
		return c, err
	}
	c.Origin = orDefault(origin, "http://localhost:8080")
	u, perr := url.Parse(c.Origin)
	if perr != nil || u.Host == "" || u.Path != "" {
		return c, errors.New("SOFAR_ORIGIN must be an origin without a trailing slash")
	}
	c.Production = orDefault(env("SOFAR_ENV"), "production") == "production"
	if c.Production && u.Scheme != "https" {
		return c, errors.New("production requires an HTTPS SOFAR_ORIGIN")
	}

	// SOFAR_ADDR is the listen address inside the process. SOFAR_BIND is only
	// compose's host-side publish address (see compose.yaml) and is not read here.
	c.Bind = orDefault(env("SOFAR_ADDR"), "127.0.0.1:8080")
	c.TrustProxy = env("TRUST_PROXY") == "true"
	if c.TrustedProxies, err = netguard.ParseProxyNets(env("TRUSTED_PROXIES")); err != nil {
		return c, err
	}

	tz, err := value("SOFAR_HOME_TZ", "TZ")
	if err != nil {
		return c, err
	}
	c.HomeTZName = orDefault(tz, "UTC")
	if c.HomeTZ, err = time.LoadLocation(c.HomeTZName); err != nil {
		return c, fmt.Errorf("unknown time zone %q", c.HomeTZName)
	}

	c.BridgeHosts = []string{"simplefin.org"}
	if raw := strings.TrimSpace(env("SIMPLEFIN_ALLOWED_HOSTS")); raw != "" {
		c.BridgeHosts = nil
		for _, h := range strings.Split(raw, ",") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				c.BridgeHosts = append(c.BridgeHosts, h)
			}
		}
	}
	c.StaticDir = orDefault(env("SOFAR_STATIC_DIR"), "dist")
	c.LogLevel = orDefault(env("SOFAR_LOG_LEVEL"), "info")
	return c, nil
}

// Warnings lists things worth telling the owner at startup.
func (c Config) Warnings() []string {
	var w []string
	if c.TrustProxy && len(c.TrustedProxies) == 0 {
		w = append(w, "TRUST_PROXY is on without TRUSTED_PROXIES: any client that can reach this port can claim to be the proxy. Set TRUSTED_PROXIES to your proxy's address, or keep the port on loopback.")
	}
	return w
}

// Redacted is safe to print: every secret shows only whether it is set.
func (c Config) Redacted() map[string]string {
	state := func(name string) string {
		if c.secrets[name] {
			return "[set]"
		}
		return "[unset]"
	}
	return map[string]string{
		"DATABASE_URL":         state("DATABASE_URL"),
		"SOFAR_ENCRYPTION_KEY": state("SOFAR_ENCRYPTION_KEY"),
		"SOFAR_SETUP_KEY":      state("SOFAR_SETUP_KEY"),
		"VAPID_PRIVATE_KEY":    state("VAPID_PRIVATE_KEY"),
		"SOFAR_ORIGIN":         c.Origin,
		"SOFAR_ADDR":           c.Bind,
		"SOFAR_HOME_TZ":        c.HomeTZName,
		"TRUST_PROXY":          fmt.Sprint(c.TrustProxy),
		"SOFAR_ENV":            map[bool]string{true: "production", false: "development"}[c.Production],
	}
}

// Proxies is the forwarding-header policy derived from the config.
func (c Config) Proxies() netguard.Proxies {
	return netguard.Proxies{Trust: c.TrustProxy, Nets: c.TrustedProxies}
}

func orDefault(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return fallback
}
