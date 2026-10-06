//go:build integration

package receivables_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/manual"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	led  *ledger.Service
	rec  *recompute.Service
	svc  *receivables.Service
	cps  *counterparties.Service
	lnk  *links.Service
	acct string
	now  time.Time
}

func ym(y int, m time.Month) civil.YearMonth { return civil.YearMonth{Y: y, M: m} }

func setup(t *testing.T) *env {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	e := &env{t: t, db: db, led: led, now: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)}
	e.rec = recompute.New(db, reg, led, time.UTC)
	e.rec.Now = func() time.Time { return e.now }
	e.lnk = links.New(db, reg)
	e.cps = counterparties.New(db, reg)
	e.svc = receivables.New(db, reg, e.lnk, e.cps, manual.New(db, reg, led))
	e.svc.Now = func() time.Time { return e.now }
	e.acct = testkit.Account(t, db, "checking")
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

func (e *env) buy(date string, cents int64) string {
	id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: -cents, Description: "RESTAURANT"})
	e.set(id, "dining_out")
	return id
}

func (e *env) deposit(date string, cents int64) string {
	return testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: cents, Description: "VENMO"})
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

func (e *env) create(p receivables.Params) receivables.Receivable {
	var r receivables.Receivable
	e.tx(func(tx *sql.Tx) {
		var err error
		if r, err = e.svc.Create(context.Background(), tx, events.ActorUser, p); err != nil {
			e.t.Fatal(err)
		}
	})
	return r
}

func (e *env) get(id string) receivables.Receivable {
	r, err := e.svc.Get(context.Background(), e.db, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) spendLeft(m civil.YearMonth) budget.MonthState {
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

func (e *env) receive(id, payment string, amount int64) {
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Receive(context.Background(), tx, events.ActorUser, id, payment, amount); err != nil {
			e.t.Fatal(err)
		}
	})
}

func TestS12a_ReceivableCountsInFullUntilReceived(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 2240)
	base := e.spendLeft(ym(2026, 4))
	r := e.create(receivables.Params{ExpenseID: dinner, Amount: 1100})
	if r.Status != receivables.Expected || r.NettedNow {
		t.Fatalf("%+v", r)
	}
	if got := e.spendLeft(ym(2026, 4)); got.SpendLeft != base.SpendLeft {
		t.Fatalf("it counts in full until the money lands: %v vs %v", got.SpendLeft, base.SpendLeft)
	}
	pay := e.deposit("2026-04-22", 1100)
	e.receive(r.ID, pay, 0)
	if got := e.get(r.ID); got.Status != receivables.Received || got.ReceivedAmount != 1100 {
		t.Fatalf("%+v", got)
	}
	if got := e.spendLeft(ym(2026, 4)); got.SpendLeft-base.SpendLeft != 1100 {
		t.Fatalf("now it nets in the dinner's month: %v", got.SpendLeft-base.SpendLeft)
	}
	if got := e.spendLeft(ym(2026, 5)); got.Income != e.spendLeft(ym(2026, 5)).Income {
		t.Fatal("stable")
	}
}

func TestS12a_NettedNowThenWriteOff(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 4000)
	base := e.spendLeft(ym(2026, 4))
	yes := true
	r := e.create(receivables.Params{ExpenseID: dinner, Amount: 2000, NettedNow: &yes})
	if got := e.spendLeft(ym(2026, 4)); got.SpendLeft-base.SpendLeft != 2000 {
		t.Fatalf("netted now: the money is trusted: %v", got.SpendLeft-base.SpendLeft)
	}
	var wo events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if _, err = e.svc.WriteOff(context.Background(), tx, events.ActorUser, r.ID); err != nil {
			t.Fatal(err)
		}
		tx.QueryRowContext(context.Background(), `SELECT max(id) FROM events`).Scan(&wo.ID)
	})
	got := e.get(r.ID)
	if got.Status != receivables.WrittenOff || got.Unrecovered() != 2000 {
		t.Fatalf("the unrecovered amount is shown: %+v", got)
	}
	if after := e.spendLeft(ym(2026, 4)); after.SpendLeft != base.SpendLeft {
		t.Fatalf("written off, the expense counts in full again: %v vs %v", after.SpendLeft, base.SpendLeft)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Undo(context.Background(), tx, events.ActorUser, wo.ID); err != nil {
			t.Fatal(err)
		}
	})
	if e.get(r.ID).WrittenOff {
		t.Fatal("undoable")
	}
	if again := e.spendLeft(ym(2026, 4)); again.SpendLeft-base.SpendLeft != 2000 {
		t.Fatal("undo restores the netting")
	}
	// a written-off receivable of a payer set to auto-net: the flag follows the payer
	e.tx(func(tx *sql.Tx) {
		c, _ := e.cps.Create(context.Background(), tx, events.ActorUser, "Dad", "ZELLE FROM DAD")
		if err := e.cps.SetAutoNet(context.Background(), tx, events.ActorUser, c.ID, true); err != nil {
			t.Fatal(err)
		}
		other := e.buyTx(tx)
		rr, err := e.svc.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: other, Amount: 500, CounterpartyID: &c.ID})
		if err != nil || !rr.NettedNow {
			t.Fatalf("auto-net payers net immediately: %+v %v", rr, err)
		}
	})
}

func (e *env) buyTx(tx *sql.Tx) string {
	id := testkit.RawTxn(e.t, tx, e.acct, testkit.Raw{PostedOn: "2026-04-21", Amount: -3000, Description: "LUNCH"})
	return id
}

func TestS12a_E5_TwoInstallments(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 6000)
	r := e.create(receivables.Params{ExpenseID: dinner, Amount: 3000})
	e.receive(r.ID, e.deposit("2026-04-25", 1000), 0)
	if got := e.get(r.ID); got.Status != receivables.Partial || got.ReceivedAmount != 1000 || !got.Open() {
		t.Fatalf("partially received: %+v", got)
	}
	e.receive(r.ID, e.deposit("2026-05-02", 2000), 0)
	if got := e.get(r.ID); got.Status != receivables.Received || got.Open() {
		t.Fatalf("received: %+v", got)
	}
}

func TestS12a_E6_OnePaymentTwoExpenses(t *testing.T) {
	e := setup(t)
	a := e.buy("2026-04-10", 4000)
	b := e.buy("2026-04-12", 3000)
	ra := e.create(receivables.Params{ExpenseID: a, Amount: 2000})
	rb := e.create(receivables.Params{ExpenseID: b, Amount: 1500})
	pay := e.deposit("2026-04-15", 3500)
	base := e.spendLeft(ym(2026, 4))
	e.receive(ra.ID, pay, 0)
	e.receive(rb.ID, pay, 0)
	if e.get(ra.ID).Status != receivables.Received || e.get(rb.ID).Status != receivables.Received {
		t.Fatal("both are paid by one payment")
	}
	if got := e.spendLeft(ym(2026, 4)); got.SpendLeft-base.SpendLeft != 3500 {
		t.Fatalf("both expenses net: %d", got.SpendLeft-base.SpendLeft)
	}
	var payments int
	e.db.QueryRow(`SELECT count(DISTINCT payment_txn_id) FROM links`).Scan(&payments)
	if payments != 1 {
		t.Fatal("many-to-many: one payment, two links")
	}
}

func TestS12a_E8_AgingAlerts(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 4000)
	r := e.create(receivables.Params{ExpenseID: dinner, Amount: 2000})
	check := func(days, want int) {
		t.Helper()
		e.now = time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).AddDate(0, 0, days)
		al, err := e.svc.Aging(context.Background(), e.db, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		if len(al) == 1 {
			got = al[0].Threshold
		}
		if got != want {
			t.Fatalf("day %d: threshold %d want %d", days, got, want)
		}
	}
	check(13, 0)
	check(14, 14)
	check(31, 30)
	check(75, 60)
	if !e.get(r.ID).Overdue {
		t.Fatal("overdue is derived from age")
	}
	// personalised: a payer who always pays in 2 days is nudged on day 5
	e.now = time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 5)
	al, _ := e.svc.Aging(context.Background(), e.db, func(*string) []int { return []int{5} })
	if len(al) != 1 || al[0].Threshold != 5 {
		t.Fatalf("%+v", al)
	}
	e.receive(r.ID, e.deposit("2026-05-20", 2000), 0)
	if al, _ := e.svc.Aging(context.Background(), e.db, nil); len(al) != 0 {
		t.Fatal("a paid receivable raises no alert")
	}
}

func TestS12a_E3_CashMarkReceived(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 4000)
	r := e.create(receivables.Params{ExpenseID: dinner, Amount: 2000})
	base := e.spendLeft(ym(2026, 4))
	e.tx(func(tx *sql.Tx) {
		l, txn, err := e.svc.MarkReceivedCash(context.Background(), tx, events.ActorUser, r.ID, "", civil.Date{Y: 2026, M: 4, D: 22}, 0)
		if err != nil || l.State != links.Confirmed || txn == "" {
			t.Fatalf("%+v %v", l, err)
		}
	})
	if e.get(r.ID).Status != receivables.Received {
		t.Fatal("cash is received")
	}
	var manualRows int
	e.db.QueryRow(`SELECT count(*) FROM raw_transactions WHERE provider_txn_id LIKE 'manual:%'`).Scan(&manualRows)
	if manualRows != 1 {
		t.Fatalf("the cash is a manual transaction (merged later per S11): %d", manualRows)
	}
	if got := e.spendLeft(ym(2026, 4)); got.SpendLeft-base.SpendLeft != 2000 {
		t.Fatalf("nets: %d", got.SpendLeft-base.SpendLeft)
	}
}

func TestS12a_CreateValidation(t *testing.T) {
	e := setup(t)
	dinner := e.buy("2026-04-20", 1000)
	for _, p := range []receivables.Params{{ExpenseID: dinner, Amount: 0}, {ExpenseID: dinner, Amount: 5000}} {
		tx, _ := e.db.Begin()
		if _, err := e.svc.Create(context.Background(), tx, events.ActorUser, p); err == nil {
			t.Fatalf("%+v should be refused", p)
		}
		tx.Rollback()
	}
}
