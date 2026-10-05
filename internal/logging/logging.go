// Package logging sets up structured JSON logs and the helpers that keep
// amounts, descriptions, account ids and credentials out of them. spec: S17.
package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/alec-jensen/sofar/internal/money"
	"github.com/rs/zerolog"
)

// userinfoURL matches anything resembling a URL with credentials, such as a
// SimpleFIN access URL.
var userinfoURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/@"']*:[^\s/@"']*@[^\s"']*`)

// bearer matches Authorization-style values.
var bearer = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)

// Scrub removes credential-looking text from a string.
func Scrub(s string) string {
	s = userinfoURL.ReplaceAllString(s, "[redacted-url]")
	return bearer.ReplaceAllString(s, "$1 [redacted]")
}

type scrubHook struct{}

func (scrubHook) Run(e *zerolog.Event, _ zerolog.Level, msg string) {
	if s := Scrub(msg); s != msg {
		e.Str("scrubbed", "message")
	}
}

// scrubWriter rewrites every finished log line, so credentials in any field
// (including error strings) never reach the output.
type scrubWriter struct{ w io.Writer }

func (s scrubWriter) Write(p []byte) (int, error) {
	out := Scrub(string(p))
	if _, err := io.WriteString(s.w, out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// New builds the process logger.
func New(w io.Writer, level string) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil || level == "" {
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(scrubWriter{w}).Level(lvl).With().Timestamp().Logger().Hook(scrubHook{})
}

// Amount logs a magnitude bucket only, never the amount.
func Amount(c money.Cents) string {
	if c < 0 {
		c = -c
	}
	switch {
	case c < 1_000:
		return "<$10"
	case c < 10_000:
		return "$10-100"
	case c < 100_000:
		return "$100-1k"
	default:
		return ">=$1k"
	}
}

// Desc logs a hash prefix and the length of a description, never the text.
func Desc(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%s/%d", hex.EncodeToString(sum[:3]), len(s))
}

// Acct logs only the last two characters of an account id.
func Acct(id string) string {
	if len(id) <= 2 {
		return "..**"
	}
	return "..." + id[len(id)-2:]
}
