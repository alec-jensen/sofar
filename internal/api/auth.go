package api

import (
	"errors"
	"net/http"

	"github.com/alec-jensen/sofar/internal/auth"
	"github.com/alec-jensen/sofar/internal/httpx"
)

const cookieName = "sofar_session"

func (s *server) token(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

func (s *server) clientIP(r *http.Request) string { return s.Cfg.Proxies().ClientIP(r) }

func (s *server) meta(r *http.Request) auth.Meta {
	return auth.Meta{UserAgent: r.UserAgent(), IP: s.clientIP(r)}
}

func (s *server) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.Cfg.Production, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (s *server) protect(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.Auth.Authenticate(r.Context(), s.token(r))
		if err != nil {
			httpx.Fail(w, http.StatusServiceUnavailable, "unavailable", "Database unavailable.")
			return
		}
		if !ok {
			httpx.Fail(w, http.StatusUnauthorized, "unauthenticated", "Please sign in to continue.")
			return
		}
		h(w, r)
	})
}

var authStatusCodes = map[string]int{
	"invalid_input": http.StatusBadRequest, "setup_key": http.StatusForbidden, "already_set_up": http.StatusConflict,
	"bad_credentials": http.StatusUnauthorized, "bad_password": http.StatusUnauthorized, "stale_code": http.StatusUnauthorized,
	"totp_already_on": http.StatusConflict, "totp_already_off": http.StatusConflict, "totp_not_started": http.StatusBadRequest,
	"bad_code": http.StatusBadRequest, "totp_required": http.StatusConflict, "short_password": http.StatusBadRequest,
	"unauthenticated": http.StatusUnauthorized,
}

// failure answers an error from the auth service. Credential failures are
// delayed under a distributed guessing flood (never correct logins).
func (s *server) failure(w http.ResponseWriter, err error) {
	var ae *auth.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case "bad_credentials", "bad_password", "stale_code":
			s.Throttle.Failed()
		}
		status := authStatusCodes[ae.Code]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		httpx.Fail(w, status, ae.Code, ae.Message)
		return
	}
	s.Log.Error().Msg("auth: unexpected error")
	httpx.Fail(w, http.StatusInternalServerError, "internal", "Something went wrong.")
}

// limited enforces the per-address attempt window; it answers 429 itself.
func (s *server) limited(w http.ResponseWriter, r *http.Request) bool {
	if !s.Throttle.Allow(s.clientIP(r)) {
		httpx.Fail(w, http.StatusTooManyRequests, "too_many_attempts", "Too many attempts. Try again in 15 minutes.")
		return true
	}
	return false
}

func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.Auth.Status(r.Context(), s.token(r))
	if err != nil {
		httpx.Fail(w, http.StatusServiceUnavailable, "unavailable", "Database unavailable.")
		return
	}
	httpx.Respond(w, map[string]bool{"authenticated": st.Authenticated, "setup": st.Setup, "totpEnabled": st.TOTPEnabled})
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, token string) {
	s.Throttle.Succeeded(s.clientIP(r))
	s.setCookie(w, token, int(auth.SessionLife.Seconds()))
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		SetupKey string `json:"setupKey"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid account details.")
		return
	}
	token, err := s.Auth.Setup(r.Context(), in.Username, in.Password, in.SetupKey, s.meta(r))
	if err != nil {
		s.failure(w, err)
		return
	}
	s.startSession(w, r, token)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid login details.")
		return
	}
	token, err := s.Auth.Login(r.Context(), in.Username, in.Password, in.Code, s.meta(r))
	if err != nil {
		s.failure(w, err)
		return
	}
	s.startSession(w, r, token)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.Logout(r.Context(), s.token(r)); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "internal", "Could not sign out.")
		return
	}
	s.setCookie(w, "", -1)
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) totpSetup(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Enter your password to continue.")
		return
	}
	secret, url, err := s.Auth.TOTPSetup(r.Context(), in.Password)
	if err != nil {
		s.failure(w, err)
		return
	}
	s.Throttle.Succeeded(s.clientIP(r))
	httpx.Respond(w, map[string]string{"secret": secret, "url": url})
}

func (s *server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid code.")
		return
	}
	codes, err := s.Auth.TOTPConfirm(r.Context(), s.token(r), in.Code, in.Password)
	if err != nil {
		s.failure(w, err)
		return
	}
	httpx.Respond(w, map[string]any{"ok": true, "recoveryCodes": codes})
}

func (s *server) totpDisable(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid details.")
		return
	}
	if err := s.Auth.TOTPDisable(r.Context(), in.Password, in.Code); err != nil {
		s.failure(w, err)
		return
	}
	s.Throttle.Succeeded(s.clientIP(r))
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Invalid password details.")
		return
	}
	if err := s.Auth.ChangePassword(r.Context(), s.token(r), in.Current, in.Next); err != nil {
		s.failure(w, err)
		return
	}
	s.Throttle.Succeeded(s.clientIP(r))
	httpx.Respond(w, map[string]bool{"ok": true})
}

func (s *server) recoveryCodes(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if httpx.Decode(r, &in) != nil {
		httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Enter your password to continue.")
		return
	}
	codes, err := s.Auth.RegenerateRecoveryCodes(r.Context(), in.Password)
	if err != nil {
		s.failure(w, err)
		return
	}
	s.Throttle.Succeeded(s.clientIP(r))
	httpx.Respond(w, map[string]any{"recoveryCodes": codes})
}
