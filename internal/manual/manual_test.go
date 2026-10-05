//go:build integration

package manual_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t   *testing.T
	db  *sql.DB
	led *ledger.Service
	man *manual.Service
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	return &env{t: t, db: db, led: led, man: manual.New(db, reg, led)}
}

func (e *env) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err = tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func d(s string) civil.Date {
	v, err := civil.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func str(s string) *string { return &s }
func i64(n int64) *int64   { return &n }

func TestS11_ManualTransactionRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var acct, txn string
	e.tx(func(tx *sql.Tx) {
		var err error
		if acct, err = e.man.CreateAccount(ctx, tx, events.ActorUser, "cash", "tracked", ""); err != nil {
			t.Fatal(err)
		}
		if txn, err = e.man.AddTxn(ctx, tx, events.ActorUser, acct, d("2026-10-02"), -2500, "  lunch   with sam ", "Sam"); err != nil {
			t.Fatal(err)
		}
	})
	got, err := e.led.Get(ctx, txn)
	if err != nil || got.Description != "lunch with sam" || got.Amount != -2500 || got.PostedOn != d("2026-10-02") || got.Payee != "Sam" {
		t.Fatalf("after add: %+v %v", got, err)
	}
	var edit events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		edit, err = e.man.EditTxn(ctx, tx, events.ActorUser, txn, manual.Edit{Amount: i64(-3000), Description: str("lunch")})
		if err != nil {
			t.Fatal(err)
		}
	})
	got, _ = e.led.Get(ctx, txn)
	if got.Amount != -3000 || got.Description != "lunch" || got.Payee != "Sam" {
		t.Fatalf("after edit: %+v", got)
	}
	// undo restores the earlier values
	e.tx(func(tx *sql.Tx) {
		if _, err := e.man.Undo(ctx, tx, events.ActorUser, edit.ID); err != nil {
			t.Fatal(err)
		}
	})
	got, _ = e.led.Get(ctx, txn)
	if got.Amount != -2500 || got.Description != "lunch with sam" {
		t.Fatalf("after undo: %+v", got)
	}
	// delete is a reversible hide
	var del events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if del, err = e.man.DeleteTxn(ctx, tx, events.ActorUser, txn); err != nil {
			t.Fatal(err)
		}
	})
	if p, _ := e.led.View(ctx, ledger.Filter{}); len(p.Items) != 0 {
		t.Fatal("a deleted manual transaction must not appear")
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.led.Undo(ctx, tx, events.ActorUser, del.ID, ""); err != nil {
			t.Fatal(err)
		}
	})
	if p, _ := e.led.View(ctx, ledger.Filter{}); len(p.Items) != 1 {
		t.Fatal("undoing the delete brings it back")
	}
}

func TestS11_ManualValidation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acct := testkit.Account(t, e.db, "checking")
	bank := testkit.RawTxn(t, e.db, acct, testkit.Raw{Amount: -100})
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, err := e.man.AddTxn(ctx, tx, events.ActorUser, acct, d("2026-10-01"), 0, "x", ""); !errors.Is(err, manual.ErrInvalid) {
		t.Errorf("zero amount: %v", err)
	}
	if _, err := e.man.AddTxn(ctx, tx, events.ActorUser, acct, d("2026-10-01"), -5, "  ", ""); !errors.Is(err, manual.ErrInvalid) {
		t.Errorf("empty description: %v", err)
	}
	if _, err := e.man.EditTxn(ctx, tx, events.ActorUser, bank, manual.Edit{Amount: i64(-1)}); !errors.Is(err, manual.ErrNotManual) {
		t.Errorf("a bank transaction's fields are never edited: %v", err)
	}
	if _, err := e.man.CreateAccount(ctx, tx, events.ActorUser, "x", "mystery", ""); !errors.Is(err, manual.ErrInvalid) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestS11_SyncedMatchSuggestsMergeNeverAuto(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acct := testkit.Account(t, e.db, "checking")
	var manualID string
	e.tx(func(tx *sql.Tx) {
		var err error
		if manualID, err = e.man.AddTxn(ctx, tx, events.ActorUser, acct, d("2026-10-01"), -2000, "dad paid me back", ""); err != nil {
			t.Fatal(err)
		}
		cat := "gift_other"
		if _, err = e.led.Apply(ctx, tx, events.ActorUser, manualID, ledger.KindSetCategory, ledger.SetCategory{TxnID: manualID, CategoryID: &cat}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = e.led.Apply(ctx, tx, events.ActorUser, manualID, ledger.KindSetNote, ledger.SetNote{TxnID: manualID, Note: "dinner"}, ""); err != nil {
			t.Fatal(err)
		}
	})
	if s, _ := e.man.Suggestions(ctx, acct); len(s) != 0 {
		t.Fatalf("nothing to suggest before a synced row exists: %+v", s)
	}
	// near misses must not be suggested
	testkit.RawTxn(t, e.db, acct, testkit.Raw{PostedOn: "2026-10-01", Amount: -2001})
	testkit.RawTxn(t, e.db, acct, testkit.Raw{PostedOn: "2026-10-09", Amount: -2000})
	if s, _ := e.man.Suggestions(ctx, acct); len(s) != 0 {
		t.Fatalf("different amount or more than 3 days apart: %+v", s)
	}
	bank := testkit.RawTxn(t, e.db, acct, testkit.Raw{ProviderID: "bank-1", PostedOn: "2026-10-03", Amount: -2000, Description: "VENMO PAYMENT"})
	s, err := e.man.Suggestions(ctx, acct)
	if err != nil || len(s) != 1 || s[0].ManualID != manualID || s[0].BankID != bank {
		t.Fatalf("want one suggestion, got %+v %v", s, err)
	}
	// suggesting merges nothing
	p, _ := e.led.View(ctx, ledger.Filter{AccountID: acct})
	if len(p.Items) != 4 {
		t.Fatalf("the suggestion must not change the ledger: %d rows", len(p.Items))
	}

	var merge events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if merge, err = e.man.Merge(ctx, tx, events.ActorUser, manualID, bank); err != nil {
			t.Fatal(err)
		}
	})
	got, _ := e.led.Get(ctx, bank)
	if got.CategoryID != "gift_other" || got.Note != "dinner" || got.Description != "VENMO PAYMENT" {
		t.Fatalf("the bank row keeps the manual row's overlay and its own bank fields: %+v", got)
	}
	if m, _ := e.led.Get(ctx, manualID); !m.Hidden {
		t.Fatal("the manual row disappears once merged")
	}
	if s, _ = e.man.Suggestions(ctx, acct); len(s) != 0 {
		t.Fatalf("a merged pair is not suggested again: %+v", s)
	}

	e.tx(func(tx *sql.Tx) {
		if _, err := e.man.Undo(ctx, tx, events.ActorUser, merge.ID); err != nil {
			t.Fatal(err)
		}
	})
	if m, _ := e.led.Get(ctx, manualID); m.Hidden {
		t.Fatal("undoing the merge brings the manual row back")
	}
	if b, _ := e.led.Get(ctx, bank); b.CategoryID != "" || b.Note != "" {
		t.Fatalf("and the bank row loses the inherited overlay: %+v", b)
	}
}

func TestS11_MergeRefusesBadPairs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a1, a2 := testkit.Account(t, e.db, "one"), testkit.Account(t, e.db, "two")
	var m string
	e.tx(func(tx *sql.Tx) {
		var err error
		if m, err = e.man.AddTxn(ctx, tx, events.ActorUser, a1, d("2026-10-01"), -100, "x", ""); err != nil {
			t.Fatal(err)
		}
	})
	otherAccount := testkit.RawTxn(t, e.db, a2, testkit.Raw{Amount: -100})
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if _, err := e.man.Merge(ctx, tx, events.ActorUser, m, otherAccount); !errors.Is(err, manual.ErrInvalid) {
		t.Errorf("different accounts: %v", err)
	}
}
