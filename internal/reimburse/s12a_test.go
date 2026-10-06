//go:build integration

package reimburse_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/events"
	"github.com/alec-jensen/sofar/internal/links"
	"github.com/alec-jensen/sofar/internal/receivables"
	"github.com/alec-jensen/sofar/internal/reimburse"
)

// T1 and T2 are the reported bug: dad's $11 posts while the $22.40 charge is
// still pending and invisible, and a larger unrelated $60 expense is a
// plausible amount-only candidate.
func TestS12a_T1_ReportedBug_NoLinkToUnrelatedExpense(t *testing.T) {
	e := setup(t)
	e.dad()
	e2 := e.expense("2026-10-12", "2026-10-12", 6000, "HOME DEPOT 0121", "home_household")
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	ds := e.process()
	d := ds[pay]
	if d.Kind != reimburse.KindAbstain {
		t.Fatalf("no real counterpart is visible, so it abstains: %+v", d)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("never a link on a guess")
	}
	if s := e.suggestions(pay); len(s) != 0 {
		t.Fatalf("not even a suggestion for the amount-only candidate %s: %v", e2, s)
	}
	h, ok := e.held()[pay]
	if !ok || h.State != reimburse.AwaitingExpense || h.Amount != 1100 {
		t.Fatalf("held as awaiting expense: %+v", e.held())
	}
	if d.Reason == "" {
		t.Fatal("the abstain says why")
	}
}

func TestS12a_T2_LateMatchSuggestsWhenExpensePosts(t *testing.T) {
	e := setup(t)
	e.dad()
	e2 := e.expense("2026-10-12", "2026-10-12", 6000, "HOME DEPOT 0121", "home_household")
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	e.process()
	// two days later the charge posts; it happened the day before the payback
	e1 := e.expense("2026-10-17", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	ds := e.process()
	d := ds[pay]
	if d.Kind != reimburse.KindSuggest || d.Chosen == nil || d.Chosen.ExpenseID != e1 {
		t.Fatalf("late match suggests the McDonald's charge: %+v", d)
	}
	if got := e.suggestions(pay); len(got) != 1 || got[0] != e1 {
		t.Fatalf("one suggestion, and not %s: %v", e2, got)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("a suggestion is not a link: the user decides")
	}
	// accepting it links tentatively and nets at once
	e.tx(func(tx *sql.Tx) {
		l, err := e.svc.AcceptSuggestion(context.Background(), tx, events.ActorUser, pay, 1)
		if err != nil || l.State != links.Tentative || l.Amount != 1100 {
			t.Fatalf("%+v %v", l, err)
		}
	})
	if _, still := e.held()[pay]; still {
		t.Fatal("no longer held once linked")
	}
	if left, _ := e.lnk.Remaining(context.Background(), e.db, e2, nil); left != 6000 {
		t.Fatalf("E2 is untouched: %d", left)
	}
}

func TestS12a_T3_PendingExpenseLinksAndCarriesOver(t *testing.T) {
	e := setup(t)
	e.dad()
	pending := e.expenseIn(e.acct, "2026-10-15", "2026-10-14", 2000, "MCDONALDS F1234", "dining_out", true)
	pay := e.payment("2026-10-15", 1000, "VENMO PAYMENT FROM DAD", "")
	ds := e.process()
	if d := ds[pay]; d.Kind != reimburse.KindSuggest || d.Chosen.ExpenseID != pending {
		t.Fatalf("a visible pending charge is a candidate: %+v", d)
	}
	var l links.Link
	e.tx(func(tx *sql.Tx) {
		var err error
		if l, err = e.svc.AcceptSuggestion(context.Background(), tx, events.ActorUser, pay, 1); err != nil {
			t.Fatal(err)
		}
	})
	// the charge posts at a different amount (a tip) as a new row
	posted := e.expense("2026-10-17", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.CarryOver(context.Background(), tx, events.ActorSystem, pending, posted); err != nil {
			t.Fatal(err)
		}
	})
	got, _ := e.lnk.Get(context.Background(), e.db, l.ID)
	if got.ExpenseID != posted || got.Amount != 1000 || !got.Active() {
		t.Fatalf("the link followed the charge to its posted row: %+v", got)
	}
	// had the posted amount been smaller than the link, the link shrinks to fit
	e.tx(func(tx *sql.Tx) {
		small := e.expense("2026-10-18", "2026-10-14", 800, "SMALLER", "dining_out")
		if _, err := tx.Exec(`UPDATE links SET expense_txn_id=$1 WHERE id=$2`, small, l.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.CarryOver(context.Background(), tx, events.ActorSystem, small, small); err != nil {
			t.Fatal(err)
		}
	})
	if shrunk, _ := e.lnk.Get(context.Background(), e.db, l.ID); shrunk.Amount != 800 {
		t.Fatalf("shrunk to the posted amount: %+v", shrunk)
	}
}

func TestS12a_T4_ReceivableMatchedFirst(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	e1 := e.expense("2026-10-14", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	var r receivables.Receivable
	e.tx(func(tx *sql.Tx) {
		var err error
		r, err = e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: e1, Amount: 1100, CounterpartyID: &dad.ID})
		if err != nil {
			t.Fatal(err)
		}
	})
	// an unrelated expense that would also fit by amount and time must not capture the payment
	e.expense("2026-10-13", "2026-10-13", 2200, "PIZZA PLACE", "dining_out")
	pay := e.payment("2026-10-18", 1100, "VENMO PAYMENT FROM DAD", "")
	ds := e.process()
	d := ds[pay]
	if d.Kind != reimburse.KindAuto || d.Source != "receivable" || d.Chosen.ReceivableID != r.ID {
		t.Fatalf("exact receivable match from the same payer: %+v", d)
	}
	ls := e.activeLinks()
	if len(ls) != 1 || ls[0].State != links.Tentative || ls[0].CreatedBy != links.ByAutoReceivable || ls[0].ExpenseID != e1 {
		t.Fatalf("tentative, auto_receivable, to the receivable's expense: %+v", ls)
	}
	if got, _ := e.rcv.Get(context.Background(), e.db, r.ID); got.Status != receivables.Received {
		t.Fatalf("%+v", got)
	}
}

func TestS12a_T5_NoCandidateHeldThenAsks(t *testing.T) {
	e := setup(t)
	e.dad()
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	e.process()
	if h := e.held()[pay]; h.State != reimburse.AwaitingExpense || h.GraceUntil != (civil.Date{Y: 2026, M: 10, D: 20}) {
		t.Fatalf("5 day grace window from the payment: %+v", h)
	}
	e.sweepAt(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	if e.held()[pay].State != reimburse.AwaitingExpense {
		t.Fatal("still inside the grace window")
	}
	e.sweepAt(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	if e.held()[pay].State != reimburse.UnlinkedReimb {
		t.Fatalf("after the grace window it asks what it was for: %+v", e.held()[pay])
	}
	e.sweepAt(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	if e.held()[pay].State != reimburse.NeedsDecision {
		t.Fatalf("after the 30 day hold it needs a decision: %+v", e.held()[pay])
	}
	// idempotent after downtime
	e.sweepAt(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	if e.held()[pay].State != reimburse.NeedsDecision {
		t.Fatal("a second sweep changes nothing")
	}
}

func TestS12a_T6_TwoCandidatesInsideMarginAbstain(t *testing.T) {
	e := setup(t)
	e.dad()
	a := e.expense("2026-10-14", "2026-10-14", 2200, "TACO PLACE A", "dining_out")
	b := e.expense("2026-10-14", "2026-10-14", 2200, "TACO PLACE B", "dining_out")
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	ds := e.process()
	d := ds[pay]
	if d.Kind != reimburse.KindAmbiguous || d.Chosen != nil {
		t.Fatalf("two equally good fits: %+v", d)
	}
	got := e.suggestions(pay)
	if len(got) != 2 || !((got[0] == a && got[1] == b) || (got[0] == b && got[1] == a)) {
		t.Fatalf("it shows both: %v", got)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("no link")
	}
}

func TestS12a_T7_OverReimbursementExcludedAsCandidate(t *testing.T) {
	e := setup(t)
	e.dad()
	e.expense("2026-10-14", "2026-10-14", 2000, "SMALL LUNCH", "dining_out")
	pay := e.payment("2026-10-15", 3000, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind == reimburse.KindSuggest || d.Kind == reimburse.KindAuto || len(e.suggestions(pay)) != 0 {
		t.Fatalf("a link may not exceed the expense's remaining amount: %+v", d)
	}
	for _, o := range d.Options {
		if !o.Features.Over {
			t.Fatalf("the debugger shows it was excluded as over: %+v", o)
		}
	}
}

func TestS12a_T8_UnknownPayerRoundAmountAsksNeverLinks(t *testing.T) {
	e := setup(t)
	e.expense("2026-10-14", "2026-10-14", 4000, "BIG DINNER", "dining_out")
	pay := e.payment("2026-10-15", 2000, "ZELLE PAYMENT FROM MIKE RAY 8812", "")
	d := e.process()[pay]
	if d.Kind == reimburse.KindAuto || len(e.activeLinks()) != 0 {
		t.Fatalf("a stranger paying a round half is a question, not a link: %+v", d)
	}
}

func TestS12a_T9_PendingInboundCancelledDissolves(t *testing.T) {
	e := setup(t)
	dad := e.dad()
	e1 := e.expense("2026-10-14", "2026-10-14", 2200, "TACO PLACE", "dining_out")
	var r receivables.Receivable
	e.tx(func(tx *sql.Tx) {
		r, _ = e.rcv.Create(context.Background(), tx, events.ActorUser, receivables.Params{ExpenseID: e1, Amount: 1100, CounterpartyID: &dad.ID})
	})
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	if _, err := e.db.Exec(`UPDATE raw_transactions SET pending=true WHERE id=$1`, pay); err != nil {
		t.Fatal(err)
	}
	e.tx(func(tx *sql.Tx) {
		if _, err := e.rcv.Receive(context.Background(), tx, events.ActorSystem, r.ID, pay, 0); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ := e.rcv.Get(context.Background(), e.db, r.ID); got.Status != receivables.Received {
		t.Fatal("linked while pending")
	}
	// the pending inbound payment is cancelled and vanishes
	e.tx(func(tx *sql.Tx) {
		n, err := e.svc.Dissolve(context.Background(), tx, events.ActorSystem, pay)
		if err != nil || n != 1 {
			t.Fatalf("%d %v", n, err)
		}
	})
	if got, _ := e.rcv.Get(context.Background(), e.db, r.ID); got.Status != receivables.Expected || got.ReceivedAmount != 0 {
		t.Fatalf("the receivable returns to expected: %+v", got)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("the link dissolved cleanly")
	}
}

func TestS12a_T10_ExpensePostingAfterPaymentNotPenalized(t *testing.T) {
	e := setup(t)
	e.dad()
	// the dinner happened on the 14th but posts on the 17th, after the payback on the 15th
	e1 := e.expense("2026-10-17", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	d := e.process()[pay]
	if d.Kind != reimburse.KindSuggest || d.Chosen.ExpenseID != e1 || d.Chosen.Features.TooEarly {
		t.Fatalf("dating uses the transaction date: %+v", d)
	}
}

func TestS12a_T10_PostedDateOnlyWithinPostingDelay(t *testing.T) {
	e := setup(t)
	e.dad()
	// with only a posted date, an expense one day after the payment (inside the posting delay) is not penalized
	e2 := e.expense("2026-10-17", "", 3000, "WINGS PLACE", "dining_out")
	pay2 := e.payment("2026-10-16", 1500, "VENMO PAYMENT FROM DAD", "")
	d2 := e.process()[pay2]
	if d2.Chosen == nil || d2.Chosen.ExpenseID != e2 || d2.Chosen.Features.TooEarly {
		t.Fatalf("one day of posting lag is fine: %+v", d2)
	}
}

func TestS12a_T11_HiddenPendingHintRaisesAbstainBiasNeverLinks(t *testing.T) {
	e := setup(t)
	e.dad()
	// a half of a dining expense, but with nothing else in its favor: it scores just over the threshold
	e1 := e.expense("2026-10-05", "2026-10-05", 3000, "SOME CAFE", "dining_out")
	pay := e.payment("2026-10-15", 1500, "VENMO PAYMENT FROM DAD", "")
	if d := e.process()[pay]; d.Kind != reimburse.KindSuggest || d.Chosen.ExpenseID != e1 {
		t.Fatalf("without a hint it is suggested: %+v", d)
	}
	// the account reports a persistent balance versus available gap: pending holds the feed is not showing
	bias := reimburse.HintBias(500000, 450000, 0)
	if bias <= 0 {
		t.Fatalf("a gap raises the bias: %v", bias)
	}
	if reimburse.HintBias(500000, 500000, 0) != 0 || reimburse.HintBias(500000, 450000, 50000) != 0 {
		t.Fatal("no gap, or one fully explained by visible pending items, adds nothing")
	}
	e.svc.Cfg.AbstainBias[e.acct] = bias
	d := e.process()[pay]
	if d.Kind != reimburse.KindAbstain {
		t.Fatalf("a hint makes it abstain: %+v", d)
	}
	if len(e.activeLinks()) != 0 {
		t.Fatal("a hint never creates a link")
	}
}

func TestS12a_T12_EveryScheduledPullSendsPending(t *testing.T) {
	t.Skip("M2: verified in the sync client tests (internal/simplefin) when the client lands")
}

func TestS12a_DeclarationBindsPlaceholder(t *testing.T) {
	e := setup(t)
	e.dad()
	e.expense("2026-10-12", "2026-10-12", 6000, "HOME DEPOT 0121", "home_household")
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	e.process()
	approx := int64(2200)
	e.tx(func(tx *sql.Tx) {
		if err := e.svc.Declare(context.Background(), tx, pay, "McDonalds", &approx); err != nil {
			t.Fatal(err)
		}
	})
	h := e.held()[pay]
	if !h.Declared || h.HintMerchant != "mcdonalds" || h.HintAmount == nil || *h.HintAmount != 2200 {
		t.Fatalf("the placeholder keeps its hints: %+v", h)
	}
	// a declared placeholder does not age into a question while it waits
	e.sweepAt(time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC))
	if e.held()[pay].State != reimburse.AwaitingExpense {
		t.Fatalf("it waits for its expense: %+v", e.held()[pay])
	}
	e.sweepAt(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	e1 := e.expense("2026-10-17", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	d := e.process()[pay]
	if d.Kind != reimburse.KindAuto || d.Source != "declaration" || d.Chosen.ExpenseID != e1 {
		t.Fatalf("it binds to the expense when it appears: %+v", d)
	}
	ls := e.activeLinks()
	if len(ls) != 1 || ls[0].State != links.Tentative || ls[0].CreatedBy != links.ByUser || ls[0].ExpenseID != e1 {
		t.Fatalf("tentative, and still waiting for review: %+v", ls)
	}
	if _, still := e.held()[pay]; still {
		t.Fatal("no longer held")
	}
}

func TestS12a_DeclarationNeverBindsToAmbiguousHint(t *testing.T) {
	e := setup(t)
	e.dad()
	pay := e.payment("2026-10-15", 1100, "VENMO PAYMENT FROM DAD", "")
	e.process()
	e.tx(func(tx *sql.Tx) { e.svc.Declare(context.Background(), tx, pay, "mcdonalds", nil) })
	e.expense("2026-10-17", "2026-10-14", 2240, "MCDONALDS F1234", "dining_out")
	e.expense("2026-10-17", "2026-10-14", 2250, "MCDONALDS F9999", "dining_out")
	if d := e.process()[pay]; d.Source == "declaration" || len(e.activeLinks()) != 0 {
		t.Fatalf("two matches means asking, not binding: %+v", d)
	}
}
