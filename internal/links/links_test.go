//go:build integration

package links_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	led  *ledger.Service
	rec  *recompute.Service
	lnk  *links.Service
	acct string
}

func ym(y int, m time.Month) civil.YearMonth { return civil.YearMonth{Y: y, M: m} }

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	rec := recompute.New(db, reg, led, time.UTC)
	rec.Now = func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) }
	e := &env{t: t, db: db, led: led, rec: rec, lnk: links.New(db, reg), acct: testkit.Account(t, db, "checking")}
	for _, m := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"} {
		pay := testkit.RawTxn(t, db, e.acct, testkit.Raw{PostedOn: m + "-01", Amount: 400000, Description: "PAYROLL"})
		e.set(pay, "salary")
	}
	return e
}

func (e *env) set(txn, category string) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	ctx := context.Background()
	if _, err := e.led.Apply(ctx, tx, events.ActorUser, txn, ledger.KindSetCategory, ledger.SetCategory{TxnID: txn, CategoryID: &category}, ""); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.led.Apply(ctx, tx, events.ActorUser, txn, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: txn, Reviewed: true}, ""); err != nil {
		e.t.Fatal(err)
	}
	tx.Commit()
}

func (e *env) buy(date string, cents int64, desc string) string {
	id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: -cents, Description: desc})
	e.set(id, "dining_out")
	return id
}

func (e *env) deposit(date string, cents int64, desc string) string {
	return testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: cents, Description: desc})
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

func (e *env) link(p links.Params) (l links.Link, err error) {
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	if l, err = e.lnk.Create(context.Background(), tx, events.ActorUser, p); err != nil {
		return l, err
	}
	return l, tx.Commit()
}

func (e *env) snap(m civil.YearMonth) budget.MonthState {
	e.t.Helper()
	if _, err := e.rec.Recompute(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	s, err := e.rec.GetSnapshot(context.Background(), e.db, m)
	if err != nil {
		e.t.Fatal(err)
	}
	return s.State
}

func (e *env) hash(m civil.YearMonth) string {
	e.snap(m)
	var h string
	e.db.QueryRow(`SELECT input_hash FROM month_snapshots WHERE month=$1`, m.First().String()).Scan(&h)
	return h
}

func TestS2_ExampleF_ReimbursementAcrossMonths(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 10000, "RESTAURANT")
	venmo := e.deposit("2026-05-03", 5000, "VENMO PAYMENT FROM DAD")
	before := e.snap(ym(2026, 4))
	mayBefore := e.snap(ym(2026, 5))
	if _, err := e.link(links.Params{PaymentID: venmo, ExpenseID: dinner, Amount: 5000}); err != nil {
		t.Fatal(err)
	}
	after := e.snap(ym(2026, 4))
	if after.SpendLeft-before.SpendLeft != 5000 {
		t.Fatalf("a $100 dinner with $50 back is $50 of spending in the dinner's month: %v -> %v", before.SpendLeft, after.SpendLeft)
	}
	mayAfter := e.snap(ym(2026, 5))
	if mayAfter.Income != mayBefore.Income || mayAfter.Plan != mayBefore.Plan {
		t.Fatalf("a reimbursement is never income: %+v vs %+v", mayBefore, mayAfter)
	}
}

func TestS12_RefundNetsInExpenseMonth(t *testing.T) {
	e := setup(t)
	buy := e.buy("2026-03-12", 8000, "SHOP")
	refund := e.deposit("2026-05-02", 8000, "SHOP REFUND")
	before := e.snap(ym(2026, 3))
	if _, err := e.link(links.Params{PaymentID: refund, ExpenseID: buy}); err != nil {
		t.Fatal(err)
	}
	if got := e.snap(ym(2026, 3)).SpendLeft - before.SpendLeft; got != 8000 {
		t.Fatalf("the refund undoes the purchase in March: %d", got)
	}
}

func TestS12_ReimbursementBeforeExpenseAllowed(t *testing.T) {
	e := setup(t)
	venmo := e.deposit("2026-04-18", 3000, "VENMO")
	dinner := e.buy("2026-04-20", 6000, "RESTAURANT") // posts after the payback
	before := e.snap(ym(2026, 4))
	if _, err := e.link(links.Params{PaymentID: venmo, ExpenseID: dinner}); err != nil {
		t.Fatalf("either order works: %v", err)
	}
	if e.snap(ym(2026, 4)).SpendLeft-before.SpendLeft != 3000 {
		t.Fatal("netted")
	}
}

func TestS12_OverpaymentExcessAsks(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 4000, "RESTAURANT")
	venmo := e.deposit("2026-04-22", 5000, "VENMO")
	if _, err := e.link(links.Params{PaymentID: venmo, ExpenseID: dinner, Amount: 5000}); !errors.Is(err, links.ErrExceeds) {
		t.Fatalf("a link above the remaining amount is refused unless overridden: %v", err)
	}
	l, err := e.link(links.Params{PaymentID: venmo, ExpenseID: dinner}) // as much as fits
	if err != nil || l.Amount != 4000 {
		t.Fatalf("capped to what is left: %+v %v", l, err)
	}
	// the excess is an unlinked deposit, held until the user says what it is
	e.snap(ym(2026, 4))
	h, err := e.rec.Held(context.Background(), e.db, ym(2026, 4))
	if err != nil || int64(h) != 1000 {
		t.Fatalf("the $10 excess is held, not income: held=%d %v", h, err)
	}
}

func TestA9_Inv7_LinksNeverExceedRemainingNorCreateIncome(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 10000, "RESTAURANT")
	p1 := e.deposit("2026-04-21", 7000, "VENMO A")
	p2 := e.deposit("2026-04-22", 7000, "VENMO B")
	if _, err := e.link(links.Params{PaymentID: p1, ExpenseID: dinner}); err != nil {
		t.Fatal(err)
	}
	// only 30 is left on the dinner
	if l, err := e.link(links.Params{PaymentID: p2, ExpenseID: dinner}); err != nil || l.Amount != 3000 {
		t.Fatalf("second payment fills the rest: %+v %v", l, err)
	}
	if _, err := e.link(links.Params{PaymentID: p2, ExpenseID: dinner, Amount: 100}); err == nil {
		t.Fatal("the dinner is fully reimbursed")
	}
	// a link cannot make spending negative or pay out more than the payment held
	var sum int64
	e.db.QueryRow(`SELECT sum(amount) FROM links WHERE expense_txn_id=$1 AND state IN ('tentative','confirmed')`, dinner).Scan(&sum)
	if sum > 10000 {
		t.Fatalf("netting never exceeds the expense: %d", sum)
	}
	if _, err := e.link(links.Params{PaymentID: dinner, ExpenseID: p1}); err == nil {
		t.Fatal("direction matters: money in pays back money out")
	}
	// the payment side
	if rem, _ := e.lnk.PaymentRemaining(context.Background(), e.db, p2); rem != 4000 {
		t.Fatalf("payment remainder: %d", rem)
	}
	e.snap(ym(2026, 4))
}

func TestA9_Inv14_RejectTentativeRevertsExactly(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 10000, "RESTAURANT")
	venmo := e.deposit("2026-05-03", 5000, "VENMO")
	before := map[civil.YearMonth]string{ym(2026, 4): e.hash(ym(2026, 4)), ym(2026, 5): e.hash(ym(2026, 5))}
	l, err := e.link(links.Params{PaymentID: venmo, ExpenseID: dinner, CreatedBy: links.ByAutoPattern})
	if err != nil || l.State != links.Tentative {
		t.Fatalf("%+v %v", l, err)
	}
	if e.hash(ym(2026, 4)) == before[ym(2026, 4)] {
		t.Fatal("a tentative link nets immediately")
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.lnk.Reject(context.Background(), tx, events.ActorUser, l.ID); err != nil {
			t.Fatal(err)
		}
	})
	for m, h := range before {
		if got := e.hash(m); got != h {
			t.Fatalf("rejecting restores %v exactly: %s vs %s", m, got, h)
		}
	}
	// a rejected link cannot be confirmed, and the state machine refuses nonsense
	e.tx(func(tx *sql.Tx) {
		if _, err := e.lnk.Confirm(context.Background(), tx, events.ActorUser, l.ID); err == nil {
			t.Fatal("rejected stays rejected")
		}
	})
}

func TestS12_LinkUndoRedoAndConfirm(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 10000, "RESTAURANT")
	venmo := e.deposit("2026-04-21", 5000, "VENMO")
	base := e.hash(ym(2026, 4))
	var created events.Event
	var l links.Link
	e.tx(func(tx *sql.Tx) {
		var err error
		if l, err = e.lnk.Create(context.Background(), tx, events.ActorUser, links.Params{PaymentID: venmo, ExpenseID: dinner}); err != nil {
			t.Fatal(err)
		}
		tx.QueryRowContext(context.Background(), `SELECT id FROM events WHERE kind='link.create' ORDER BY id DESC LIMIT 1`).Scan(&created.ID)
	})
	netted := e.hash(ym(2026, 4))
	e.tx(func(tx *sql.Tx) {
		if _, err := e.lnk.Confirm(context.Background(), tx, events.ActorUser, l.ID); err != nil {
			t.Fatal(err)
		}
	})
	if e.hash(ym(2026, 4)) != netted {
		t.Fatal("confirming changes the label, not the math")
	}
	var undo events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if undo, err = e.lnk.Undo(context.Background(), tx, events.ActorUser, created.ID); err != nil {
			t.Fatal(err)
		}
	})
	if e.hash(ym(2026, 4)) != base {
		t.Fatal("undoing the creation removes the netting")
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.lnk.Redo(context.Background(), tx, events.ActorUser, undo.ID); err != nil {
			t.Fatal(err)
		}
	})
	if e.hash(ym(2026, 4)) != netted {
		t.Fatal("redo puts it back")
	}
}

func TestS12_SplitPartNetsOnlyItsPart(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 10000, "COSTCO")
	e.tx(func(tx *sql.Tx) {
		a, b := "groceries", "home_household"
		if _, err := e.led.Apply(context.Background(), tx, events.ActorUser, dinner, ledger.KindSplit, ledger.Split{TxnID: dinner, Parts: []ledger.SplitPart{{CategoryID: a, Amount: -6000}, {CategoryID: b, Amount: -4000}}}, ""); err != nil {
			t.Fatal(err)
		}
	})
	back := e.deposit("2026-04-25", 9000, "VENMO")
	before := e.snap(ym(2026, 4))
	part := 2
	if _, err := e.link(links.Params{PaymentID: back, ExpenseID: dinner, PartNo: &part, Amount: 9000}); !errors.Is(err, links.ErrExceeds) {
		t.Fatalf("part 2 is only $40: %v", err)
	}
	if _, err := e.link(links.Params{PaymentID: back, ExpenseID: dinner, PartNo: &part, Amount: 4000}); err != nil {
		t.Fatal(err)
	}
	if got := e.snap(ym(2026, 4)).SpendLeft - before.SpendLeft; got != 4000 {
		t.Fatalf("only the part nets: %d", got)
	}
}
