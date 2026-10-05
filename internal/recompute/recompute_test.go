//go:build integration

package recompute_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/alec-jensen/sofar/internal/budget"
	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/ledger"
	"github.com/alec-jensen/sofar/internal/recompute"
	"github.com/alec-jensen/sofar/internal/testkit"
)

type env struct {
	t    *testing.T
	db   *sql.DB
	led  *ledger.Service
	rec  *recompute.Service
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
	return &env{t: t, db: db, led: led, rec: rec, acct: testkit.Account(t, db, "checking")}
}

func (e *env) set(txn, category string, reviewed bool) {
	e.t.Helper()
	tx, _ := e.db.Begin()
	defer tx.Rollback()
	ctx := context.Background()
	if _, err := e.led.Apply(ctx, tx, events.ActorUser, txn, ledger.KindSetCategory, ledger.SetCategory{TxnID: txn, CategoryID: &category}, ""); err != nil {
		e.t.Fatal(err)
	}
	if reviewed {
		if _, err := e.led.Apply(ctx, tx, events.ActorUser, txn, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: txn, Reviewed: true}, ""); err != nil {
			e.t.Fatal(err)
		}
	}
	tx.Commit()
}

// seed adds salary in Jan to Apr and some spending, categorized and reviewed.
func (e *env) seed() {
	for i, m := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"} {
		pay := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: m + "-01", Amount: 400000, Description: "PAYROLL"})
		e.set(pay, "salary", true)
		buy := testkit.RawTxn(e.t, e.db, e.acct, testkit.Raw{PostedOn: m + "-05", Amount: -int64(10000 + 1000*i), Description: "GROCERY"})
		e.set(buy, "groceries", true)
	}
}

func (e *env) snap(m civil.YearMonth) budget.MonthState {
	e.t.Helper()
	s, err := e.rec.GetSnapshot(context.Background(), e.db, m)
	if err != nil {
		e.t.Fatalf("snapshot %v: %v", m, err)
	}
	return s.State
}

func (e *env) hashes() []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT month::text || ':' || input_hash FROM month_snapshots ORDER BY month`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestS3_RecomputeBuildsTheChain(t *testing.T) {
	e := setup(t)
	e.seed()
	res, err := e.rec.Recompute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Computed) != 5 {
		t.Fatalf("computed %v", res.Computed)
	}
	jan, feb, may := e.snap(ym(2026, 1)), e.snap(ym(2026, 2)), e.snap(ym(2026, 5))
	if !jan.Learning || jan.Plan != 0 {
		t.Errorf("the first month has no earlier data: learning with no plan, got %+v", jan)
	}
	if feb.Learning || feb.Plan != 400000 {
		t.Errorf("feb: plan is January's reviewed salary: %+v", feb)
	}
	if may.Plan != 400000 || may.SpendLeft != may.SpendPool-14000 {
		t.Errorf("may: plan 4000, spent 140: %+v", may)
	}
	if may.PerDay == 0 {
		t.Errorf("the current month has a per-day figure: %+v", may)
	}
	if feb.PerDay != 0 && feb.LastDayExtra != 0 {
		t.Errorf("past months have no per-day: %+v", feb)
	}
}

func TestA9_Inv6_RecomputeTwiceSame(t *testing.T) {
	e := setup(t)
	e.seed()
	e.rec.Recompute(context.Background())
	first := e.hashes()
	if _, err := e.rec.RecomputeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first) != fmt.Sprint(e.hashes()) {
		t.Fatal("recomputing twice must give the same snapshots")
	}
}

func TestRecompute_EarlyCutoffStopsChain(t *testing.T) {
	e := setup(t)
	e.seed()
	ctx := context.Background()
	e.rec.Recompute(ctx)
	// a note on a January transaction marks Jan and Feb dirty but changes no number
	p, _ := e.led.View(ctx, ledger.Filter{Month: &civil.YearMonth{Y: 2026, M: 1}})
	tx, _ := e.db.Begin()
	if _, err := e.led.Apply(ctx, tx, events.ActorUser, p.Items[0].ID, ledger.KindSetNote, ledger.SetNote{TxnID: p.Items[0].ID, Note: "hello"}, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	res, err := e.rec.Recompute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stopped || len(res.Computed) != 4 || res.Computed[3] != ym(2026, 5) {
		t.Fatalf("expected Jan to Mar (the dirty marks), then straight to the current month, got %+v", res)
	}
	var dirty int
	e.db.QueryRow(`SELECT count(*) FROM dirty_months`).Scan(&dirty)
	if dirty != 0 {
		t.Errorf("dirty marks are cleared: %d", dirty)
	}
}

func TestS3_LateTransactionRecomputesItsMonthAndNext(t *testing.T) {
	e := setup(t)
	e.seed()
	ctx := context.Background()
	e.rec.Recompute(ctx)
	before := e.snap(ym(2026, 3))
	late := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-02-20", Amount: 200000, Description: "BONUS"})
	e.set(late, "one_time_income", true)
	res, err := e.rec.Recompute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Computed) < 2 || res.Computed[0] != ym(2026, 2) {
		t.Fatalf("a late February deposit recomputes February and what follows: %+v", res)
	}
	after := e.snap(ym(2026, 3))
	if after.Plan != before.Plan+200000 {
		t.Errorf("March's plan follows February's income: %v -> %v", before.Plan, after.Plan)
	}
	if after.Pot != 200000 {
		t.Errorf("the extra lands in the pot: %v", after.Pot)
	}
	if e.snap(ym(2026, 4)).Plan != 400000 {
		t.Errorf("April is back to normal")
	}
}

func TestS3_UnreviewedDepositIsHeldOut(t *testing.T) {
	e := setup(t)
	pay := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-01", Amount: 400000})
	e.set(pay, "salary", true)
	venmo := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-04-15", Amount: 90000, Description: "VENMO"})
	e.set(venmo, "gift_other", false) // categorized but not reviewed
	_ = testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-01-01", Amount: -100})
	e.rec.Recompute(context.Background())
	if got := e.snap(ym(2026, 5)).Plan; got != 400000 {
		t.Errorf("an unreviewed deposit does not inflate next month's plan: %v", got)
	}
}

func TestS4_OverrideUsedAndNoted(t *testing.T) {
	e := setup(t)
	e.seed()
	ctx := context.Background()
	e.rec.Recompute(ctx)
	tx, _ := e.db.Begin()
	if _, err := e.rec.SetPlanOverride(ctx, tx, events.ActorUser, ym(2026, 3), 250000, "partial data"); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	e.rec.Recompute(ctx)
	if got := e.snap(ym(2026, 3)).Plan; got != 250000 {
		t.Fatalf("override wins: %v", got)
	}
	// the next month's delta uses the effective (overridden) plan: April's plan is still March's income
	if got := e.snap(ym(2026, 4)).Plan; got != 400000 {
		t.Fatalf("april plan = I(march): %v", got)
	}
	if e.snap(ym(2026, 3)).SpendBase != 250000 && e.snap(ym(2026, 3)).Absorption == nil {
		t.Errorf("a lower plan absorbs: %+v", e.snap(ym(2026, 3)))
	}
	// undo removes it
	var id int64
	e.db.QueryRow(`SELECT id FROM events WHERE kind='plan.override'`).Scan(&id)
	tx, _ = e.db.Begin()
	if _, err := events.Undo(ctx, tx, events.ActorUser, id, ""); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if _, err := e.rec.RecomputeFrom(ctx, ym(2026, 3)); err != nil {
		t.Fatal(err)
	}
	if got := e.snap(ym(2026, 3)).Plan; got != 400000 {
		t.Errorf("undo restores the data-driven plan: %v", got)
	}
}

func TestS18_LearningUntilCompletePriorMonth(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	buy := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-05-03", Amount: -2000})
	e.set(buy, "groceries", true)
	e.rec.Recompute(ctx)
	may := e.snap(ym(2026, 5))
	if !may.Learning || may.Plan != 0 || !may.Has(budget.FlagLearning) {
		t.Fatalf("data started this month: learning, got %+v", may)
	}
}

func TestS18_ManualStartingPlanEndsLearning(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	buy := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-05-03", Amount: -2000})
	e.set(buy, "groceries", true)
	tx, _ := e.db.Begin()
	e.rec.SetPlanOverride(ctx, tx, events.ActorUser, ym(2026, 5), 300000, "starting plan")
	tx.Commit()
	e.rec.Recompute(ctx)
	may := e.snap(ym(2026, 5))
	if may.Learning || may.Plan != 300000 || may.SpendBase != 300000 {
		t.Fatalf("a starting plan ends learning: %+v", may)
	}
}

func TestS13_PartialMonthLabeled(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	other := testkit.Account(t, e.db, "savings-ish")
	old := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: "2026-02-03", Amount: -500})
	e.set(old, "groceries", true)
	recent := testkit.RawTxn(t, e.db, other, testkit.Raw{PostedOn: "2026-05-02", Amount: -500})
	e.set(recent, "groceries", true)
	e.rec.Recompute(ctx)
	if may := e.snap(ym(2026, 5)); !may.Partial || may.Learning {
		t.Fatalf("one account has no data for April: partial, got %+v", may)
	}
}

// TestA9_Inv5_RecomputeFromScratchEqualsIncremental applies random changes,
// recomputing incrementally after each, then compares with a full recompute.
func TestA9_Inv5_RecomputeFromScratchEqualsIncremental(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cats := []string{"salary", "groceries", "dining_out", "rent_mortgage", "transfer", "one_time_income"}
	var txns []string
	rapid.Check(t, func(rt *rapid.T) {
		if _, err := e.db.Exec(`DELETE FROM month_snapshots; DELETE FROM dirty_months; DELETE FROM events; DELETE FROM txn_splits; DELETE FROM transaction_overlays; DELETE FROM hidden_tombstones; DELETE FROM raw_transactions`); err != nil {
			rt.Fatal(err)
		}
		txns = txns[:0]
		for i, n := 0, rapid.IntRange(1, 12).Draw(rt, "ops"); i < n; i++ {
			switch k := rapid.IntRange(0, 3).Draw(rt, "op"); {
			case k <= 1 || len(txns) == 0:
				day := rapid.IntRange(1, 28).Draw(rt, "day")
				mon := rapid.IntRange(1, 5).Draw(rt, "mon")
				amt := int64(rapid.IntRange(-50000, 90000).Draw(rt, "amt"))
				if amt == 0 {
					amt = 1
				}
				id := testkit.RawTxn(rt, e.db, e.acct, testkit.Raw{PostedOn: fmt.Sprintf("2026-%02d-%02d", mon, day), Amount: amt})
				txns = append(txns, id)
				cat := rapid.SampledFrom(cats).Draw(rt, "cat")
				tx, _ := e.db.Begin()
				e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetCategory, ledger.SetCategory{TxnID: id, CategoryID: &cat}, "")
				e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetReviewed, ledger.SetReviewed{TxnID: id, Reviewed: true}, "")
				tx.Commit()
			case k == 2:
				id := rapid.SampledFrom(txns).Draw(rt, "hide")
				tx, _ := e.db.Begin()
				e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindHide, ledger.Hide{TxnID: id}, "")
				tx.Commit()
			default:
				id := rapid.SampledFrom(txns).Draw(rt, "move")
				m := fmt.Sprintf("2026-%02d", rapid.IntRange(1, 5).Draw(rt, "to"))
				tx, _ := e.db.Begin()
				e.led.Apply(ctx, tx, events.ActorUser, id, ledger.KindSetMonth, ledger.SetMonth{TxnID: id, Month: &m}, "")
				tx.Commit()
			}
			if _, err := e.rec.Recompute(ctx); err != nil {
				rt.Fatal(err)
			}
		}
		incremental := e.hashes()
		if _, err := e.rec.RecomputeAll(ctx); err != nil {
			rt.Fatal(err)
		}
		if fmt.Sprint(incremental) != fmt.Sprint(e.hashes()) {
			rt.Fatalf("incremental recompute drifted from a full one:\n%v\n%v", incremental, e.hashes())
		}
	})
}
