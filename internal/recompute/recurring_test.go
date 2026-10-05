//go:build integration

package recompute_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/money"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/recurrence"
	"github.com/alec-jensen/sofar/internal/recurring"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type renv struct {
	*env
	items *recurring.Service
}

func rsetup(t *testing.T) *renv {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	rec := recompute.New(db, reg, led, time.UTC)
	rec.Now = func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) }
	items := recurring.New(db, reg, led, time.UTC)
	items.Now = rec.Now
	return &renv{env: &env{t: t, db: db, led: led, rec: rec, acct: testkit.Account(t, db, "checking")}, items: items}
}

func (e *renv) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func monthly(day int, from string) recurrence.Schedule {
	return recurrence.Schedule{Kind: recurrence.KindCalendar, Calendar: recurrence.CalendarRule{Kind: recurrence.Monthly, Day: day}, EffectiveFrom: dd(from)}
}

func dd(s string) civil.Date {
	v, _ := civil.ParseDate(s)
	return v
}

// salary adds a reviewed monthly paycheck for each month.
func (e *renv) salary(amount int64, months ...string) {
	for _, m := range months {
		id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: m + "-01", Amount: amount, Description: "ACME PAYROLL"})
		e.set(id, "salary", true)
	}
}

func (e *renv) bill(kind recurrence.ItemKind, name string, amount money.Cents, day int, from string) recurring.Item {
	e.t.Helper()
	var it recurring.Item
	e.tx(func(tx *sql.Tx) {
		var err error
		it, err = e.items.Create(context.Background(), tx, events.ActorUser, recurring.Params{Kind: kind, Name: name, Amount: amount, Schedule: monthly(day, from), ActiveFrom: dd(from)})
		if err != nil {
			e.t.Fatal(err)
		}
	})
	return it
}

func (e *renv) pay(desc string, amount int64, date string) string {
	id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: date, Amount: -amount, Description: desc})
	return id
}

func TestS5_NewBillReducesPotThenBase(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02", "2026-03", "2026-04")
	e.salary(100000, "2026-04") // April income is 5000: May has a pot
	e.rec.Recompute(context.Background())
	may := e.snap(ym(2026, 5))
	if may.Pot != 100000 || may.SpendBase != 400000 {
		t.Fatalf("before the bill: pot 1000, base 4000: %+v", may)
	}
	e.bill(recurrence.ItemBill, "Landlord", 80000, 1, "2026-05-01")
	e.rec.Recompute(context.Background())
	may = e.snap(ym(2026, 5))
	if may.Fixed != 80000 || may.Pot != 20000 || may.SpendBase != 400000 {
		t.Fatalf("an 800 bill comes out of the pot first: %+v", may)
	}
	e.bill(recurrence.ItemBill, "Insurance", 150000, 15, "2026-05-01")
	e.rec.Recompute(context.Background())
	may = e.snap(ym(2026, 5))
	if may.Pot != 0 || may.SpendBase != 400000-(80000+150000-100000) || len(may.Absorption) != 1 {
		t.Fatalf("beyond the pot, spend_base absorbs: %+v", may)
	}
}

func TestS5_ReserveUsesLastActualAndBillConsumesIt(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02", "2026-03", "2026-04", "2026-05")
	e.bill(recurrence.ItemBill, "Landlord", 100000, 1, "2026-02-01")
	e.pay("LANDLORD", 110000, "2026-04-01")
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 5)).Fixed; got != 110000 {
		t.Fatalf("May's reserve follows April's actual: %v", got)
	}
	e.pay("LANDLORD", 110000, "2026-05-01")
	e.rec.Recompute(context.Background())
	may := e.snap(ym(2026, 5))
	if may.SpendLeft != may.SpendPool || may.BillsUnderage != 0 {
		t.Fatalf("paying the bill uses its reserve, not the spending pool: %+v", may)
	}
}

func TestS5_CancelledBillFreesIntoPot(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02", "2026-03", "2026-04", "2026-05")
	it := e.bill(recurrence.ItemBill, "Gym Co", 50000, 3, "2026-02-01")
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 4)).Fixed; got != 50000 {
		t.Fatalf("reserved: %v", got)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.items.EndItem(context.Background(), tx, events.ActorUser, it.ID, dd("2026-04-30")); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(context.Background())
	may := e.snap(ym(2026, 5))
	if may.Fixed != 0 || may.Pot != 50000 {
		t.Fatalf("a cancelled bill frees its reserve into the pot: %+v", may)
	}
}

func TestS6_SubscriptionChargeUsesReserveNotPool(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02", "2026-03", "2026-04", "2026-05")
	e.bill(recurrence.ItemSubscription, "Netflix", 1549, 12, "2026-02-01")
	e.pay("NETFLIX", 1549, "2026-05-02")
	e.rec.Recompute(context.Background())
	may := e.snap(ym(2026, 5))
	if may.Fixed != 1549 || may.SpendLeft != may.SpendPool {
		t.Fatalf("the charge consumes the subscription reserve: %+v", may)
	}
	if may.BillsUnderage != 0 {
		t.Fatalf("nothing left over: %+v", may)
	}
}

func TestS19_9_PaymentPlanCompletesAndFreesInRecompute(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-01", "2026-02", "2026-03", "2026-04", "2026-05")
	var plan recurring.Item
	e.tx(func(tx *sql.Tx) {
		var err error
		plan, err = e.items.Create(context.Background(), tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemPaymentPlan, Name: "Laptop", Amount: 20000,
			Schedule: monthly(5, "2026-02-01"), ActiveFrom: dd("2026-02-01"), InstallmentsTotal: 3})
		if err != nil {
			t.Fatal(err)
		}
	})
	_ = plan
	for _, m := range []string{"2026-02", "2026-03", "2026-04"} {
		e.pay("LAPTOP", 20000, m+"-05")
	}
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 4)).Fixed; got != 20000 {
		t.Fatalf("the third installment is still reserved in April: %v", got)
	}
	if got := e.snap(ym(2026, 5)).Fixed; got != 0 {
		t.Fatalf("three installments paid: May has no reserve: %v", got)
	}
}

func TestS4_ScheduledDepositCountsProvisionally(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-03")
	// April's paycheck is unreviewed and uncategorized, but it matches the salary stream
	unreviewed := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-01", Amount: 400000, Description: "ACME PAYROLL"})
	_ = unreviewed
	e.tx(func(tx *sql.Tx) {
		if _, err := e.items.Create(context.Background(), tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemIncome, Name: "ACME PAYROLL", Amount: 400000, Schedule: monthly(1, "2026-03-01"), ActiveFrom: dd("2026-03-01")}); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 5)).Plan; got != 400000 {
		t.Fatalf("an unreviewed deposit that matches a schedule counts provisionally: %v", got)
	}
}

func TestS4_DepositTypeIntoTrackedAccountCounts(t *testing.T) {
	e := rsetup(t)
	tracked := testkit.AccountOfKind(t, e.db, "savings", "tracked")
	e.salary(300000, "2026-03")
	dep := testkit.RawTxn(t, e.db, tracked, testkit.Raw{PostedOn: "2026-04-02", Amount: 100000, Description: "SPLIT DEPOSIT"})
	e.set(dep, "salary", true)
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 5)).Plan; got != 100000 {
		t.Fatalf("a direct deposit into a tracked account is income: %v", got)
	}
}

func TestS4_ReversalNetsAgainstOriginalMonth(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02", "2026-03")
	rev := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-10", Amount: -150000, Description: "ACME PAYROLL REVERSAL"})
	_ = rev
	// the reversal's merchant text differs; link by the exact payer name instead
	rev2 := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-10", Amount: -150000, Description: "ACME PAYROLL"})
	e.set(rev2, "salary", true)
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 3)).Plan; got != 400000 {
		t.Fatalf("March's plan comes from February: %v", got)
	}
	if got := e.snap(ym(2026, 4)).Plan; got != 250000 {
		t.Fatalf("the reversal reduces March's income, so April's plan: %v", got)
	}
}

func TestS3_BoundaryAttributionMonthlyPaycheck(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-02")
	// the March paycheck is due on the 1st but lands on Feb 27 (a weekend), and the stream knows the 1st
	early := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-02-27", Amount: 400000, Description: "ACME PAYROLL"})
	e.set(early, "salary", true)
	e.tx(func(tx *sql.Tx) {
		if _, err := e.items.Create(context.Background(), tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemIncome, Name: "ACME PAYROLL", Amount: 400000, Schedule: monthly(1, "2026-01-01"), ActiveFrom: dd("2026-01-01")}); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(context.Background())
	// Feb 1 counts in February; Feb 27 belongs to March (nominal Mar 1, within tolerance)
	if got := e.snap(ym(2026, 3)).Plan; got != 400000 {
		t.Fatalf("February's income is only the Feb 1 paycheck: %v", got)
	}
	if got := e.snap(ym(2026, 4)).Plan; got != 400000 {
		t.Fatalf("the Feb 27 paycheck counts in March: %v", got)
	}
	// a user month override wins over attribution
	e.tx(func(tx *sql.Tx) {
		m := "2026-02"
		if _, err := e.led.Apply(context.Background(), tx, events.ActorUser, early, ledger.KindSetMonth, ledger.SetMonth{TxnID: early, Month: &m}, ""); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 3)).Plan; got != 800000 {
		t.Fatalf("the override puts it back in February: %v", got)
	}
}

func TestS4_UnknownDepositHeldOutAndReported(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-04")
	venmo := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-20", Amount: 90000, Description: "VENMO CASHOUT"})
	_ = venmo
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 5)).Plan; got != 400000 {
		t.Fatalf("held out of the plan: %v", got)
	}
	held, err := e.rec.Held(context.Background(), e.db, ym(2026, 4))
	if err != nil || held != 90000 {
		t.Fatalf("held = %v %v", held, err)
	}
}

func TestS4_DropLabeledExpectedOrUnexpected(t *testing.T) {
	e := rsetup(t)
	e.salary(400000, "2026-01", "2026-02")
	// March: no paycheck arrives although the stream expected one
	e.tx(func(tx *sql.Tx) {
		if _, err := e.items.Create(context.Background(), tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemIncome, Name: "ACME PAYROLL", Amount: 400000, Schedule: monthly(1, "2026-01-01"), ActiveFrom: dd("2026-01-01")}); err != nil {
			t.Fatal(err)
		}
	})
	e.salary(400000, "2026-04", "2026-05")
	e.rec.Recompute(context.Background())
	apr := e.snap(ym(2026, 4)) // plan = March income = 0
	if apr.Plan != 0 || len(apr.Absorption) != 1 || len(apr.Absorption[0].Causes) == 0 || apr.Absorption[0].Causes[0].Kind != "plan_dropped_unexpected" {
		t.Fatalf("a missing paycheck is an unexpected drop: %+v", apr)
	}
}

// TestA9_Inv8_TransactionCountsInIncomeAtMostOnce checks that across every month a
// transaction appears in income at most once.
func TestA9_Inv8_TransactionCountsInIncomeAtMostOnce(t *testing.T) {
	e := rsetup(t)
	ctx := context.Background()
	e.tx(func(tx *sql.Tx) {
		if _, err := e.items.Create(ctx, tx, events.ActorUser, recurring.Params{Kind: recurrence.ItemIncome, Name: "ACME PAYROLL", Amount: 400000, Schedule: monthly(1, "2026-01-01"), ActiveFrom: dd("2026-01-01")}); err != nil {
			t.Fatal(err)
		}
	})
	rapid.Check(t, func(rt *rapid.T) {
		e.db.Exec(`DELETE FROM month_snapshots; DELETE FROM dirty_months; DELETE FROM events WHERE kind <> 'recurring.create'; DELETE FROM txn_splits; DELETE FROM transaction_overlays; DELETE FROM hidden_tombstones; DELETE FROM raw_transactions`)
		n := rapid.IntRange(1, 10).Draw(rt, "n")
		for i := 0; i < n; i++ {
			day := rapid.IntRange(1, 28).Draw(rt, "day")
			mon := rapid.IntRange(1, 5).Draw(rt, "mon")
			amt := int64(rapid.IntRange(-100000, 400000).Draw(rt, "amt"))
			if amt == 0 {
				amt = 5
			}
			id := testkit.RawTxn(rt, e.db, e.acct, testkit.Raw{PostedOn: fmtDate(mon, day), Amount: amt, Description: "ACME PAYROLL"})
			cat := rapid.SampledFrom([]string{"salary", "gift_other", "groceries", "transfer"}).Draw(rt, "cat")
			tx, _ := e.db.Begin()
			e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &cat}, "")
			if rapid.Bool().Draw(rt, "reviewed") {
				e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true}, "")
			}
			tx.Commit()
		}
		d, err := e.rec.Assemble(ctx, e.db, ym(2026, 1))
		_ = d
		if err != nil {
			rt.Fatal(err)
		}
		seen := map[string]int{}
		for m := ym(2026, 1); !ym(2026, 6).Before(m); m = m.Next() {
			in, err := e.rec.Assemble(ctx, e.db, m)
			if err != nil {
				rt.Fatal(err)
			}
			for _, it := range in.Income {
				seen[it.ID]++
				if seen[it.ID] > 1 {
					rt.Fatalf("transaction %s counts in income more than once", it.ID)
				}
			}
		}
	})
	var _ budget.MonthInputs
}

func fmtDate(m, d int) string {
	return time.Date(2026, time.Month(m), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}
