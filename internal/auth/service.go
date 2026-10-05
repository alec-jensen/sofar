// Package auth is the single-owner account: setup, login, sliding sessions,
// two-step verification and recovery codes. spec: S17, S19.19.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/alec-jensen/sofar/internal/crypt"
)

const (
	// SessionLife is how long a session lasts after its last use (S17).
	SessionLife = 30 * 24 * time.Hour
	// touchEvery limits last_seen writes to once an hour per session.
	touchEvery = time.Hour
	// RecoveryCodeCount is how many codes are issued with two-step verification.
	RecoveryCodeCount = 8

	maxPasswordBytes = 72 // bcrypt's limit
	minPasswordBytes = 12
)

// Error is a failure the caller may show to the owner. Code is stable.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

var (
	ErrInvalidInput       = &Error{"invalid_input", "Invalid details."}
	ErrSetupKey           = &Error{"setup_key", "The setup key does not match."}
	ErrAlreadySetUp       = &Error{"already_set_up", "This instance already has an account."}
	ErrBadCredentials     = &Error{"bad_credentials", "Check your username, password, and authenticator code."}
	ErrBadPassword        = &Error{"bad_password", "Your password did not match."}
	ErrStaleCode          = &Error{"stale_code", "Use a fresh authenticator code."}
	ErrTOTPAlreadyOn      = &Error{"totp_already_on", "Two-step verification is already enabled."}
	ErrTOTPAlreadyOff     = &Error{"totp_already_off", "Two-step verification is already off."}
	ErrTOTPNotStarted     = &Error{"totp_not_started", "Start authenticator enrollment first."}
	ErrBadCode            = &Error{"bad_code", "That code did not match. Try a fresh code."}
	ErrTOTPRequired       = &Error{"totp_required", "Turn on two-step verification first."}
	ErrShortPassword      = &Error{"short_password", "Use a password of 12–72 bytes."}
	ErrUnauthenticated    = &Error{"unauthenticated", "Please sign in to continue."}
	errInternalUnexpected = &Error{"internal", "Something went wrong."}
)

// Meta is what we remember about where a session started.
type Meta struct{ UserAgent, IP string }

// State is what the login screen needs to know.
type State struct{ Setup, Authenticated, TOTPEnabled bool }

// Service holds the account logic. It never touches HTTP.
type Service struct {
	DB       *sql.DB
	Box      *crypt.Box
	SetupKey string
	Now      func() time.Time
	// PasswordCost is the bcrypt cost for passwords; tests lower it.
	PasswordCost int
	// CodeCost is the bcrypt cost for recovery codes.
	CodeCost int
}

// New builds a Service with production costs.
func New(db *sql.DB, box *crypt.Box, setupKey string) *Service {
	return &Service{DB: db, Box: box, SetupKey: setupKey, Now: time.Now, PasswordCost: 12, CodeCost: 10}
}

func (s *Service) hash(password string, cost int) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(h), err
}

// dummyHash makes unknown-username logins cost the same as real ones.
var dummyHash = "$2a$12$QERZC3cr1kjMRovx5EcaQee6Pf08FgvYsUzKHrjGAOjfxGBKaGlR."

func (s *Service) newSession(ctx context.Context, m Meta) (string, error) {
	token := crypt.RandomToken()
	now := s.Now()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at, user_agent, ip_hint) VALUES ($1,1,$2,$2,$3,$4,$5)`,
		crypt.HashToken(token), now, now.Add(SessionLife), trunc(m.UserAgent, 200), trunc(m.IP, 64))
	return token, err
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Authenticate validates a session token and slides its expiry. It updates
// last_seen at most once an hour per session.
func (s *Service) Authenticate(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	now := s.Now()
	var lastSeen time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT last_seen_at FROM sessions WHERE token_hash=$1 AND expires_at>$2`, crypt.HashToken(token), now).Scan(&lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if now.Sub(lastSeen) >= touchEvery {
		if _, err = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at=$2, expires_at=$3 WHERE token_hash=$1`, crypt.HashToken(token), now, now.Add(SessionLife)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Status reports setup, sign-in and two-step state.
func (s *Service) Status(ctx context.Context, token string) (State, error) {
	var st State
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return st, err
	}
	st.Setup = n == 0
	ok, err := s.Authenticate(ctx, token)
	if err != nil {
		return st, err
	}
	st.Authenticated = ok
	if ok {
		if err = s.DB.QueryRowContext(ctx, `SELECT totp_secret IS NOT NULL FROM users WHERE id=1`).Scan(&st.TOTPEnabled); err != nil {
			return st, err
		}
	}
	return st, nil
}

// Setup creates the one account and signs it in.
func (s *Service) Setup(ctx context.Context, username, password, setupKey string, m Meta) (string, error) {
	if len(s.SetupKey) < 24 || subtle.ConstantTimeCompare([]byte(setupKey), []byte(s.SetupKey)) != 1 {
		return "", ErrSetupKey
	}
	username = strings.TrimSpace(username)
	if len(username) < 1 || len(username) > 80 || len(password) < minPasswordBytes || len(password) > maxPasswordBytes {
		return "", ErrShortPassword
	}
	h, err := s.hash(password, s.PasswordCost)
	if err != nil {
		return "", errInternalUnexpected
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO users (id, username, password_hash) VALUES (1,$1,$2)`, username, h); err != nil {
		return "", ErrAlreadySetUp
	}
	return s.newSession(ctx, m)
}

// Login checks the password and, when two-step is on, a TOTP or recovery code.
func (s *Service) Login(ctx context.Context, username, password, code string, m Meta) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", ErrInvalidInput
	}
	var hash string
	var secret sql.NullString
	var last int64
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash, totp_secret, totp_last_step FROM users WHERE id=1 AND username=$1`, username).Scan(&hash, &secret, &last)
	if err != nil {
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return "", ErrBadCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", ErrBadCredentials
	}
	if secret.Valid && secret.String != "" {
		if err = s.secondFactor(ctx, secret.String, last, code); err != nil {
			return "", err
		}
	}
	return s.newSession(ctx, m)
}

func isTOTPShape(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) secondFactor(ctx context.Context, sealed string, last int64, code string) error {
	code = strings.TrimSpace(code)
	if !isTOTPShape(code) {
		if s.useRecoveryCode(ctx, code) {
			return nil
		}
		return ErrBadCredentials
	}
	plain, err := s.Box.Unseal(sealed)
	if err != nil {
		return ErrBadCredentials
	}
	now := s.Now()
	step := now.Unix() / 30
	valid, verr := totp.ValidateCustom(code, plain, now, totp.ValidateOpts{Period: 30, Skew: 0, Digits: 6})
	if verr != nil || !valid || step <= last {
		return ErrBadCredentials
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_last_step=$1 WHERE id=1 AND totp_last_step<$1`, step)
	if err != nil {
		return errInternalUnexpected
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrStaleCode
	}
	return nil
}

// Logout ends one session.
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=$1`, crypt.HashToken(token))
	return err
}

func (s *Service) checkPassword(ctx context.Context, password string) error {
	var hash string
	if len(password) > maxPasswordBytes || s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=1`).Scan(&hash) != nil ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrBadPassword
	}
	return nil
}

// TOTPSetup starts enrollment and returns the secret and otpauth URL.
func (s *Service) TOTPSetup(ctx context.Context, password string) (secret, url string, err error) {
	if err = s.checkPassword(ctx, password); err != nil {
		return
	}
	var username string
	var existing sql.NullString
	if err = s.DB.QueryRowContext(ctx, `SELECT username, totp_secret FROM users WHERE id=1`).Scan(&username, &existing); err != nil {
		return
	}
	if existing.Valid && existing.String != "" {
		err = ErrTOTPAlreadyOn
		return
	}
	key, kerr := totp.Generate(totp.GenerateOpts{Issuer: "sofar", AccountName: username})
	if kerr != nil {
		err = errInternalUnexpected
		return
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE users SET totp_pending=$1 WHERE id=1`, s.Box.Seal(key.Secret())); err != nil {
		return
	}
	return key.Secret(), key.URL(), nil
}

// TOTPConfirm finishes enrollment, signs out every other session and returns
// the recovery codes (shown once).
func (s *Service) TOTPConfirm(ctx context.Context, token, code, password string) ([]string, error) {
	if err := s.checkPassword(ctx, password); err != nil {
		return nil, err
	}
	var sealed string
	if s.DB.QueryRowContext(ctx, `SELECT totp_pending FROM users WHERE id=1 AND totp_secret IS NULL`).Scan(&sealed) != nil {
		return nil, ErrTOTPNotStarted
	}
	secret, err := s.Box.Unseal(sealed)
	if err != nil || !validTOTP(code, secret, s.Now()) {
		return nil, ErrBadCode
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE users SET totp_secret=totp_pending, totp_pending=NULL, totp_last_step=$1 WHERE id=1`, s.Now().Unix()/30); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash<>$1`, crypt.HashToken(token)); err != nil {
		return nil, err
	}
	codes, err := s.issueRecoveryCodes(ctx, tx)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

// TOTPDisable turns two-step verification off and drops its recovery codes.
func (s *Service) TOTPDisable(ctx context.Context, password, code string) error {
	var hash string
	var secret sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash, totp_secret FROM users WHERE id=1`).Scan(&hash, &secret); err != nil {
		return err
	}
	if !secret.Valid || secret.String == "" {
		return ErrTOTPAlreadyOff
	}
	plain, uerr := s.Box.Unseal(secret.String)
	if len(password) > maxPasswordBytes || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || uerr != nil || !validTOTP(code, plain, s.Now()) {
		return ErrBadCredentials
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE users SET totp_secret=NULL, totp_pending=NULL, totp_last_step=0 WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM recovery_codes`); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangePassword signs out every other session and removes push subscriptions
// (other devices are signed out, so their subscriptions go too).
func (s *Service) ChangePassword(ctx context.Context, token, current, next string) error {
	if len(current) > maxPasswordBytes {
		return ErrInvalidInput
	}
	if len(next) < minPasswordBytes || len(next) > maxPasswordBytes {
		return ErrShortPassword
	}
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=1`).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrBadPassword
	}
	h, err := s.hash(next, s.PasswordCost)
	if err != nil {
		return errInternalUnexpected
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=$1 WHERE id=1`, h); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash<>$1`, crypt.HashToken(token)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM push_subscriptions`); err != nil {
		return err
	}
	return tx.Commit()
}

// RegenerateRecoveryCodes replaces the codes; it needs the password and two-step on.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, password string) ([]string, error) {
	if err := s.checkPassword(ctx, password); err != nil {
		return nil, err
	}
	var on bool
	if err := s.DB.QueryRowContext(ctx, `SELECT totp_secret IS NOT NULL FROM users WHERE id=1`).Scan(&on); err != nil {
		return nil, err
	}
	if !on {
		return nil, ErrTOTPRequired
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	codes, err := s.issueRecoveryCodes(ctx, tx)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alikes

func newRecoveryCode() string {
	b := make([]byte, 10)
	rand.Read(b)
	out := make([]byte, 0, 11)
	for i, v := range b {
		if i == 5 {
			out = append(out, '-')
		}
		out = append(out, codeAlphabet[int(v)%len(codeAlphabet)])
	}
	return string(out)
}

func (s *Service) issueRecoveryCodes(ctx context.Context, tx *sql.Tx) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes`); err != nil {
		return nil, err
	}
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		codes[i] = newRecoveryCode()
		h, err := s.hash(normalizeCode(codes[i]), s.CodeCost)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, hash) VALUES (1,$1)`, h); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

func normalizeCode(c string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(c), "-", ""), " ", ""))
}

// useRecoveryCode spends a matching unused code. Each code works once.
func (s *Service) useRecoveryCode(ctx context.Context, code string) bool {
	code = normalizeCode(code)
	if len(code) != 10 {
		return false
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, hash FROM recovery_codes WHERE used_at IS NULL`)
	if err != nil {
		return false
	}
	type row struct {
		id   int64
		hash string
	}
	var all []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.hash) == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	for _, r := range all {
		if bcrypt.CompareHashAndPassword([]byte(r.hash), []byte(code)) == nil {
			res, err := s.DB.ExecContext(ctx, `UPDATE recovery_codes SET used_at=$2 WHERE id=$1 AND used_at IS NULL`, r.id, s.Now())
			if err != nil {
				return false
			}
			n, _ := res.RowsAffected()
			return n == 1
		}
	}
	return false
}

// RecoveryCodesLeft counts unused codes.
func (s *Service) RecoveryCodesLeft(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM recovery_codes WHERE used_at IS NULL`).Scan(&n)
	return n, err
}

// PurgeExpired removes sessions past their expiry.
func (s *Service) PurgeExpired(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < $1`, s.Now())
	return err
}

// validTOTP accepts the current 30-second step and one either side (enrollment
// and disable tolerate clock drift; login uses exact steps plus replay checks).
func validTOTP(code, secret string, now time.Time) bool {
	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{Period: 30, Skew: 1, Digits: 6})
	return err == nil && ok
}
