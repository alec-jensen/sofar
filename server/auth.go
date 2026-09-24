package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"net"
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
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	for k, a := range s.attempts {
		if now.After(a.Until) {
			delete(s.attempts, k)
		}
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
func (s *server) newSession(w http.ResponseWriter, r *http.Request) error {
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
		fail(w, 401, "Check your username, password, and authenticator code.")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		fail(w, 401, "Check your username, password, and authenticator code.")
		return
	}
	if secret.Valid && secret.String != "" {
		plain, e := s.unseal(secret.String)
		step := time.Now().Unix() / 30
		valid, ve := totp.ValidateCustom(in.Code, plain, time.Now(), totp.ValidateOpts{Period: 30, Skew: 0, Digits: 6})
		if e != nil || ve != nil || !valid || step <= last {
			fail(w, 401, "Check your username, password, and authenticator code.")
			return
		}
		result, e := s.db.ExecContext(r.Context(), "UPDATE users SET totp_last_step=$1 WHERE id=1 AND totp_last_step<$1", step)
		if e != nil {
			fail(w, 500, "Could not verify code.")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 401, "Use a fresh authenticator code.")
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
	var in struct{ Code string }
	if decode(r, &in) != nil {
		fail(w, 400, "Invalid code.")
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
