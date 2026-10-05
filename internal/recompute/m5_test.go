//go:build integration

package recompute_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/budgetcfg"
	"github.com/alec-jensen/sofar/internal/categories"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type menv struct {
	*env
	cfg *budgetcfg.Service
}

func msetup(t *testing.T) *menv {
	t.Helper()
	db := testkit.DB(t)
	reg := events.NewRegistry()
	led := ledger.New(db, reg)
	rec := recompute.New(db, reg, led, time.UTC)
	rec.Now = func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) }
	cfg := budgetcfg.New(db, reg, categories.New(db, led, reg), time.UTC)
	cfg.Now = rec.Now
	e := &menv{env: &env{t: t, db: db, led: led, rec: rec, acct: testkit.Account(t, db, "checking")}, cfg: cfg}
	return e
}

func (e *menv) tx(fn func(tx *sql.Tx)) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *menv) paychecks(months ...string) {
	for _, m := range months {
		id := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: m + "-01", Amount: 400000, Description: "ACME PAYROLL"})
		e.set(id, "salary", true)
	}
}

func (e *menv) event(ymonth string, be recompute.BudgetEvent) events.Event {
	e.t.Helper()
	var ev events.Event
	e.tx(func(tx *sql.Tx) {
		var err error
		if ev, err = e.rec.RecordEvent(context.Background(), tx, events.ActorUser, ym2(ymonth), be); err != nil {
			e.t.Fatal(err)
		}
	})
	return ev
}

func ym2(s string) civil.YearMonth {
	m, err := civil.ParseYearMonth(s)
	if err != nil {
		panic(err)
	}
	return m
}

func TestS7_VerifiedByMatchingTransfer(t *testing.T) {
	e := msetup(t)
	ctx := context.Background()
	e.paychecks("2026-01", "2026-02", "2026-03", "2026-04")
	e.tx(func(tx *sql.Tx) {
		if _, err := e.cfg.SetSavings(ctx, tx, events.ActorUser, ym(2026, 2), budget.SavingsRule{Fixed: 30000}); err != nil {
			t.Fatal(err)
		}
	})
	transfer := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-03-05", Amount: -30000, Description: "TRANSFER TO SAVINGS"})
	e.set(transfer, "general_savings", true)
	e.rec.Recompute(ctx)
	if m := e.snap(ym(2026, 3)); m.Has(budget.FlagUnverifiedSavings) || m.SavingsVerified != 30000 || m.Envelopes[budget.EnvSavings] < 30000 {
		t.Fatalf("a matching transfer verifies March: %+v", m.Flags)
	}
	if !e.snap(ym(2026, 4)).Has(budget.FlagUnverifiedSavings) {
		t.Fatal("April has no transfer: flagged")
	}
	e.event("2026-04", recompute.BudgetEvent{Kind: string(budget.EvMovedIt), Amount: 30000})
	e.rec.Recompute(ctx)
	if e.snap(ym(2026, 4)).Has(budget.FlagUnverifiedSavings) {
		t.Fatal("i moved it clears the flag")
	}
	// a rule change applies from its month and never rewrites earlier ones
	if got := e.snap(ym(2026, 1)).Fixed; got != 0 {
		t.Fatalf("January predates the rule: %v", got)
	}
}

func TestS7_PercentRuleFollowsPlanInRecompute(t *testing.T) {
	e := msetup(t)
	ctx := context.Background()
	e.paychecks("2026-02", "2026-03")
	e.tx(func(tx *sql.Tx) {
		if _, err := e.cfg.SetSavings(ctx, tx, events.ActorUser, ym(2026, 1), budget.SavingsRule{UseBasisPoints: true, BasisPoints: 1000}); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(ctx)
	if got := e.snap(ym(2026, 4)).Fixed; got != 40000 {
		t.Fatalf("10%% of a 4000 plan: %v", got)
	}
}

func TestS7_GoalsAccumulateAndStop(t *testing.T) {
	e := msetup(t)
	ctx := context.Background()
	e.paychecks("2026-01", "2026-02", "2026-03", "2026-04", "2026-05")
	var g budgetcfg.Goal
	e.tx(func(tx *sql.Tx) {
		var err error
		if g, err = e.cfg.CreateGoal(ctx, tx, events.ActorUser, budgetcfg.GoalParams{Name: "Phone", Target: 100000, Monthly: 20000}); err != nil {
			t.Fatal(err)
		}
	})
	if g.CategoryID == "" {
		t.Fatal("a goal gets its own category")
	}
	e.rec.Recompute(ctx)
	may := e.snap(ym(2026, 5))
	if may.Envelopes[budget.Goal(g.ID)] != 20000 || may.Fixed != 20000 {
		t.Fatalf("one month of contributions: %+v", may.Envelopes)
	}
	// pause from this month: the line stops, the balance stays
	e.tx(func(tx *sql.Tx) {
		paused := true
		if _, err := e.cfg.EditGoal(ctx, tx, events.ActorUser, g.ID, budgetcfg.GoalEdit{Paused: &paused}); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(ctx)
	if m := e.snap(ym(2026, 5)); m.Envelopes[budget.Goal(g.ID)] != 0 || m.Fixed != 0 {
		t.Fatalf("paused stops the line: %+v", m.Envelopes)
	}
}

func TestS7_PurchaseDrawsEnvelopeExcessIsSpending(t *testing.T) {
	e := msetup(t)
	ctx := context.Background()
	e.paychecks("2026-01", "2026-02", "2026-03", "2026-04")
	var g budgetcfg.Goal
	e.tx(func(tx *sql.Tx) {
		g, _ = e.cfg.CreateGoal(ctx, tx, events.ActorUser, budgetcfg.GoalParams{Name: "Laptop", Target: 100000, Monthly: 10000})
	})
	e.event("2026-05", recompute.BudgetEvent{Kind: string(budget.EvOpening), Dest: string(budget.Goal(g.ID)), Amount: 50000})
	buy := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-05-04", Amount: -80000, Description: "BEST BUY"})
	e.set(buy, "shopping", true)
	e.tx(func(tx *sql.Tx) {
		if _, err := e.led.Apply(ctx, tx, events.ActorUser, buy, ledger.KindSetType, ledger.SetType{TxnID: buy, Type: "goal:" + g.ID}, ""); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(ctx)
	may := e.snap(ym(2026, 5))
	// envelope: 50000 opening + 10000 line = 60000; purchase 80000 draws it all, 20000 counts as spending
	if may.Envelopes[budget.Goal(g.ID)] != 0 || may.SpendLeft != may.SpendPool-20000 {
		t.Fatalf("goal %v, spend_left %v of pool %v", may.Envelopes[budget.Goal(g.ID)], may.SpendLeft, may.SpendPool)
	}
}

func TestS8_EFTargetAndRebuildLine(t *testing.T) {
	e := msetup(t)
	ctx := context.Background()
	e.paychecks("2026-01", "2026-02", "2026-03", "2026-04", "2026-05")
	for _, m := range []string{"2026-02", "2026-03", "2026-04"} {
		b := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: m + "-10", Amount: -100000, Description: "GROCERY"})
		e.set(b, "groceries", true)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.cfg.SetEF(ctx, tx, events.ActorUser, ym(2026, 1), budgetcfg.EFConfig{Months: 3, Line: 10000}); err != nil {
			t.Fatal(err)
		}
	})
	e.rec.Recompute(ctx)
	may := e.snap(ym(2026, 5))
	if may.EFTarget != 300000 {
		t.Fatalf("3 months of 1000 spending: %v", may.EFTarget)
	}
	// withdrawing 600 for an overspend creates a rebuild line over 6 months, redirecting savings first
	e.tx(func(tx *sql.Tx) {
		if _, err := e.cfg.SetSavings(ctx, tx, events.ActorUser, ym(2026, 5), budget.SavingsRule{Fixed: 15000}); err != nil {
			t.Fatal(err)
		}
	})
	e.event("2026-04", recompute.BudgetEvent{Kind: recompute.KindRebuildLine, Amount: 60000, Months: 6})
	e.rec.Recompute(ctx)
	jun := e.snap(ym(2026, 5))
	// rebuild 100/month: 100 of the 150 savings line is redirected, nothing extra reduces spending
	if jun.Envelopes[budget.EnvEF] < 10000+10000 {
		t.Fatalf("the EF is credited its line and the rebuild: %v", jun.Envelopes[budget.EnvEF])
	}
	if jun.Has(budget.FlagRebuildFromFixed) {
		t.Fatalf("savings covered the rebuild, so spending is untouched: %+v", jun.Flags)
	}
}
