//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/alec-jensen/sofar/internal/store"
	"github.com/alec-jensen/sofar/internal/testkit"
)

func TestMigrations_UpFromEmpty(t *testing.T) {
	d := testkit.DB(t)
	ctx := context.Background()
	for _, table := range []string{"users", "sessions", "recovery_codes", "push_subscriptions", "settings", "events", "jobs", "provider_connections", "audit_log"} {
		var n int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM public.`+table).Scan(&n); err != nil {
			t.Errorf("%s missing: %v", table, err)
		}
	}
	if ok, err := store.Current(ctx, d); err != nil || !ok {
		t.Errorf("Current = %v, %v", ok, err)
	}
	if err := store.Migrate(ctx, d); err != nil {
		t.Errorf("a second migrate must be a no-op: %v", err)
	}
	var legacy int
	d.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.schemata WHERE schema_name='legacy'`).Scan(&legacy)
	if legacy != 0 {
		t.Error("a fresh database must not get a legacy schema")
	}
}

func TestMigrations_AuditLogIsAppendOnly(t *testing.T) {
	d := testkit.DB(t)
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `INSERT INTO audit_log (actor, action) VALUES ('t','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE audit_log SET actor='y'`); err == nil {
		t.Error("update must be refused")
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("delete must be refused")
	}
}

func TestMigrations_QuarantinesLegacy(t *testing.T) {
	d, _ := testkit.Empty(t)
	ctx := context.Background()
	schema, err := os.ReadFile("testdata/legacy_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.ExecContext(ctx, string(schema)); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'alec', 'hash')`,
		`INSERT INTO accounts (id, institution_name, account_type, display_name) VALUES ('a1', 'TTCU', 'depository', 'checking')`,
		`INSERT INTO transactions (id, account_id, date, amount, raw_merchant, clean_merchant, direction, review_status) VALUES ('t1', 'a1', '2026-01-02', 1234, 'RAW', 'Clean', 'out', 'confirmed')`,
		`INSERT INTO rules (merchant_pattern, category_id) VALUES ('clean', 'spending')`,
	} {
		if _, err = d.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err = store.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"legacy.users": 1, "legacy.accounts": 1, "legacy.transactions": 1, "legacy.rules": 1} {
		var n int
		if err = d.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != want {
			t.Errorf("%s: %d rows (%v), want %d", table, n, err, want)
		}
	}
	var legacyTx int
	d.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='transactions'`).Scan(&legacyTx)
	if legacyTx != 0 {
		t.Error("public must not still hold the legacy transactions table")
	}
	var users int
	if err = d.QueryRowContext(ctx, `SELECT count(*) FROM public.users`).Scan(&users); err != nil || users != 0 {
		t.Errorf("new public.users should exist and be empty: %d %v", users, err)
	}
}
