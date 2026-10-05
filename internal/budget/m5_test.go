package budget

import (
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

func (st *step) cover(source EnvelopeID, c money.Cents) *step {
	return st.event(Event{Kind: EvCover, Source: source, Amount: c})
}

func (st *step) spendFrom(c money.Cents, fromDisc bool) *step {
	st.s.seq++
	st.in.Outflows = append(st.in.Outflows, Outflow{ID: "o", Seq: st.s.seq, Date: st.in.Month.First(), Bucket: BucketSpending, Amount: c, FromDiscretionary: fromDisc})
	return st
}

func (st *step) opening(env EnvelopeID, c money.Cents) *step {
	return st.event(Event{Kind: EvOpening, Dest: env, Amount: c})
}

// overspendMonth sets up example E: pool 2000, the user spends past it.
func overspendMonth(t *testing.T, covers func(*step) *step, spend money.Cents) (*scenario, MonthState) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).opening(Goal("phone"), usd(200)).run()
	m := s.month("2026-03").prevIncome(usd(4000)).spend(spend)
	return s, covers(m).run()
}

func TestS2_ExampleE_OverspendCover(t *testing.T) {
	s, mar := overspendMonth(t, func(st *step) *step { return st.cover(EnvNextMonth, usd(100)).cover(Goal("phone"), usd(80)) }, usd(2180))
	if mar.OverspendGross != usd(180) || mar.CoversApplied != usd(180) || mar.OverspendOpen != 0 || mar.SpendLeft != 0 {
		t.Fatalf("covered in full: %+v", mar)
	}
	if mar.CarryToNext != usd(100) || mar.Envelopes[Goal("phone")] != usd(120) || !mar.Has(FlagBorrowedGoal) {
		t.Fatalf("100 borrowed from next month, 80 from the goal (borrowed flag): %+v", mar)
	}
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	if apr.Deductions != usd(100) || apr.SpendLeft != usd(1900) {
		t.Fatalf("next month's pool is reduced by 100: %+v", apr)
	}
}

func TestS10_OverspendOnlyWhenBothExhausted(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).run()
	s.month("2026-03").prevIncome(usd(5000)).once(EnvDiscretionary, usd(300)).run()
	m := s.month("2026-04").prevIncome(usd(4000)).spend(usd(2250)).run()
	if m.OverspendGross != 0 || m.Envelopes[EnvDiscretionary] != usd(50) {
		t.Fatalf("pool then discretionary, no overspend yet: %+v", m)
	}
	m = s.month("2026-05").prevIncome(usd(4000)).spend(usd(2400)).run()
	if m.OverspendGross != usd(350) {
		t.Fatalf("both exhausted: overspend 350, got %+v", m)
	}
}

func TestS10_CoverShrinksInReverseOrder(t *testing.T) { // A-006
	_, m := overspendMonth(t, func(st *step) *step { return st.cover(EnvNextMonth, usd(100)).cover(Goal("phone"), usd(80)) }, usd(2120))
	// a refund shrank the overspend to 120: the newest cover (the goal) shrinks first
	if m.OverspendGross != usd(120) || m.CoversApplied != usd(120) {
		t.Fatalf("%+v", m)
	}
	if m.CarryToNext != usd(100) || m.Envelopes[Goal("phone")] != usd(180) || !m.Has(FlagCoverClamped) {
		t.Fatalf("the goal is refunded 60 and keeps a 20 cover: next=%v goal=%v", m.CarryToNext, m.Envelopes[Goal("phone")])
	}
	_, base := overspendMonth(t, func(st *step) *step { return st }, usd(1500))
	_, none := overspendMonth(t, func(st *step) *step { return st.cover(EnvSavings, usd(50)) }, usd(1500))
	if none.CoversApplied != 0 || none.Envelopes[EnvSavings] != base.Envelopes[EnvSavings] {
		t.Fatalf("a cover with no overspend does nothing: %+v", none)
	}
}

func TestS10_UncoveredIsDebtNotDeduction(t *testing.T) { // A-003
	s, mar := overspendMonth(t, func(st *step) *step { return st.cover(EnvUncovered, usd(180)) }, usd(2180))
	if mar.OverspendOpen != 0 || mar.Envelopes[EnvUncovered] != usd(180) || mar.SpendLeft != -usd(180) {
		t.Fatalf("left uncovered: a visible debt: %+v", mar)
	}
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	if apr.Deductions != 0 || apr.SpendLeft != usd(2000) || apr.Envelopes[EnvUncovered] != usd(180) {
		t.Fatalf("it does not reduce later pools and stays until settled: %+v", apr)
	}
	may := s.month("2026-05").prevIncome(usd(4000)).opening(EnvSavings, usd(500)).
		event(Event{Kind: EvSettle, Source: EnvSavings, Amount: usd(100)}).run()
	if may.Envelopes[EnvUncovered] != usd(80) || may.Envelopes[EnvSavings] < usd(400) {
		t.Fatalf("settling pays the debt down from savings: %+v", may.Envelopes)
	}
}

func TestS10_BillsReserveCoverCreatesGap(t *testing.T) {
	_, m := overspendMonth(t, func(st *step) *step { return st.cover(EnvBillsReserve, usd(180)) }, usd(2180))
	if m.Envelopes[EnvBillsGap] != usd(180) || !m.Has(FlagBillsGap) || m.OverspendOpen != 0 {
		t.Fatalf("%+v", m)
	}
}

func TestS10_EFAndSavingsCovers(t *testing.T) {
	opened := func(st *step) *step { return st.opening(EnvEF, usd(1000)).opening(EnvSavings, usd(1000)) }
	_, base := overspendMonth(t, opened, usd(2180))
	_, m := overspendMonth(t, func(st *step) *step { return opened(st).cover(EnvEF, usd(100)).cover(EnvSavings, usd(80)) }, usd(2180))
	if m.Envelopes[EnvEF] != usd(900) || m.Envelopes[EnvSavings] != base.Envelopes[EnvSavings]-usd(80) || !m.Has(FlagEFUsed) {
		t.Fatalf("%+v", m.Envelopes)
	}
}

func TestS9_FromDiscretionaryBypassesPool(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).run()
	m := s.month("2026-03").prevIncome(usd(4000)).spendFrom(usd(300), true).run()
	if m.SpendLeft != usd(2000) || m.OverspendGross != 0 {
		t.Fatalf("the pool is untouched: %+v", m)
	}
}

func TestS9_DiscretionaryCanGoNegative(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).run()
	m := s.month("2026-03").prevIncome(usd(4000)).spendFrom(usd(300), true).run()
	if m.Envelopes[EnvDiscretionary] != -usd(300) {
		t.Fatalf("discretionary may go negative (S9): %v", m.Envelopes[EnvDiscretionary])
	}
	n := s.month("2026-04").prevIncome(usd(4000)).run()
	if n.Envelopes[EnvDiscretionary] != -usd(300) {
		t.Fatalf("and carries: %v", n.Envelopes[EnvDiscretionary])
	}
}

func TestS7_SavingsVerification(t *testing.T) {
	line := usd(300)
	march := civil.YearMonth{Y: 2026, M: 3}
	past := func(outs ...Outflow) MonthState {
		return ComputeMonth(nil, MonthInputs{Month: march, PrevIncome: usd(4000), Lines: FixedLines{Savings: SavingsRule{Fixed: line}}, Outflows: outs})
	}
	if m := past(); !m.Has(FlagUnverifiedSavings) {
		t.Fatalf("a finished month with no transfer is flagged: %+v", m.Flags)
	}
	if m := past(Outflow{ID: "t", Bucket: BucketSavings, Amount: line}); m.Has(FlagUnverifiedSavings) || m.SpendLeft != m.SpendPool {
		t.Fatalf("a matching transfer verifies it and does not draw from spending: %+v", m)
	}
	moved := ComputeMonth(nil, MonthInputs{Month: march, PrevIncome: usd(4000), Lines: FixedLines{Savings: SavingsRule{Fixed: line}},
		Events: []Event{{ID: 1, At: time.Unix(1, 0), Kind: EvMovedIt, Amount: line}}})
	if moved.Has(FlagUnverifiedSavings) {
		t.Fatalf("i moved it: %+v", moved.Flags)
	}
	today := civil.Date{Y: 2026, M: 3, D: 5}
	cur := ComputeMonth(nil, MonthInputs{Month: march, PrevIncome: usd(4000), Today: &today, Lines: FixedLines{Savings: SavingsRule{Fixed: line}}})
	if cur.Has(FlagUnverifiedSavings) {
		t.Fatal("the current month is not flagged yet")
	}
}

func TestS7_PercentFollowsPlan(t *testing.T) {
	rule := SavingsRule{UseBasisPoints: true, BasisPoints: 1000}
	march := civil.YearMonth{Y: 2026, M: 3}
	a := ComputeMonth(nil, MonthInputs{Month: march, PrevIncome: usd(4000), Lines: FixedLines{Savings: rule}})
	b := ComputeMonth(nil, MonthInputs{Month: march, PrevIncome: usd(2000), Lines: FixedLines{Savings: rule}})
	if a.Fixed != usd(400) || b.Fixed != usd(200) {
		t.Fatalf("10%% of the effective plan: %v %v", a.Fixed, b.Fixed)
	}
}

func TestS7_GoalOverfundGoesToSavingsAndPurchaseDrawsEnvelope(t *testing.T) {
	lines := FixedLines{Goals: []Line{{ID: "trip", Amount: usd(100), Target: usd(250)}}}
	var prev *MonthState
	for _, m := range []time.Month{3, 4, 5, 6} {
		s := ComputeMonth(prev, MonthInputs{Month: civil.YearMonth{Y: 2026, M: m}, PrevIncome: usd(4000), Lines: lines})
		prev = &s
	}
	if prev.Envelopes[Goal("trip")] != usd(250) {
		t.Fatalf("capped at the target: %v", prev.Envelopes)
	}
	if prev.Envelopes[EnvSavings] < usd(150) {
		t.Fatalf("the excess went to savings: %v", prev.Envelopes[EnvSavings])
	}
	buy := ComputeMonth(prev, MonthInputs{Month: civil.YearMonth{Y: 2026, M: 7}, PrevIncome: usd(4000), Lines: FixedLines{Goals: []Line{{ID: "trip", Target: usd(250)}}},
		Outflows: []Outflow{{ID: "p", Seq: 1, Bucket: BucketGoalPurchase, GoalID: "trip", Amount: usd(400)}}})
	if buy.Envelopes[Goal("trip")] != 0 || buy.SpendLeft != buy.SpendPool-usd(150) || !buy.Has(FlagGoalPurchased) {
		t.Fatalf("the goal pays 250, the excess 150 counts as spending: %+v", buy)
	}
}

func TestS8_TargetFromTrailingOutflowAndExcess(t *testing.T) {
	in := MonthInputs{Month: civil.YearMonth{Y: 2026, M: 3}, PrevIncome: usd(4000), TrailingOutflow: [3]money.Cents{usd(1000), usd(2000), usd(3000)},
		Events: []Event{{ID: 1, At: time.Unix(1, 0), Kind: EvOpening, Dest: EnvEF, Amount: usd(7000)}}}
	m := ComputeMonth(nil, in)
	if m.EFTarget != usd(6000) {
		t.Fatalf("3 months of the trailing outflow: %v", m.EFTarget)
	}
	if m.Envelopes[EnvEF] != usd(6000) || m.Envelopes[EnvSavings] != usd(1000) {
		t.Fatalf("above target goes to savings: %v", m.Envelopes)
	}
	six := in
	six.EFMonths = 6
	if got := ComputeMonth(nil, six).EFTarget; got != usd(12000) {
		t.Fatalf("N is configurable: %v", got)
	}
	fixed := usd(500)
	ov := in
	ov.EFTargetOverride = &fixed
	if got := ComputeMonth(nil, ov).EFTarget; got != fixed {
		t.Fatalf("a fixed override: %v", got)
	}
}

func TestS2_UnallocatedPotSweepsToSavings(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	s.month("2026-03").prevIncome(usd(5000)).run() // pot 1000, untouched
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	if apr.Envelopes[EnvSavings] != usd(1000) {
		t.Fatalf("savings %v", apr.Envelopes[EnvSavings])
	}
}

func TestA9_Inv6_RolloverTwiceSame(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	feb := s.month("2026-02").prevIncome(usd(4000)).spend(usd(2300)).run()
	in := MonthInputs{Month: civil.YearMonth{Y: 2026, M: 3}, PrevIncome: usd(4000)}
	a, b := ComputeMonth(&feb, in), ComputeMonth(&feb, in)
	if a.InputHash != b.InputHash || a.Deductions != b.Deductions || a.Envelopes[EnvSavings] != b.Envelopes[EnvSavings] {
		t.Fatal("rolling over twice must give the same state")
	}
}

func TestA9_Inv14_RejectTentativeRevertsExactly(t *testing.T) {
	t.Skip("needs links, M7")
}
