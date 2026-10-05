//go:build integration

package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/crypt"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

const setupKey = "setup-key-of-at-least-24-chars"

func newService(t *testing.T) (*auth.Service, *clock) {
	t.Helper()
	box, err := crypt.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := auth.New(testkit.DB(t), box, setupKey)
	c := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	s.Now = c.now
	s.PasswordCost, s.CodeCost = 4, 4
	return s, c
}

func setup(t *testing.T, s *auth.Service) string {
	t.Helper()
	tok, err := s.Setup(context.Background(), "alec", "correct horse battery", setupKey, auth.Meta{UserAgent: "test", IP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestS17_SetupLoginLogout(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if _, err := s.Setup(ctx, "alec", "correct horse battery", "wrong-key-wrong-key-wrong-key", auth.Meta{}); !errors.Is(err, auth.ErrSetupKey) {
		t.Fatalf("wrong setup key: %v", err)
	}
	if _, err := s.Setup(ctx, "alec", "short", setupKey, auth.Meta{}); !errors.Is(err, auth.ErrShortPassword) {
		t.Fatalf("short password: %v", err)
	}
	tok := setup(t, s)
	if _, err := s.Setup(ctx, "other", "another long password", setupKey, auth.Meta{}); !errors.Is(err, auth.ErrAlreadySetUp) {
		t.Fatalf("second setup: %v", err)
	}
	if st, _ := s.Status(ctx, tok); !st.Authenticated || st.Setup {
		t.Fatalf("status after setup: %+v", st)
	}
	if _, err := s.Login(ctx, "alec", "wrong password here", "", auth.Meta{}); !errors.Is(err, auth.ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Login(ctx, "nobody", "correct horse battery", "", auth.Meta{}); !errors.Is(err, auth.ErrBadCredentials) {
		t.Fatalf("wrong username: %v", err)
	}
	tok2, err := s.Login(ctx, "alec", "correct horse battery", "", auth.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(ctx, tok2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Authenticate(ctx, tok2); ok {
		t.Error("a logged-out token must not authenticate")
	}
	if ok, _ := s.Authenticate(ctx, tok); !ok {
		t.Error("the other session must survive")
	}
}

func TestS17_SessionSlidesWithUse(t *testing.T) {
	s, c := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	// Use it every 20 days for 100 days: it never expires.
	for i := 0; i < 5; i++ {
		c.t = c.t.Add(20 * 24 * time.Hour)
		if ok, err := s.Authenticate(ctx, tok); err != nil || !ok {
			t.Fatalf("day %d: %v %v", (i+1)*20, ok, err)
		}
	}
}

func TestS17_SessionExpiresAfter30IdleDays(t *testing.T) {
	s, c := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	c.t = c.t.Add(29 * 24 * time.Hour)
	if ok, _ := s.Authenticate(ctx, tok); !ok {
		t.Fatal("29 idle days is still valid")
	}
	c.t = c.t.Add(31 * 24 * time.Hour)
	if ok, _ := s.Authenticate(ctx, tok); ok {
		t.Fatal("31 idle days must expire the session")
	}
}

func TestS17_LastSeenWritesAtMostHourly(t *testing.T) {
	s, c := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	var first time.Time
	read := func() time.Time {
		var ts time.Time
		if err := s.DB.QueryRowContext(ctx, `SELECT last_seen_at FROM sessions LIMIT 1`).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	first = read()
	c.t = c.t.Add(30 * time.Minute)
	s.Authenticate(ctx, tok)
	if !read().Equal(first) {
		t.Error("a use within the hour must not write")
	}
	c.t = c.t.Add(40 * time.Minute)
	s.Authenticate(ctx, tok)
	if read().Equal(first) {
		t.Error("a use after an hour must update last_seen")
	}
}

func enableTOTP(t *testing.T, s *auth.Service, tok string) (secret string, codes []string) {
	t.Helper()
	ctx := context.Background()
	secret, _, err := s.TOTPSetup(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, s.Now())
	codes, err = s.TOTPConfirm(ctx, tok, code, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

func TestS17_TOTPFlowAndReplayRefused(t *testing.T) {
	s, c := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	other, _ := s.Login(ctx, "alec", "correct horse battery", "", auth.Meta{})
	secret, codes := enableTOTP(t, s, tok)
	if len(codes) != auth.RecoveryCodeCount {
		t.Fatalf("want %d recovery codes, got %d", auth.RecoveryCodeCount, len(codes))
	}
	if ok, _ := s.Authenticate(ctx, other); ok {
		t.Error("enabling two-step signs out other sessions")
	}
	if _, err := s.Login(ctx, "alec", "correct horse battery", "", auth.Meta{}); !errors.Is(err, auth.ErrBadCredentials) {
		t.Fatalf("login without a code: %v", err)
	}
	c.t = c.t.Add(31 * time.Second)
	code, _ := totp.GenerateCode(secret, c.t)
	if _, err := s.Login(ctx, "alec", "correct horse battery", code, auth.Meta{}); err != nil {
		t.Fatalf("login with code: %v", err)
	}
	if _, err := s.Login(ctx, "alec", "correct horse battery", code, auth.Meta{}); err == nil {
		t.Fatal("a code must not be reusable")
	}
}

func TestS17_RecoveryCodeWorksOnce(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	_, codes := enableTOTP(t, s, tok)
	if _, err := s.Login(ctx, "alec", "correct horse battery", codes[0], auth.Meta{}); err != nil {
		t.Fatalf("recovery code should sign in: %v", err)
	}
	if _, err := s.Login(ctx, "alec", "correct horse battery", codes[0], auth.Meta{}); err == nil {
		t.Fatal("a recovery code must work only once")
	}
	if _, err := s.Login(ctx, "alec", "correct horse battery", "zzzzz-zzzzz", auth.Meta{}); err == nil {
		t.Fatal("a made-up recovery code must fail")
	}
	if n, _ := s.RecoveryCodesLeft(ctx); n != auth.RecoveryCodeCount-1 {
		t.Errorf("codes left = %d", n)
	}
	// Regeneration needs the password and replaces every code.
	if _, err := s.RegenerateRecoveryCodes(ctx, "wrong password!!"); !errors.Is(err, auth.ErrBadPassword) {
		t.Fatalf("regenerate with wrong password: %v", err)
	}
	fresh, err := s.RegenerateRecoveryCodes(ctx, "correct horse battery")
	if err != nil || len(fresh) != auth.RecoveryCodeCount {
		t.Fatalf("regenerate: %v", err)
	}
	if _, err := s.Login(ctx, "alec", "correct horse battery", codes[1], auth.Meta{}); err == nil {
		t.Fatal("old codes die when new ones are issued")
	}
}

func TestS17_RecoveryCodesRequireTOTP(t *testing.T) {
	s, _ := newService(t)
	setup(t, s)
	if _, err := s.RegenerateRecoveryCodes(context.Background(), "correct horse battery"); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("want ErrTOTPRequired, got %v", err)
	}
}

func TestS17_DisableTOTPDropsRecoveryCodes(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	secret, _ := enableTOTP(t, s, tok)
	code, _ := totp.GenerateCode(secret, s.Now())
	if err := s.TOTPDisable(ctx, "correct horse battery", code); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.RecoveryCodesLeft(ctx); n != 0 {
		t.Errorf("recovery codes should be gone, %d left", n)
	}
	if err := s.TOTPDisable(ctx, "correct horse battery", code); !errors.Is(err, auth.ErrTOTPAlreadyOff) {
		t.Errorf("second disable: %v", err)
	}
}

func TestS17_ChangePasswordSignsOutOthers(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	tok := setup(t, s)
	other, _ := s.Login(ctx, "alec", "correct horse battery", "", auth.Meta{})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO push_subscriptions (endpoint, user_id, keys) VALUES ('https://push.example/x', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, tok, "not the password", "a brand new password"); !errors.Is(err, auth.ErrBadPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := s.ChangePassword(ctx, tok, "correct horse battery", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Authenticate(ctx, other); ok {
		t.Error("other sessions must be signed out")
	}
	if ok, _ := s.Authenticate(ctx, tok); !ok {
		t.Error("the current session stays")
	}
	var n int
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n)
	if n != 0 {
		t.Error("push subscriptions are dropped with the other sessions")
	}
	if _, err := s.Login(ctx, "alec", "a brand new password", "", auth.Meta{}); err != nil {
		t.Errorf("new password should work: %v", err)
	}
}
