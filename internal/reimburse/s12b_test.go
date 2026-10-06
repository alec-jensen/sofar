//go:build integration

package reimburse_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/counterparties"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/reimburse"
)

func day(n int) string {
	return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n).Format("2006-01-02")
}

// round i of "dad pays half of dining": an expense, and his payback two days later.
// It returns the payment id and its decision.
func (e *env) round(i int, expenseCents int64, category string) (string, reimburse.Decision) {
	exp := e.expense(day(i*4), day(i*4), expenseCents, fmt.Sprintf("DINER NUMBER %d", i), category)
	_ = exp
	pay := e.payment(day(i*4+2), expenseCents/2, "VENMO PAYMENT FROM DAD", "")
	ds := e.process()
	return pay, ds[pay]
}

// confirmRound accepts a suggestion (or takes the auto link) and confirms it.
func (e *env) confirmRound(pay string, d reimburse.Decision) links.Link {
	e.t.Helper()
	var l links.Link
	e.tx(func(tx *sql.Tx) {
		ctx := context.Background()
		if d.Kind == reimburse.KindSuggest {
			var err error
			if l, err = e.svc.AcceptSuggestion(ctx, tx, events.ActorUser, pay, 1); err != nil {
				e.t.Fatal(err)
			}
		} else {
			for _, x := range e.activeLinksTx(tx) {
				if x.PaymentID == pay {
					l = x
				}
			}
		}
		if _, err := e.svc.Confirm(ctx, tx, events.ActorUser, l.ID); err != nil {
			e.t.Fatal(err)
		}
	})
	return l
}

func (e *env) activeLinksTx(tx *sql.Tx) []links.Link {
	all, err := e.lnk.List(context.Background(), tx)
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

func (e *env) graduate() {
	e.t.Helper()
	for i := 0; i < 3; i++ {
		pay, d := e.round(i, 2000, "dining_out")
		if d.Kind != reimburse.KindSuggest {
			e.t.Fatalf("round %d is suggest-only while the pattern is young: %+v", i, d)
		}
		e.confirmRound(pay, d)
	}
}

func (e *env) patterns() []reimburse.Pattern {
	ps, err := e.svc.Patterns(context.Background(), e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	return ps
}

func TestS12b_T1_ThreeConfirmsGraduate(t *testing.T) {
	e := setup(t)
	e.dad()
	for i := 0; i < 3; i++ {
		pay, d := e.round(i, 2000, "dining_out")
		if d.Kind != reimburse.KindSuggest {
			t.Fatalf("a first encounter only suggests: round %d %+v", i, d)
		}
		e.confirmRound(pay, d)
		if ps := e.patterns(); len(ps) != 1 || (i < 2 && ps[0].State != reimburse.Probation) {
			t.Fatalf("probation until the third: %+v", ps)
		}
	}
	if ps := e.patterns(); ps[0].State != reimburse.Active || ps[0].Support != 3 {
		t.Fatalf("three confirmations in a row graduate: %+v", ps)
	}
	pay, d := e.round(3, 2000, "dining_out")
	if d.Kind != reimburse.KindAuto || d.Source != "pattern" || d.PatternID == "" {
		t.Fatalf("the next one is auto-tentative: %+v", d)
	}
	ls := e.activeLinks()
	last := ls[len(ls)-1]
	if last.PaymentID != pay || last.State != links.Tentative || last.CreatedBy != links.ByAutoPattern {
		t.Fatalf("tentative, auto_pattern, waiting for review: %+v", last)
	}
}

func TestS12b_T2_OneRejectionDemotes(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	pay, d := e.round(3, 2000, "dining_out")
	if d.Kind != reimburse.KindAuto {
		t.Fatalf("%+v", d)
	}
	var auto links.Link
	for _, l := range e.activeLinks() {
		if l.PaymentID == pay {
			auto = l
		}
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.Reject(context.Background(), tx, events.ActorUser, auto.ID); err != nil {
			t.Fatal(err)
		}
	})
	ps := e.patterns()
	if ps[0].State != reimburse.Probation || ps[0].Streak != 0 || ps[0].Strikes != 1 {
		t.Fatalf("one strike puts it back to suggest-only: %+v", ps[0])
	}
	var demoted int
	e.db.QueryRow(`SELECT count(*) FROM events WHERE kind=$1`, reimburse.KindPatternDemoted).Scan(&demoted)
	if demoted != 1 {
		t.Fatal("the owner is told through an event")
	}
	_, d = e.round(15, 2000, "dining_out") // well away from the rejected one
	if d.Kind != reimburse.KindSuggest {
		t.Fatalf("it must earn graduation again: %+v", d)
	}
}

func TestS12b_T3_MixedPayerNeverAutoLinks(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	// a gift from the same payer: the user says it is not a reimbursement
	gift := e.payment(day(14), 5000, "VENMO PAYMENT FROM DAD", "happy birthday")
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.ClassifyOther(context.Background(), tx, gift); err != nil {
			t.Fatal(err)
		}
	})
	_, d := e.round(5, 2000, "dining_out")
	if d.Kind == reimburse.KindAuto {
		t.Fatalf("a payer with gifts and paybacks always asks: %+v", d)
	}
	if d := e.process()[gift]; d.Kind != reimburse.KindSkip {
		t.Fatalf("and the gift itself is not matched: %+v", d)
	}
	pr, _ := e.svc.Profile(context.Background(), e.db, e.patterns()[0].CounterpartyID)
	if !pr.Mixed || pr.Others != 1 || pr.Paybacks != 3 {
		t.Fatalf("%+v", pr)
	}
}

func TestS12b_T4_MaterialityGateNeverAutoAbove100(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	// the same half-of-dining shape, but $150 back on a $300 dinner
	_, d := e.round(3, 30000, "dining_out")
	if d.Kind == reimburse.KindAuto {
		t.Fatalf("above the $100 gate it is never auto-tentative, however confident: %+v", d)
	}
	if d.Kind != reimburse.KindSuggest || d.Chosen == nil {
		t.Fatalf("it still suggests: %+v", d)
	}
}

func TestS12b_T5_TwoCandidatesInsideMarginAbstainEvenWithPattern(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	e.expense(day(40), day(40), 2000, "SUSHI A", "dining_out")
	e.expense(day(40), day(40), 2000, "SUSHI B", "dining_out")
	pay := e.payment(day(41), 1000, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind != reimburse.KindAmbiguous || len(e.suggestions(pay)) != 2 {
		t.Fatalf("abstain and show both: %+v", d)
	}
}

func TestS12b_T6_PersonalizedAging(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	e.graduate() // dad pays in 2 days, three times
	var slow counterparties.Counterparty
	e.tx(func(tx *sql.Tx) {
		slow, _ = e.cps.Create(context.Background(), tx, events.ActorUser, "Aunt", "ZELLE FROM AUNT MAY")
	})
	for i := 0; i < 3; i++ { // aunt takes three weeks
		exp := e.expense(day(60+i*30), day(60+i*30), 4000, fmt.Sprintf("GROCER %d", i), "groceries")
		pay := e.payment(day(60+i*30+21), 2000, "ZELLE FROM AUNT MAY", "")
		e.process()
		e.tx(func(tx *sql.Tx) {
			l, err := e.svc.ManualLink(context.Background(), tx, events.ActorUser, links.Params{PaymentID: pay, ExpenseID: exp})
			if err != nil || l.State != links.Confirmed {
				t.Fatalf("%+v %v", l, err)
			}
		})
	}
	// two new open receivables, one each
	mk := func(cp counterparties.Counterparty, desc string) {
		exp := e.expense(day(200), day(200), 3000, desc, "dining_out")
		e.tx(func(tx *sql.Tx) {
			if _, err := e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: exp, Amount: 1500, CounterpartyID: &cp.ID}); err != nil {
				t.Fatal(err)
			}
		})
	}
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 200)
	e.now = base
	e.rcv.Now = func() time.Time { return e.now }
	mk(dad, "DINNER WITH DAD")
	mk(slow, "LUNCH WITH AUNT")
	nudged := func(daysLater int) map[string]bool {
		e.now = base.AddDate(0, 0, daysLater)
		e.rcv.Now = func() time.Time { return e.now }
		al, err := e.rcv.Aging(context.Background(), e.db, e.svc.AgingThresholds(context.Background(), e.db))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, a := range al {
			r, _ := e.rcv.Get(context.Background(), e.db, a.ReceivableID)
			out[*r.CounterpartyID] = true
		}
		return out
	}
	if got := nudged(4); got[dad.ID] || got[slow.ID] {
		t.Fatalf("too early for both: %v", got)
	}
	if got := nudged(6); !got[dad.ID] || got[slow.ID] {
		t.Fatalf("a payer who always pays in 2 days is nudged near day 5, the slow one is not: %v", got)
	}
	if got := nudged(44); !got[slow.ID] {
		t.Fatalf("the slow payer is nudged after their own lag: %v", got)
	}
}

func TestS12b_T7_RetrainAfterBulkUnlink(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	// the user realizes all three were wrong and unlinks them in bulk
	e.tx(func(tx *sql.Tx) {
		for _, l := range e.activeLinksTx(tx) {
			if _, err := e.svc.Unlink(context.Background(), tx, events.ActorUser, l.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.svc.Retrain(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
	})
	ps := e.patterns()
	if ps[0].State == reimburse.Active || ps[0].Support != 0 {
		t.Fatalf("support is rebuilt from the corrected labels: %+v", ps[0])
	}
	pr, _ := e.svc.Profile(context.Background(), e.db, ps[0].CounterpartyID)
	if pr.Paybacks != 0 {
		t.Fatalf("the profile forgets what was undone: %+v", pr)
	}
	_, d := e.round(3, 2000, "dining_out")
	if d.Kind == reimburse.KindAuto {
		t.Fatalf("later suggestions change: no more auto-linking: %+v", d)
	}
}

// ---- S12b edge cases ----

func TestS12b_E1_PayerStopsPatternDecaysAndAsks(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	e.now = e.now.AddDate(0, 0, 200)
	e.tx(func(tx *sql.Tx) {
		ids, err := e.svc.DecayPatterns(context.Background(), tx, 120)
		if err != nil || len(ids) != 1 {
			t.Fatalf("%v %v", ids, err)
		}
	})
	if ps := e.patterns(); ps[0].State != reimburse.Demoted {
		t.Fatalf("back to suggest-only: %+v", ps[0])
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM events WHERE kind=$1`, reimburse.KindPatternDemoted).Scan(&n)
	if n != 1 {
		t.Fatal("and the owner is asked whether it changed")
	}
}

func TestS12b_E2_TipAndTaxShareWithinTolerance(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	// 49% instead of 50%: a tip changed the expense
	e.expense(day(40), day(40), 2240, "DINER WITH TIP", "dining_out")
	pay := e.payment(day(42), 1100, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind != reimburse.KindAuto {
		t.Fatalf("49 percent is the same habit: %+v", d)
	}
}

func TestS12b_E6_OnePaymentCoversTwoExpenses(t *testing.T) {
	e := setup(t)
	e.dad()
	a := e.expense(day(10), day(10), 3000, "BRUNCH SPOT", "dining_out")
	b := e.expense(day(11), day(11), 4000, "MOVIE NIGHT", "entertainment")
	pay := e.payment(day(13), 3500, "VENMO PAYMENT FROM DAD", "for the weekend")
	d := e.process()[pay]
	if d.Kind != reimburse.KindSuggest || d.Chosen != nil || len(d.Options) < 2 || d.Options[0].Group == 0 {
		t.Fatalf("half of both together: %+v", d)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.AcceptSuggestion(context.Background(), tx, events.ActorUser, pay, 1); err != nil {
			t.Fatal(err)
		}
	})
	ls := e.activeLinks()
	got := map[string]int64{}
	var sum int64
	for _, l := range ls {
		got[l.ExpenseID] = l.Amount
		sum += l.Amount
	}
	if len(ls) != 2 || sum != 3500 || got[a] != 1500 || got[b] != 2000 {
		t.Fatalf("one payment, two links, split by share: %+v", ls)
	}
}

func TestS12b_E6_SeveralFittingCombinationsAsk(t *testing.T) {
	e := setup(t)
	e.dad()
	e.expense(day(10), day(10), 3000, "A", "dining_out")
	e.expense(day(10), day(10), 4000, "B", "dining_out")
	e.expense(day(11), day(11), 3000, "C", "dining_out")
	pay := e.payment(day(13), 3500, "VENMO PAYMENT FROM DAD", "")
	if d := e.process()[pay]; d.Kind != reimburse.KindAmbiguous || len(e.activeLinks()) != 0 {
		t.Fatalf("more than one pair fits, so it asks: %+v", d)
	}
}

func TestS12b_E10_RoommateFixedShareNeedsProof(t *testing.T) {
	e := setup(t)
	room := e.dad()
	_ = room
	// utilities, half each, about the 3rd of each month
	for i := 0; i < 3; i++ {
		exp := e.expense(day(i*30), day(i*30), 12000, fmt.Sprintf("CITY UTILITIES %d", i), "other_bills")
		pay := e.payment(day(i*30+3), 6000, "VENMO PAYMENT FROM DAD", "utilities")
		d := e.process()[pay]
		if d.Kind == reimburse.KindAuto {
			t.Fatalf("not before it is proven: %d", i)
		}
		e.confirmRound(pay, d)
		_ = exp
	}
	exp := e.expense(day(90), day(90), 12000, "CITY UTILITIES 3", "other_bills")
	pay := e.payment(day(93), 6000, "VENMO PAYMENT FROM DAD", "utilities")
	if d := e.process()[pay]; d.Kind != reimburse.KindAuto || d.Chosen.ExpenseID != exp {
		t.Fatalf("after three the fixed share links tentatively: %+v", d)
	}
}

func TestS12b_E11_DifferentShareAsksAndRatioUpdates(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	// 57% is not the habit: it asks instead of linking on its own
	e.expense(day(40), day(40), 2000, "DINER BIG TIP", "dining_out")
	pay := e.payment(day(42), 1150, "VENMO PAYMENT FROM DAD", "")
	if d := e.process()[pay]; d.Kind == reimburse.KindAuto {
		t.Fatalf("a different share is not the habit: %+v", d)
	}
	// a drift inside the tolerance moves the learned share toward what they actually pay
	before := e.patterns()[0].ShareRatio
	e.expense(day(70), day(70), 2000, "DINER DRIFT", "dining_out")
	p2 := e.payment(day(72), 1060, "VENMO PAYMENT FROM DAD", "") // 53%
	d3 := e.process()[p2]
	if d3.Kind == reimburse.KindSkip {
		t.Fatalf("%+v", d3)
	}
	e.confirmRound(p2, d3) // 53% is inside the habit: linked tentatively, then confirmed
	if after := e.patterns()[0].ShareRatio; after <= before {
		t.Fatalf("the share ratio follows what they actually pay: %v -> %v", before, after)
	}
}

func TestS12b_E13_UserRuleBeatsLearnedPattern(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	e.tx(func(tx *sql.Tx) {
		if _, err := e.svc.AuthorPattern(context.Background(), tx, dad.ID, "dining_out", 0.5, 3); err != nil {
			t.Fatal(err)
		}
	})
	// he actually pays a third three times: a learned pattern would disagree
	for i := 0; i < 3; i++ {
		e.expense(day(i*4), day(i*4), 3000, fmt.Sprintf("THIRDS %d", i), "dining_out")
		pay := e.payment(day(i*4+2), 1000, "VENMO PAYMENT FROM DAD", "")
		d := e.process()[pay]
		e.confirmRound(pay, d)
	}
	ps := e.patterns()
	if len(ps) != 1 || !ps[0].UserAuthored || ps[0].ShareRatio != 0.5 {
		t.Fatalf("the user's habit stands and no rival is learned: %+v", ps)
	}
	var asked int
	e.db.QueryRow(`SELECT count(*) FROM events WHERE kind=$1`, reimburse.KindPatternConflict).Scan(&asked)
	if asked == 0 {
		t.Fatal("the system asks what to do about the conflict")
	}
	// the authored habit does link on its own
	e.expense(day(40), day(40), 3000, "HALF DINNER", "dining_out")
	pay := e.payment(day(42), 1500, "VENMO PAYMENT FROM DAD", "")
	if d := e.process()[pay]; d.Kind != reimburse.KindAuto {
		t.Fatalf("a habit the user wrote is already proven: %+v", d)
	}
}

func TestS12b_E14_CashPaybackTrainsExpensePrediction(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	for i := 0; i < 2; i++ {
		exp := e.expense(day(i*10), day(i*10), 2000, fmt.Sprintf("CASH DINER %d", i), "dining_out")
		var r receivables.Receivable
		e.tx(func(tx *sql.Tx) {
			r, _ = e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: exp, Amount: 1000, CounterpartyID: &dad.ID})
		})
		e.tx(func(tx *sql.Tx) {
			if err := e.svc.ReceiveCashLearn(context.Background(), tx, events.ActorUser, r.ID, "", civil.Date{Y: 2026, M: 8, D: 1 + i*10 + 2}, 0); err != nil {
				t.Fatal(err)
			}
		})
	}
	next := e.expense(day(30), day(30), 3000, "NEW DINER", "dining_out")
	ps, err := e.svc.PredictExpense(context.Background(), e.db, next)
	if err != nil || len(ps) != 1 || ps[0].Name != "Dad" || ps[0].Amount != 1500 || ps[0].Proven {
		t.Fatalf("likely reimbursed by dad, about $15, not yet proven: %+v %v", ps, err)
	}
	if ps[0].ExpectedBy != (civil.Date{Y: 2026, M: 9, D: 2}) {
		t.Fatalf("by when: %+v", ps[0].ExpectedBy)
	}
}

func TestS12b_E15_PaybackInDifferentAccountStillLinks(t *testing.T) {
	e := setup(t)
	e.dad()
	card := e.expense(day(10), day(10), 2000, "DINER ON THE CARD", "dining_out")
	other := e.acctOf("savings")
	pay := e.paymentIn(other, day(12), 1000, "VENMO PAYMENT FROM DAD")
	d := e.process()[pay]
	if d.Kind != reimburse.KindSuggest || d.Chosen.ExpenseID != card {
		t.Fatalf("link regardless of account: %+v", d)
	}
}

// ---- S12a edge cases that need the matcher ----

func TestS12a_E2_PaymentWithFeeStillMatchesReceivable(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	exp := e.expense(day(10), day(10), 2240, "DINER", "dining_out")
	e.tx(func(tx *sql.Tx) {
		e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: exp, Amount: 1100, CounterpartyID: &dad.ID})
	})
	pay := e.payment(day(12), 1075, "VENMO PAYMENT FROM DAD", "") // a fee came off
	d := e.process()[pay]
	if d.Kind != reimburse.KindAuto || d.Chosen.Amount != 1075 {
		t.Fatalf("within the small tolerance: %+v", d)
	}
	if len(e.activeLinks()) != 1 {
		t.Fatal("linked once")
	}
}

func TestS12a_E10_TwoReceivablesSamePayerSameAmountAsks(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	var first receivables.Receivable
	for i := 0; i < 2; i++ {
		exp := e.expense(day(10+i), day(10+i), 2200, fmt.Sprintf("DINER %d", i), "dining_out")
		e.now = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 10+i)
		e.rcv.Now = func() time.Time { return e.now }
		e.tx(func(tx *sql.Tx) {
			r, _ := e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: exp, Amount: 1100, CounterpartyID: &dad.ID})
			if i == 0 {
				first = r
			}
		})
	}
	e.now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pay := e.payment(day(14), 1100, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind != reimburse.KindAmbiguous || len(d.Options) != 2 || d.Options[0].ReceivableID != first.ID {
		t.Fatalf("suggest the oldest and ask: %+v", d)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("no link")
	}
}

func TestS12a_E11_ResemblesPastButNoCandidateNeverAutoLinks(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	pay := e.payment(day(60), 1000, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind != reimburse.KindAbstain || len(e.activeLinks()) != 3 {
		t.Fatalf("held for the picker, never auto-linked: %+v", d)
	}
	if _, ok := e.held()[pay]; !ok {
		t.Fatal("held")
	}
}

func TestS12a_E13_PlatformDoubleEntriesDeduped(t *testing.T) {
	e := setup(t)
	e.dad()
	e.expense(day(10), day(10), 2200, "DINER", "dining_out")
	app := e.acctOf("venmo balance")
	a := e.payment(day(12), 1100, "VENMO PAYMENT FROM DAD", "")
	b := e.paymentIn(app, day(12), 1100, "VENMO PAYMENT FROM DAD")
	ds := e.process()
	kinds := map[reimburse.Kind]int{}
	for _, id := range []string{a, b} {
		kinds[ds[id].Kind]++
	}
	if kinds[reimburse.KindSkip] != 1 || kinds[reimburse.KindSuggest] != 1 {
		t.Fatalf("one is the real payback, the other a duplicate of it: %+v", kinds)
	}
}

func TestS12a_E14_RecurringAllowanceAsks(t *testing.T) {
	e := setup(t)
	e.dad()
	for i := 0; i < 4; i++ {
		e.payment(day(i*14), 5000, "ZELLE PAYMENT FROM MOM", "allowance")
	}
	qs, err := e.svc.AllowanceQuestions(context.Background(), e.db)
	if err != nil || len(qs) != 1 || qs[0].Amount != 5000 || qs[0].Interval != 14 || qs[0].Count != 4 {
		t.Fatalf("allowance or reimbursement? %+v %v", qs, err)
	}
	e.process()
	if len(e.activeLinks()) != 0 {
		t.Fatal("the question links nothing")
	}
}

func TestS12b_DebuggerReplaysAnyPayment(t *testing.T) {
	e := setup(t)
	e.dad()
	e.graduate()
	pay, _ := e.round(3, 2000, "dining_out") // auto-linked
	d, err := e.svc.Debug(context.Background(), e.db, pay)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != reimburse.KindAuto || len(d.Options) == 0 || d.Reason == "" || d.Chosen == nil || len(d.Chosen.Why) == 0 {
		t.Fatalf("the replay shows candidates, features and why: %+v", d)
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM link_events WHERE payment_txn_id=$1 AND outcome='auto_tentative' AND features <> '{}'::jsonb`, pay).Scan(&n)
	if n != 1 {
		t.Fatalf("features are stored with the decision so it is reproducible: %d", n)
	}
	if _, err = e.svc.Debug(context.Background(), e.db, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("unknown payment")
	}
}
