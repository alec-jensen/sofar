package httpx

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
)

// Page is the envelope list endpoints return. Next is empty on the last page.
type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

// EncodeCursor turns a position into an opaque cursor string.
func EncodeCursor(position string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(position))
}

// DecodeCursor reverses EncodeCursor. An empty cursor means the first page.
func DecodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(b) > 512 {
		return "", errors.New("invalid cursor")
	}
	return string(b), nil
}

// PageParams reads ?cursor= and ?limit=, clamping the limit to [1, max].
func PageParams(r *http.Request, defaultLimit, max int) (position string, limit int, err error) {
	limit = defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 1 {
			return "", 0, errors.New("invalid limit")
		}
		limit = n
	}
	if limit > max {
		limit = max
	}
	position, err = DecodeCursor(r.URL.Query().Get("cursor"))
	return position, limit, err
}
