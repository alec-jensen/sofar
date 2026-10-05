// Package importer reads bank files (CSV, OFX/QFX, QIF) into transactions,
// shows a dry run, and applies one undoable batch. Bank files are untrusted
// input: every parser caps sizes and reports bad rows instead of failing.
// spec: S19.5, S11, A9.3 invariant 11.
package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/normalize"
)

// Limits protect against hostile or accidental huge files.
const (
	MaxBytes  = 10 << 20
	MaxRows   = 100_000
	maxFieldN = 500
)

// Row is one parsed transaction before it is stored.
type Row struct {
	// ProviderID is the file's own id (OFX FITID or a mapped CSV column). Empty
	// means the importer synthesizes one from the fingerprint.
	ProviderID  string      `json:"provider_id,omitempty"`
	Date        civil.Date  `json:"date"`
	Amount      money.Cents `json:"amount"` // negative is an outflow
	Description string      `json:"description"`
	Payee       string      `json:"payee,omitempty"`
	Memo        string      `json:"memo,omitempty"`
}

// Skipped is a row that could not be used, with the reason.
type Skipped struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Parsed is the result of reading a file.
type Parsed struct {
	Rows    []Row     `json:"rows"`
	Skipped []Skipped `json:"skipped,omitempty"`
}

var (
	// ErrTooLarge means the file exceeds MaxBytes.
	ErrTooLarge = errors.New("importer: file is too large")
	// ErrFormat means the file is not in the expected format.
	ErrFormat = errors.New("importer: unrecognized file format")
)

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxFieldN {
		s = s[:maxFieldN]
	}
	return s
}

// Fingerprint identifies a transaction by account, date, amount and normalized
// description, so the same transaction from different sources can be recognized.
func Fingerprint(accountID string, date civil.Date, amount money.Cents, description string) string {
	h := sha256.Sum256([]byte(accountID + "|" + date.String() + "|" + amount.String() + "|" + normalize.Normalize(description)))
	return hex.EncodeToString(h[:16])
}
