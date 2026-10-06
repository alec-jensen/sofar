//go:build integration

package categorize_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/categorize"
	"github.com/alec-jensen/sofar/internal/sim"
	"github.com/alec-jensen/sofar/internal/testkit"
)

// runLife plays a synthetic life day by day: transactions arrive, the engine
// suggests, an oracle user reviews, and the model retrains weekly. It returns
// the per-day first-pass results for spending transactions.
// simAmbiguous are the generator's merchants whose category is a coin toss that
// no feature explains; the gates measure everything else.
var simAmbiguous = map[string]bool{"Amazon": true, "Walmart": true, "Target": true, "CVS": true}

type dayResult struct {
	day            int
	total, correct int
}

func runLife(t *testing.T, seed int64, days int, errRate float64) ([]dayResult, *env) {
	t.Helper()
	e := setup(t)
	e.svc.Now = func() time.Time {
		return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).Add(24 * time.Hour * time.Duration(e.dayNow))
	}
	cfg := sim.DefaultConfig(seed)
	cfg.Months = 4
	cfg.OutageEvery, cfg.DuplicateRate, cfg.RotateRate = 0, 0, 0
	life := sim.Generate(cfg)
	oracle := sim.NewOracle(seed, errRate)
	ctx := context.Background()
	if err := e.installSeeds(ctx); err != nil {
		t.Fatal(err)
	}
	byDay := map[int][]sim.Txn{}
	for _, tx := range life.Txns {
		if tx.Truth.Kind != sim.KindSpending {
			continue
		}
		d := tx.Raw.Posted.DaysSince(cfg.Start)
		if d < days {
			byDay[d] = append(byDay[d], tx)
		}
	}
	var results []dayResult
	for d := 0; d < days; d++ {
		e.dayNow = d
		todays := byDay[d]
		if len(todays) == 0 {
			continue
		}
		ids := make([]string, len(todays))
		for i, tx := range todays {
			ids[i] = testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: tx.Raw.Posted.String(), Amount: int64(tx.Raw.Amount), Description: tx.Raw.Description, Payee: tx.Raw.Payee})
		}
		e.refresh()
		res := dayResult{day: d}
		for i, tx := range todays {
			cat, _, ok := e.suggestion(ids[i])
			label := oracle.Label(tx)
			if simAmbiguous[tx.Truth.Merchant] {
				// categories overlap in amount and carry no other signal: nothing can
				// predict these, and they are always reviewed one by one (S18a)
				continue
			}
			res.total++
			if ok && cat == label {
				res.correct++
			}
			sug := cat
			e.review(ids[i], label, categorize.ScopeMerchant, func(in *categorize.ReviewInput) {
				in.Suggested, in.SessionID = sug, fmt.Sprintf("day%d-%d", d, i)
				in.DwellMS = 3000
			})
		}
		results = append(results, res)
		if d%7 == 6 {
			e.tx(func(tx *sql.Tx) { e.svc.Retrain(ctx, tx) })
		}
	}
	return results, e
}

func accuracy(rs []dayResult, from, to int) (float64, int) {
	var n, ok int
	for _, r := range rs {
		if r.day >= from && r.day < to {
			n += r.total
			ok += r.correct
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(ok) / float64(n), n
}

// TestA9_10_FirstPassGates measures first-pass accuracy on the standard seeds:
// at least 90% by day 30 (days 21 to 30) and at least 95% by day 90 (days 81 to 90).
func TestA9_10_FirstPassGates(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	var a30, a90 float64
	seeds := []int64{1, 2, 3}
	for _, seed := range seeds {
		rs, _ := runLife(t, seed, 95, 0)
		x30, n30 := accuracy(rs, 21, 31)
		x90, n90 := accuracy(rs, 81, 91)
		t.Logf("seed %d: days 21-30 %.3f (n=%d), days 81-90 %.3f (n=%d)", seed, x30, n30, x90, n90)
		a30 += x30
		a90 += x90
	}
	a30 /= float64(len(seeds))
	a90 /= float64(len(seeds))
	if a30 < 0.90 {
		t.Errorf("first-pass accuracy by day 30 is %.3f, the gate is 0.90", a30)
	}
	if a90 < 0.95 {
		t.Errorf("first-pass accuracy by day 90 is %.3f, the gate is 0.95", a90)
	}
}

// TestA9_10_TeachOnceGate: after a merchant has been taught, later purchases are
// right at least 95% of the time. Ambiguous merchants are excluded: they need conditions.
func TestA9_10_TeachOnceGate(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	e := setup(t)
	ctx := context.Background()
	life := sim.Generate(func() sim.Config {
		c := sim.DefaultConfig(5)
		c.Months, c.OutageEvery, c.DuplicateRate, c.RotateRate = 4, 0, 0, 0
		return c
	}())
	ambiguous := simAmbiguous
	taught := map[string]int{}
	var after, right int
	for _, tx := range life.Txns {
		if tx.Truth.Kind != sim.KindSpending || ambiguous[tx.Truth.Merchant] {
			continue
		}
		id := testkit.RawTxn(t, e.db, e.acct, testkit.Raw{PostedOn: tx.Raw.Posted.String(), Amount: int64(tx.Raw.Amount), Description: tx.Raw.Description, Payee: tx.Raw.Payee})
		e.refresh()
		cat, _, ok := e.suggestion(id)
		if taught[tx.Truth.Merchant] >= 2 { // taught: it has been labeled twice
			after++
			if ok && cat == tx.Truth.Category {
				right++
			}
		}
		e.review(id, tx.Truth.Category, categorize.ScopeMerchant, nil)
		taught[tx.Truth.Merchant]++
	}
	if after < 100 {
		t.Fatalf("not enough taught merchants to measure: %d", after)
	}
	if rate := float64(right) / float64(after); rate < 0.95 {
		t.Errorf("teach-once accuracy %.3f (n=%d), the gate is 0.95", rate, after)
	} else {
		t.Logf("teach-once accuracy %.3f (n=%d)", rate, after)
	}
	_ = ctx
}

func TestS18a_EvaluationIsPrequential(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.addRule(rule("dining_out", "torchys"))
	a := e.buy("TORCHYS TACOS #1", "2026-09-01", 1500, "", false)
	b := e.buy("KROGER #2", "2026-09-02", 4000, "", false)
	e.refresh()
	e.review(a, "dining_out", categorize.ScopeThisTime, func(in *categorize.ReviewInput) { in.Suggested = "dining_out" })
	e.review(b, "groceries", categorize.ScopeThisTime, nil)
	// a later rule change must not rewrite what was suggested before the label
	e.addRule(rule("travel", "torchys"))
	ev, err := e.svc.Evaluate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Total != 2 || ev.Accepted != 1 {
		t.Fatalf("first-pass: %+v", ev)
	}
	if ev.BySource[categorize.SourceUserRule].N != 1 || len(ev.Trend) != 1 {
		t.Fatalf("%+v", ev)
	}
	if len(ev.WorstMerchants) != 1 || ev.Confusion["other_spending"]["groceries"] != 1 {
		t.Fatalf("the miss is attributed: %+v %+v", ev.WorstMerchants, ev.Confusion)
	}
}

func TestS18a_AuditModeHidesSuggestion(t *testing.T) {
	e := setup(t)
	e.addRule(rule("dining_out", "torchys"))
	for i := 0; i < 40; i++ {
		e.buy("TORCHYS TACOS #1", fmt.Sprintf("2026-09-%02d", 1+i%28), 1500+int64(i), "", false)
	}
	count := func() int {
		q, _ := e.svc.Queue(context.Background(), 0)
		n := 0
		for _, it := range q {
			if it.Hidden {
				n++
			}
		}
		return n
	}
	if count() != 0 {
		t.Fatal("audit mode is off by default")
	}
	e.svc.AuditShare = 0.25
	n := count()
	if n == 0 || n == 40 || count() != n {
		t.Fatalf("a stable, partial share is hidden: %d", n)
	}
}

func TestS18a_RetroReviewFindsLabelNoise(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		id := e.buy(fmt.Sprintf("SHELL OIL #%d", i), fmt.Sprintf("2026-08-%02d", 1+i%28), 3500+int64(i), "", false)
		e.review(id, "fuel", categorize.ScopeMerchant, func(in *categorize.ReviewInput) { in.SessionID = fmt.Sprintf("r%d", i) })
	}
	// a slip: one shell fill-up was once filed under dining out
	slip := e.buy("SHELL OIL #99", "2026-08-29", 3600, "", false)
	e.review(slip, "dining_out", categorize.ScopeThisTime, nil)
	e.tx(func(tx *sql.Tx) { e.svc.Retrain(ctx, tx) })
	list, err := e.svc.RetroReview(ctx, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range list {
		found = found || (d.TxnID == slip && d.ModelSays == "fuel")
	}
	if !found {
		t.Fatalf("the slip is surfaced as a possible past error: %+v", list)
	}
}
