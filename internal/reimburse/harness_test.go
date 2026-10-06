//go:build integration

package reimburse_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/reimburse"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	led  *ledger.Service
	lnk  *links.Service
	cps  *counterparties.Service
	rcv  *receivables.Service
	svc  *reimburse.Service
	acct string
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	e := &env{t: t, db: db, led: led, now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	e.lnk = links.New(db, reg)
	e.cps = counterparties.New(db, reg)
	e.rcv = receivables.New(db, reg, e.lnk, e.cps, manual.New(db, reg, led))
	e.rcv.Now = func() time.Time { return e.now }
	e.svc = reimburse.New(db, reg, e.lnk, e.cps, e.rcv)
	e.svc.Now = func() time.Time { return e.now }
	e.acct = testkit.Account(t, db, "checking")
	return e
}

func (e *env) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) setCategory(id, category string, reviewed bool) {
	e.t.Helper()
	e.tx(func(tx *sql.Tx) {
		ctx := context.Background()
		if _, err := e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &category}, ""); err != nil {
			e.t.Fatal(err)
		}
		if reviewed {
			if _, err := e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true}, ""); err != nil {
				e.t.Fatal(err)
			}
		}
	})
}

// expense adds a reviewed card purchase. posted is when it shows up in the feed;
// transacted is when it happened.
func (e *env) expense(posted, transacted string, cents int64, desc, category string) string {
	return e.expenseIn(e.acct, posted, transacted, cents, desc, category, false)
}

func (e *env) expenseIn(acct, posted, transacted string, cents int64, desc, category string, pending bool) string {
	id := testkit.RawTxn(e.t, e.db, acct, testkit.Raw{PostedOn: posted, TransactedOn: transacted, Amount: -cents, Description: desc, Pending: pending})
	e.setCategory(id, category, true)
	return id
}

func (e *env) payment(posted string, cents int64, desc, memo string) string {
	return testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: posted, TransactedOn: posted, Amount: cents, Description: desc, Memo: memo})
}

func (e *env) dad() counterparties.Counterparty {
	var c counterparties.Counterparty
	e.tx(func(tx *sql.Tx) {
		var err error
		if c, err = e.cps.Create(context.Background(), tx, events.ActorUser, "Dad", "VENMO PAYMENT FROM DAD"); err != nil {
			e.t.Fatal(err)
		}
	})
	return c
}

func (e *env) process() map[string]reimburse.Decision {
	e.t.Helper()
	out := map[string]reimburse.Decision{}
	e.tx(func(tx *sql.Tx) {
		ds, err := e.svc.Process(context.Background(), tx)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, d := range ds {
			out[d.PaymentID] = d
		}
	})
	return out
}

func (e *env) activeLinks() []links.Link {
	all, err := e.lnk.List(context.Background(), e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []links.Link
	for _, l := range all {
		if l.Active() {
			out = append(out, l)
		}
	}
	return out
}

func (e *env) held() map[string]reimburse.Held {
	hs, err := e.svc.ListHeld(context.Background(), e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]reimburse.Held{}
	for _, h := range hs {
		out[h.TxnID] = h
	}
	return out
}

func (e *env) suggestions(payment string) []string {
	rs, err := e.db.Query(`SELECT expense_txn_id::text FROM link_suggestions WHERE payment_txn_id=$1 ORDER BY rank`, payment)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var id string
		rs.Scan(&id)
		out = append(out, id)
	}
	return out
}

func (e *env) sweepAt(now time.Time) {
	e.now = now
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.Sweep(context.Background(), tx); err != nil {
			e.t.Fatal(err)
		}
	})
}

func (e *env) acctOf(name string) string { return testkit.Account(e.t, e.db, name) }

func (e *env) paymentIn(acct, posted string, cents int64, desc string) string {
	return testkit.RawTxn(e.t, e.db, acct, testkit.Raw{PostedOn: posted, TransactedOn: posted, Amount: cents, Description: desc})
}
