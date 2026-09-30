package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"os"
	"strings"
	"time"
)

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *server) authenticated(r *http.Request) bool {
	c, e := r.Cookie("sofar_session")
	if e != nil {
		return false
	}
	var n int
	e = s.db.QueryRowContext(r.Context(), "SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>now()", hashToken(c.Value)).Scan(&n)
	return e == nil && n == 1
}
func (s *server) protect(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			fail(w, 401, "Please sign in to continue.")
			return
		}
		h(w, r)
	})
}
func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	var n int
	if e := s.db.QueryRowContext(r.Context(), "SELECT count(*) FROM users").Scan(&n); e != nil {
		fail(w, 503, "Database unavailable.")
		return
	}
	authenticated := s.authenticated(r)
	totpEnabled := false
	if authenticated {
		if e := s.db.QueryRowContext(r.Context(), "SELECT totp_secret IS NOT NULL FROM users WHERE id=1").Scan(&totpEnabled); e != nil {
			fail(w, 503, "Database unavailable.")
			return
		}
	}
	respond(w, map[string]bool{"authenticated": authenticated, "setup": n == 0, "totpEnabled": totpEnabled})
}
func (s *server) limit(r *http.Request) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	host := s.clientIP(r)
	now := time.Now()
	for k, a := range s.attempts {
		if now.After(a.Until) {
			delete(s.attempts, k)
		}
	}
	if len(s.attempts) > 20000 {
		// Bound memory under a flood of distinct addresses; the per-address windows are short anyway.
		s.attempts = map[string]attempt{}
	}
	a := s.attempts[host]
	if a.Count >= 10 {
		return false
	}
	if a.Count == 0 {
		a.Until = now.Add(15 * time.Minute)
	}
	a.Count++
	s.attempts[host] = a
	return true
}

// succeeded forgets failed attempts from this address, so only failures count toward the limit.
func (s *server) succeeded(r *http.Request) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	delete(s.attempts, s.clientIP(r))
}

// loginFailed answers a failed credential check. Once many failures pile up
// across all addresses, failures (not correct logins) are slowed down, so a
// distributed guesser is throttled without ever locking the owner out.
func (s *server) loginFailed(w http.ResponseWriter, message string) {
	s.authMu.Lock()
	now := time.Now()
	if now.After(s.globalUntil) {
		s.globalFails = 0
		s.globalUntil = now.Add(15 * time.Minute)
	}
	s.globalFails++
	over := s.globalFails - 20
	s.authMu.Unlock()
	if over > 0 {
		delay := time.Duration(over) * 250 * time.Millisecond
		if delay > 3*time.Second {
			delay = 3 * time.Second
		}
		time.Sleep(delay)
	}
	fail(w, 401, message)
}

// confirmPassword checks the owner's password for actions that must not be
// possible with a session alone. It writes the error response itself.
func (s *server) confirmPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	var hash string
	if len(password) > 72 || s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE id=1").Scan(&hash) != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.loginFailed(w, "Your password did not match.")
		return false
	}
	return true
}
func (s *server) newSession(w http.ResponseWriter, r *http.Request) error {
	s.succeeded(r)
	token := randomID()
	_, e := s.db.ExecContext(r.Context(), "INSERT INTO sessions VALUES($1,1,$2)", hashToken(token), time.Now().Add(7*24*time.Hour))
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: "sofar_session", Value: token, Path: "/", HttpOnly: true, Secure: s.production, SameSite: http.SameSiteStrictMode, MaxAge: 7 * 86400})
	return nil
}
func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var in struct{ Username, Password, SetupKey string }
	if decode(r, &in) != nil {
		fail(w, 400, "Invalid account details.")
		return
	}
	expected := os.Getenv("SOFAR_SETUP_KEY")
	if len(expected) < 24 || subtle.ConstantTimeCompare([]byte(in.SetupKey), []byte(expected)) != 1 {
		fail(w, 403, "The setup key does not match.")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if len(in.Username) < 1 || len(in.Username) > 80 || len(in.Password) < 12 || len(in.Password) > 72 {
		fail(w, 400, "Use a username and a password of 12–72 bytes.")
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if e != nil {
		fail(w, 500, "Could not secure password.")
		return
	}
	_, e = s.db.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash) VALUES(1,$1,$2)", in.Username, string(hash))
	if e != nil {
		fail(w, 409, "This instance already has an account.")
		return
	}
	if s.newSession(w, r) != nil {
		fail(w, 500, "Could not start a session.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var in struct{ Username, Password, Code string }
	if decode(r, &in) != nil || len(in.Password) > 72 {
		fail(w, 400, "Invalid login details.")
		return
	}
	var hash string
	var secret sql.NullString
	var last int64
	e := s.db.QueryRowContext(r.Context(), "SELECT password_hash,totp_secret,totp_last_step FROM users WHERE id=1 AND username=$1", in.Username).Scan(&hash, &secret, &last)
	if e != nil {
		bcrypt.CompareHashAndPassword([]byte("$2a$12$QERZC3cr1kjMRovx5EcaQee6Pf08FgvYsUzKHrjGAOjfxGBKaGlR."), []byte(in.Password))
		s.loginFailed(w, "Check your username, password, and authenticator code.")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		s.loginFailed(w, "Check your username, password, and authenticator code.")
		return
	}
	if secret.Valid && secret.String != "" {
		plain, e := s.unseal(secret.String)
		step := time.Now().Unix() / 30
		valid, ve := totp.ValidateCustom(in.Code, plain, time.Now(), totp.ValidateOpts{Period: 30, Skew: 0, Digits: 6})
		if e != nil || ve != nil || !valid || step <= last {
			s.loginFailed(w, "Check your username, password, and authenticator code.")
			return
		}
		result, e := s.db.ExecContext(r.Context(), "UPDATE users SET totp_last_step=$1 WHERE id=1 AND totp_last_step<$1", step)
		if e != nil {
			fail(w, 500, "Could not verify code.")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			s.loginFailed(w, "Use a fresh authenticator code.")
			return
		}
	}
	if s.newSession(w, r) != nil {
		fail(w, 500, "Could not start a session.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("sofar_session")
	if _, e := s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", hashToken(c.Value)); e != nil {
		fail(w, 500, "Could not sign out.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sofar_session", Value: "", Path: "/", HttpOnly: true, Secure: s.production, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	respond(w, map[string]bool{"ok": true})
}
func (s *server) totpSetup(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var in struct{ Password string }
	if decode(r, &in) != nil {
		fail(w, 400, "Enter your password to continue.")
		return
	}
	if !s.confirmPassword(w, r, in.Password) {
		return
	}
	s.succeeded(r)
	var username string
	var existing sql.NullString
	if e := s.db.QueryRowContext(r.Context(), "SELECT username,totp_secret FROM users WHERE id=1").Scan(&username, &existing); e != nil {
		fail(w, 500, "Could not load account.")
		return
	}
	if existing.Valid && existing.String != "" {
		fail(w, 409, "Two-step verification is already enabled.")
		return
	}
	key, e := totp.Generate(totp.GenerateOpts{Issuer: "sofar", AccountName: username})
	if e != nil {
		fail(w, 500, "Could not create authenticator secret.")
		return
	}
	if _, e = s.db.ExecContext(r.Context(), "UPDATE users SET totp_pending=$1 WHERE id=1", s.seal(key.Secret())); e != nil {
		fail(w, 500, "Could not save enrollment.")
		return
	}
	respond(w, map[string]string{"secret": key.Secret(), "url": key.URL()})
}
func (s *server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again later.")
		return
	}
	var in struct{ Code, Password string }
	if decode(r, &in) != nil {
		fail(w, 400, "Invalid code.")
		return
	}
	if !s.confirmPassword(w, r, in.Password) {
		return
	}
	var encrypted string
	if s.db.QueryRowContext(r.Context(), "SELECT totp_pending FROM users WHERE id=1 AND totp_secret IS NULL").Scan(&encrypted) != nil {
		fail(w, 400, "Start authenticator enrollment first.")
		return
	}
	secret, e := s.unseal(encrypted)
	if e != nil || !totp.Validate(in.Code, secret) {
		fail(w, 400, "That code did not match. Try a fresh code.")
		return
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		fail(w, 500, "Could not save enrollment.")
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(r.Context(), "UPDATE users SET totp_secret=totp_pending,totp_pending=NULL,totp_last_step=$1 WHERE id=1", time.Now().Unix()/30); e == nil {
		c, _ := r.Cookie("sofar_session")
		_, e = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash<>$1", hashToken(c.Value))
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "Could not save enrollment.")
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var in struct{ Current, Next string }
	if decode(r, &in) != nil || len(in.Current) > 72 {
		fail(w, 400, "Invalid password details.")
		return
	}
	if len(in.Next) < 12 || len(in.Next) > 72 {
		fail(w, 400, "Use a new password of 12–72 bytes.")
		return
	}
	var hash string
	if e := s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE id=1").Scan(&hash); e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Current)) != nil {
		s.loginFailed(w, "Your current password did not match.")
		return
	}
	next, e := bcrypt.GenerateFromPassword([]byte(in.Next), 12)
	if e != nil {
		fail(w, 500, "Could not secure password.")
		return
	}
	c, _ := r.Cookie("sofar_session")
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(r.Context(), "UPDATE users SET password_hash=$1 WHERE id=1", string(next)); e == nil {
		_, e = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash<>$1", hashToken(c.Value))
	}
	if e == nil {
		// Other devices are signed out, so their notification subscriptions go too.
		_, e = tx.ExecContext(r.Context(), "DELETE FROM push_subscriptions")
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	s.succeeded(r)
	respond(w, map[string]bool{"ok": true})
}
func (s *server) totpDisable(w http.ResponseWriter, r *http.Request) {
	if !s.limit(r) {
		fail(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var in struct{ Password, Code string }
	if decode(r, &in) != nil || len(in.Password) > 72 {
		fail(w, 400, "Invalid details.")
		return
	}
	var hash string
	var secret sql.NullString
	if e := s.db.QueryRowContext(r.Context(), "SELECT password_hash,totp_secret FROM users WHERE id=1").Scan(&hash, &secret); e != nil {
		fail(w, 500, "Could not load account.")
		return
	}
	if !secret.Valid || secret.String == "" {
		fail(w, 409, "Two-step verification is already off.")
		return
	}
	plain, e := s.unseal(secret.String)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || e != nil || !totp.Validate(in.Code, plain) {
		s.loginFailed(w, "Check your password and authenticator code.")
		return
	}
	if _, e = s.db.ExecContext(r.Context(), "UPDATE users SET totp_secret=NULL,totp_pending=NULL,totp_last_step=0 WHERE id=1"); e != nil {
		fail(w, 500, "Could not turn off two-step verification.")
		return
	}
	s.succeeded(r)
	respond(w, map[string]bool{"ok": true})
}
