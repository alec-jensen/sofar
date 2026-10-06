package testkit

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// Fataler is the part of testing.TB (and rapid.T) the fixtures need.
type Fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Raw describes a raw transaction to insert. Zero values get sensible defaults.
type Raw struct {
	ProviderID        string
	PostedOn          string // "2026-10-01"
	TransactedOn      string
	PostedInstant     *time.Time
	TransactedInstant *time.Time
	Amount            int64 // cents, negative is an outflow
	Description       string
	Payee             string
	Memo              string
	Pending           bool
}

// Account inserts a budget account and returns its id.
func Account(t testing.TB, db *sql.DB, name string) string {
	t.Helper()
	return AccountOfKind(t, db, name, "budget")
}

// AccountOfKind inserts an account of the given kind and returns its id.
func AccountOfKind(t testing.TB, db *sql.DB, name, kind string) string {
	t.Helper()
	var id string
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO accounts (provider, provider_account_id, name, kind) VALUES ('test',$1,$1,$2) RETURNING id`, name, kind).Scan(&id)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return id
}

var rawSeq int

// RawTxn inserts a raw transaction and returns its id.
func RawTxn(t Fataler, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, accountID string, r Raw) string {
	t.Helper()
	rawSeq++
	if r.ProviderID == "" {
		r.ProviderID = fmt.Sprintf("txn-%d", rawSeq)
	}
	if r.PostedOn == "" {
		r.PostedOn = "2026-10-01"
	}
	var transacted any
	if r.TransactedOn != "" {
		transacted = r.TransactedOn
	}
	var id string
	err := q.QueryRowContext(context.Background(), `
		INSERT INTO raw_transactions (account_id, provider_txn_id, posted_on, posted_instant, transacted_on, transacted_instant, amount, description, payee, memo, pending)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		accountID, r.ProviderID, r.PostedOn, r.PostedInstant, transacted, r.TransactedInstant, r.Amount, r.Description, r.Payee, r.Memo, r.Pending).Scan(&id)
	if err != nil {
		t.Fatalf("insert raw transaction: %v", err)
	}
	return id
}
