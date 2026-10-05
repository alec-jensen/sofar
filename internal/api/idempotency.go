package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/alec-jensen/sofar/internal/httpx"
)

const maxKeyLength = 200

// idempotent makes a mutating handler safe to retry. A request carrying an
// Idempotency-Key runs once; a retry with the same key and body returns the
// stored response (marked Idempotency-Replayed: true). The same key with a
// different request is refused, and a retry that arrives while the first is
// still running gets 409. Server errors are not stored, so they can be retried.
func (s *server) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		if len(key) > maxKeyLength {
			httpx.Fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long.")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "invalid_input", "Could not read the request.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
		hash := hex.EncodeToString(sum[:])

		res, err := s.DB.ExecContext(r.Context(), `INSERT INTO idempotency_keys (key, request_hash) VALUES ($1,$2) ON CONFLICT (key) DO NOTHING`, key, hash)
		if err != nil {
			httpx.Fail(w, http.StatusServiceUnavailable, "unavailable", "Database unavailable.")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			s.replay(w, r, key, hash)
			return
		}

		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if recover() != nil {
					rec = httptest.NewRecorder()
					httpx.Fail(rec, http.StatusInternalServerError, "internal", "Something went wrong.")
				}
			}()
			next(rec, r)
		}()
		result := rec.Result()
		out := rec.Body.Bytes()
		if result.StatusCode >= 500 {
			s.DB.ExecContext(r.Context(), `DELETE FROM idempotency_keys WHERE key=$1`, key)
		} else {
			s.DB.ExecContext(r.Context(), `UPDATE idempotency_keys SET status=$2, content_type=$3, body=$4 WHERE key=$1`, key, result.StatusCode, result.Header.Get("Content-Type"), out)
		}
		for k, v := range result.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(result.StatusCode)
		w.Write(out)
	}
}

func (s *server) replay(w http.ResponseWriter, r *http.Request, key, hash string) {
	var storedHash string
	var status sql.NullInt64
	var contentType sql.NullString
	var body []byte
	err := s.DB.QueryRowContext(r.Context(), `SELECT request_hash, status, content_type, body FROM idempotency_keys WHERE key=$1`, key).Scan(&storedHash, &status, &contentType, &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// the first attempt failed with a server error and cleared its key
		httpx.Fail(w, http.StatusConflict, "idempotency_retry", "Please retry the request.")
	case err != nil:
		httpx.Fail(w, http.StatusServiceUnavailable, "unavailable", "Database unavailable.")
	case storedHash != hash:
		httpx.Fail(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "This Idempotency-Key was used for a different request.")
	case !status.Valid:
		httpx.Fail(w, http.StatusConflict, "idempotency_in_progress", "The first request with this key is still running.")
	default:
		w.Header().Set("Content-Type", contentType.String)
		w.Header().Set("Idempotency-Replayed", "true")
		w.WriteHeader(int(status.Int64))
		w.Write(body)
	}
}
