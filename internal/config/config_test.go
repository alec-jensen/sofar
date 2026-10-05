package config

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

var goodKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v + "\n"), nil
		}
		return nil, errors.New("no such file")
	}
}

func base() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://x", "SOFAR_ENCRYPTION_KEY": goodKey, "SOFAR_ORIGIN": "https://money.example.com"}
}

func TestConfig_FileVariantsWin(t *testing.T) {
	e := base()
	e["SOFAR_SETUP_KEY"] = "from-env"
	e["SOFAR_SETUP_KEY_FILE"] = "/run/secrets/setup"
	c, err := Load(envOf(e), files(map[string]string{"/run/secrets/setup": "from-file-with-trailing-newline"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SetupKey != "from-file-with-trailing-newline" {
		t.Errorf("file variant should win and be trimmed, got %q", c.SetupKey)
	}
	e["SOFAR_SETUP_KEY_FILE"] = "/missing"
	if _, err = Load(envOf(e), files(nil)); err == nil {
		t.Error("an unreadable secret file must be an error")
	}
}

func TestConfig_HomeTZAliases(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"home tz":  {map[string]string{"SOFAR_HOME_TZ": "America/Chicago", "TZ": "UTC"}, "America/Chicago"},
		"tz":       {map[string]string{"TZ": "America/Chicago"}, "America/Chicago"},
		"fallback": {nil, "UTC"},
	} {
		e := base()
		for k, v := range tc.env {
			e[k] = v
		}
		c, err := Load(envOf(e), files(nil))
		if err != nil || c.HomeTZ.String() != tc.want {
			t.Errorf("%s: got %v %v, want %s", name, c.HomeTZ, err, tc.want)
		}
	}
	e := base()
	e["TZ"] = "Mars/Olympus"
	if _, err := Load(envOf(e), files(nil)); err == nil {
		t.Error("an unknown zone must be an error")
	}
}

func TestConfig_ProductionRequiresHTTPSOrigin(t *testing.T) {
	e := base()
	e["SOFAR_ORIGIN"] = "http://money.example.com"
	if _, err := Load(envOf(e), files(nil)); err == nil {
		t.Error("production over http must be refused")
	}
	e["SOFAR_ENV"] = "development"
	if _, err := Load(envOf(e), files(nil)); err != nil {
		t.Errorf("development over http is fine: %v", err)
	}
	e["SOFAR_ORIGIN"] = "https://money.example.com/"
	if _, err := Load(envOf(e), files(nil)); err == nil {
		t.Error("a trailing slash must be refused")
	}
	e["SOFAR_ORIGIN"] = ""
	e["SOFAR_BASE_URL"] = "http://localhost:8080"
	if c, err := Load(envOf(e), files(nil)); err != nil || c.Origin != "http://localhost:8080" {
		t.Errorf("SOFAR_BASE_URL alias: %v %q", err, c.Origin)
	}
}

func TestConfig_RedactedNeverContainsSecrets(t *testing.T) {
	e := base()
	e["DATABASE_URL"] = "postgres://sofar:hunter2hunter2@db/sofar"
	e["SOFAR_SETUP_KEY"] = "setup-key-that-is-secret-1234"
	e["VAPID_PRIVATE_KEY"] = "vapid-private-secret"
	c, err := Load(envOf(e), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for k, v := range c.Redacted() {
		out.WriteString(k + "=" + v + "\n")
	}
	for _, secret := range []string{"hunter2hunter2", "setup-key-that-is-secret-1234", "vapid-private-secret", goodKey} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("redacted output leaks %q", secret)
		}
	}
	if !strings.Contains(out.String(), "SOFAR_SETUP_KEY=[set]") {
		t.Error("set secrets should show [set]")
	}
}

func TestConfig_RequiredAndKeyShape(t *testing.T) {
	e := base()
	e["SOFAR_ENCRYPTION_KEY"] = "short"
	if _, err := Load(envOf(e), files(nil)); err == nil {
		t.Error("a bad key must be refused")
	}
	e = base()
	delete(e, "DATABASE_URL")
	if _, err := Load(envOf(e), files(nil)); err == nil {
		t.Error("DATABASE_URL is required")
	}
}

func TestConfig_ProxyWarning(t *testing.T) {
	e := base()
	e["TRUST_PROXY"] = "true"
	c, _ := Load(envOf(e), files(nil))
	if len(c.Warnings()) != 1 {
		t.Error("trusting any proxy should warn")
	}
	e["TRUSTED_PROXIES"] = "100.100.63.10"
	c, _ = Load(envOf(e), files(nil))
	if len(c.Warnings()) != 0 {
		t.Error("a pinned proxy needs no warning")
	}
}
