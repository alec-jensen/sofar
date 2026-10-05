// Package httpx holds small HTTP helpers shared by the API packages.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ErrorBody is the JSON error shape of /api/v1.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Respond writes v as JSON with status 200.
func Respond(w http.ResponseWriter, v any) { RespondStatus(w, http.StatusOK, v) }

// RespondStatus writes v as JSON with the given status.
func RespondStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Fail writes the standard error body. The code is a stable machine-readable
// string (for example "unauthorized"); the message is for people.
func Fail(w http.ResponseWriter, status int, code, message string) {
	var b ErrorBody
	b.Error.Code = code
	b.Error.Message = message
	RespondStatus(w, status, b)
}

// Decode reads exactly one JSON value, rejecting unknown fields and trailing data.
func Decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
