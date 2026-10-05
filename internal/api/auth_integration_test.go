//go:build integration

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog"

	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/config"
	"github.com/alec-jensen/sofar/internal/crypt"
	"github.com/alec-jensen/sofar/internal/testkit"
)

const origin = "http://sofar.test"

type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
}

func (c *client) do(method, path, body string) (int, map[string]any, *http.Response) {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "198.51.100.7:1234"
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	res := w.Result()
	for _, ck := range res.Cookies() {
		if ck.Name == cookieName {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return res.StatusCode, out, res
}

func newClient(t *testing.T) *client {
	t.Helper()
	box, _ := crypt.New(make([]byte, 32))
	svc := auth.New(testkit.DB(t), box, "setup-key-of-at-least-24-chars")
	svc.PasswordCost, svc.CodeCost = 4, 4
	cfg := config.Config{Origin: origin, StaticDir: "none"}
	h := New(Deps{Cfg: cfg, DB: svc.DB, Auth: svc, Log: zerolog.Nop()})
	return &client{t: t, h: h}
}

func TestS17_AuthFlowOverHTTP(t *testing.T) {
	c := newClient(t)
	if code, body, _ := c.do("GET", "/api/v1/auth/status", ""); code != 200 || body["setup"] != true || body["authenticated"] != false {
		t.Fatalf("fresh status: %d %v", code, body)
	}
	if code, _, _ := c.do("GET", "/api/v1/push/devices", ""); code != 401 {
		t.Fatalf("protected endpoint without a session: %d", code)
	}
	if code, _, _ := c.do("POST", "/api/v1/auth/setup", `{"username":"alec","password":"correct horse battery","setupKey":"nope"}`); code != 403 {
		t.Fatalf("wrong setup key: %d", code)
	}
	code, _, res := c.do("POST", "/api/v1/auth/setup", `{"username":"alec","password":"correct horse battery","setupKey":"setup-key-of-at-least-24-chars"}`)
	if code != 200 || c.cookie == nil {
		t.Fatalf("setup: %d", code)
	}
	if ck := res.Cookies()[0]; !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.MaxAge != 30*86400 {
		t.Errorf("cookie flags: %+v", ck)
	}
	if code, body, _ := c.do("GET", "/api/v1/push/devices", ""); code != 200 || body["count"] != float64(0) {
		t.Fatalf("devices: %d %v", code, body)
	}

	// two-step enrollment returns recovery codes once
	code, body, _ := c.do("POST", "/api/v1/auth/totp/setup", `{"password":"correct horse battery"}`)
	if code != 200 {
		t.Fatalf("totp setup: %d %v", code, body)
	}
	code1, _ := totp.GenerateCode(body["secret"].(string), time.Now())
	code, body, _ = c.do("POST", "/api/v1/auth/totp/confirm", `{"code":"`+code1+`","password":"correct horse battery"}`)
	codes, _ := body["recoveryCodes"].([]any)
	if code != 200 || len(codes) != auth.RecoveryCodeCount {
		t.Fatalf("totp confirm: %d %v", code, body)
	}

	c.do("POST", "/api/v1/auth/logout", `{}`)
	if c.cookie != nil {
		t.Fatal("logout should clear the cookie")
	}
	if code, _, _ := c.do("POST", "/api/v1/auth/login", `{"username":"alec","password":"correct horse battery"}`); code != 401 {
		t.Fatalf("login without a code: %d", code)
	}
	if code, _, _ := c.do("POST", "/api/v1/auth/login", `{"username":"alec","password":"correct horse battery","code":"`+codes[0].(string)+`"}`); code != 200 {
		t.Fatalf("login with a recovery code: %d", code)
	}
}

func TestS19_19_LoginRateLimitReturns429(t *testing.T) {
	c := newClient(t)
	for i := 0; i < 10; i++ {
		if code, _, _ := c.do("POST", "/api/v1/auth/login", `{"username":"a","password":"b"}`); code != 401 {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	if code, body, _ := c.do("POST", "/api/v1/auth/login", `{"username":"a","password":"b"}`); code != 429 {
		t.Fatalf("11th attempt: %d %v", code, body)
	}
}
