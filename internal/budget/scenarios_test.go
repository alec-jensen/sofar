package budget

import (
	"testing"
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

const dollar = 100

func usd(n int64) money.Cents { return money.Cents(n * dollar) }

// scenario chains months the way the spec's worked examples read.
type scenario struct {
	t     *testing.T
	fixed money.Cents
	prev  *MonthState
	seq   int64
	// keep leaves unspent pool money alone; by default each month pays its
	// bill and spends the whole pool so sweeps do not blur what is being checked.
	keep bool
}

func newScenario(t *testing.T) *scenario { return &scenario{t: t} }

// withFixed sets the monthly fixed bills reserve.
func (s *scenario) withFixed(c money.Cents) *scenario { s.fixed = c; return s }

type step struct {
	s      *scenario
	in     MonthInputs
	extra  []StandingLine
	causes []Cause
}

// month starts a month, e.g. "2026-02", whose plan comes from prevIncome.
func (s *scenario) month(ym string) *step {
	m, err := civil.ParseYearMonth(ym)
	if err != nil {
		s.t.Fatal(err)
	}
	return &step{s: s, in: MonthInputs{Month: m}}
}

func (st *step) prevIncome(c money.Cents) *step { st.in.PrevIncome = c; return st }
func (st *step) standing(l StandingLine) *step  { st.extra = append(st.extra, l); return st }
func (st *step) cause(c Cause) *step            { st.causes = append(st.causes, c); return st }
func (st *step) override(c money.Cents) *step   { st.in.PlanOverride = &c; return st }

func (st *step) event(e Event) *step {
	st.s.seq++
	e.ID = st.s.seq
	e.At = time.Date(st.in.Month.Y, st.in.Month.M, 1, 0, 0, int(e.ID), 0, time.UTC)
	st.in.Events = append(st.in.Events, e)
	return st
}

func (st *step) once(dest EnvelopeID, c money.Cents) *step {
	return st.event(Event{Kind: EvAllocation, Dest: dest, Amount: c})
}

func (st *step) permanent(dest EnvelopeID, c money.Cents) *step {
	return st.event(Event{Kind: EvAllocation, Dest: dest, Amount: c, Permanent: true})
}

func (st *step) spend(c money.Cents) *step {
	st.s.seq++
	st.in.Outflows = append(st.in.Outflows, Outflow{ID: "o", Seq: st.s.seq, Date: st.in.Month.First(), Bucket: BucketSpending, Amount: c})
	return st
}

// run computes the month, chains it as the next prev, and returns the state.
func (st *step) run() MonthState {
	s := st.s
	if s.fixed > 0 {
		st.in.Lines.Bills = []Line{{ID: "fixed", Label: "fixed", Amount: s.fixed}}
	}
	st.in.Lines.Standing = append(st.in.Lines.Standing, st.extra...)
	st.in.AbsorptionCauses = st.causes
	if !s.keep {
		if s.fixed > 0 {
			st.in.Outflows = append(st.in.Outflows, Outflow{ID: "bill", Seq: 1000, Date: st.in.Month.First(), Bucket: BucketBills, LineID: "fixed", Amount: s.fixed})
		}
		first := ComputeMonth(s.prev, st.in)
		if left := first.SpendLeft; left > 0 {
			st.in.Outflows = append(st.in.Outflows, Outflow{ID: "rest", Seq: 2000, Date: st.in.Month.First(), Bucket: BucketSpending, Amount: left})
		}
	}
	got := ComputeMonth(s.prev, st.in)
	s.prev = &got
	return got
}

type want struct {
	plan, fixed, base, pot, pool money.Cents
}

func (s MonthState) check(t *testing.T, label string, w want) {
	t.Helper()
	if s.Plan != w.plan || s.Fixed != w.fixed || s.SpendBase != w.base || s.Pot != w.pot || s.SpendPool != w.pool {
		t.Errorf("%s: plan/fixed/base/pot/pool = %v/%v/%v/%v/%v, want %v/%v/%v/%v/%v", label, s.Plan, s.Fixed, s.SpendBase, s.Pot, s.SpendPool, w.plan, w.fixed, w.base, w.pot, w.pool)
	}
}

func TestS2_ExampleA_ThreePaycheckMonth(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	feb := s.month("2026-02").prevIncome(usd(4000)).run()
	feb.check(t, "feb", want{usd(4000), usd(2000), usd(2000), 0, usd(2000)})
	mar := s.month("2026-03").prevIncome(usd(6000)).once(Goal("phone"), usd(2000)).run()
	mar.check(t, "mar", want{usd(6000), usd(2000), usd(2000), usd(2000), usd(2000)})
	if mar.PotAllocated != usd(2000) || mar.PotUnallocated != 0 || mar.Envelopes[Goal("phone")] != usd(2000) {
		t.Errorf("the phone goal should hold the pot: %+v", mar)
	}
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	apr.check(t, "apr", want{usd(4000), usd(2000), usd(2000), 0, usd(2000)})
	if len(apr.Absorption) != 0 {
		t.Errorf("spend_base never moved, so nothing is absorbed: %+v", apr.Absorption)
	}
	if apr.Envelopes[EnvSavings] != 0 {
		t.Errorf("an allocated pot does not sweep to savings: %v", apr.Envelopes[EnvSavings])
	}
}

func TestS2_ExampleB_TemporarySpikeSplit(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	mar := s.month("2026-03").prevIncome(usd(6000)).once(EnvSavings, usd(1500)).once(EnvSpendPool, usd(500)).run()
	mar.check(t, "mar", want{usd(6000), usd(2000), usd(2000), usd(2000), usd(2500)})
	if mar.PotUnallocated != 0 || mar.Envelopes[EnvSavings] != usd(1500) {
		t.Errorf("1500 to savings, nothing left in the pot: %+v", mar)
	}
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	apr.check(t, "apr", want{usd(4000), usd(2000), usd(2000), 0, usd(2000)})
	if apr.Envelopes[EnvSavings] != usd(1500) {
		t.Errorf("savings = %v, want the 1500 allocation", apr.Envelopes[EnvSavings])
	}
}

func TestS2_ExampleC_PermanentRaiseThenLoss(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	mar := s.month("2026-03").prevIncome(usd(6000)).permanent(EnvSavings, usd(1500)).permanent(EnvSpendPool, usd(500)).run()
	mar.check(t, "mar", want{usd(6000), usd(3500), usd(2500), 0, usd(2500)})
	apr := s.month("2026-04").prevIncome(usd(6000)).
		standing(StandingLine{ID: "raise", Label: "from the raise", Amount: usd(1500), Dest: EnvSavings}).run()
	apr.check(t, "apr", want{usd(6000), usd(3500), usd(2500), 0, usd(2500)})
	later := s.month("2026-05").prevIncome(usd(4000)).
		standing(StandingLine{ID: "raise", Amount: usd(1500), Dest: EnvSavings}).
		cause(Cause{Kind: "standing_lines", Label: "standing lines added from the raise", Amount: usd(1500), Ref: "raise"}).run()
	later.check(t, "later", want{usd(4000), usd(3500), usd(500), 0, usd(500)})
	if len(later.Absorption) != 1 || later.Absorption[0].Amount != usd(2000) || later.Absorption[0].From != usd(2500) || later.Absorption[0].To != usd(500) {
		t.Fatalf("absorption: %+v", later.Absorption)
	}
	if later.Absorption[0].Causes[0].Kind != "standing_lines" {
		t.Errorf("the cause is attached: %+v", later.Absorption[0].Causes)
	}
}

func TestS2_ExampleD_DropAndRecovery(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	drop := s.month("2026-03").prevIncome(usd(2000)).run()
	drop.check(t, "drop", want{usd(2000), usd(2000), 0, 0, 0})
	if len(drop.Absorption) != 1 || drop.Absorption[0].Amount != usd(2000) {
		t.Fatalf("absorbed 2000: %+v", drop.Absorption)
	}
	rec := s.month("2026-04").prevIncome(usd(4000)).run()
	rec.check(t, "recovery", want{usd(4000), usd(2000), 0, usd(2000), 0})
	if rec.PotUnallocated != usd(2000) {
		t.Errorf("the whole pot is waiting to be allocated: %v", rec.PotUnallocated)
	}
	next := s.month("2026-05").prevIncome(usd(4000)).run()
	if next.Envelopes[EnvSavings] != usd(2000) {
		t.Errorf("an untouched pot goes to savings at month end: %v", next.Envelopes[EnvSavings])
	}
	if next.SpendBase != 0 {
		t.Errorf("spend_base never rises on its own: %v", next.SpendBase)
	}
}

func TestS2_LeftoverPoolSweepsToSavings(t *testing.T) { // A-002
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).spend(usd(1200)).run()
	mar := s.month("2026-03").prevIncome(usd(4000)).run()
	if mar.Envelopes[EnvSavings] != usd(800)+usd(2000) { // unspent pool plus the unspent bill reserve
		t.Errorf("savings = %v", mar.Envelopes[EnvSavings])
	}
}

// Example F at the engine: the assembly nets a linked reimbursement into the
// expense's month, so the engine just sees less spending, and a cover that was
// larger than the new overspend shrinks and its source is refunded (A-006).
func TestS2_ExampleF_ReimbursementAcrossMonths(t *testing.T) {
	build := func(spent money.Cents) MonthState {
		s := newScenario(t).withFixed(usd(2000))
		s.keep = true
		s.month("2026-02").prevIncome(usd(4000)).run()
		s.month("2026-03").prevIncome(usd(4000)).spend(spent).cover(EnvSavings, usd(180)).run()
		return s.month("2026-04").prevIncome(usd(4000)).run()
	}
	gross, netted := build(usd(2180)), build(usd(2130)) // a $50 reimbursement lands in April
	if gross.Envelopes[EnvSavings]-netted.Envelopes[EnvSavings] != -usd(50) {
		t.Fatalf("the cover shrinks by $50 and savings is refunded: %v vs %v", gross.Envelopes[EnvSavings], netted.Envelopes[EnvSavings])
	}
}

func TestS2_FirstPlanIsPlanMinusFixed(t *testing.T) {
	got := newScenario(t).withFixed(usd(1500)).month("2026-02").prevIncome(usd(4000)).run()
	got.check(t, "first", want{usd(4000), usd(1500), usd(2500), 0, usd(2500)})
	over := newScenario(t).withFixed(usd(5000)).month("2026-02").prevIncome(usd(4000)).run()
	if over.SpendBase != 0 || over.Overcommitted != usd(1000) || !over.Has(FlagOvercommitted) {
		t.Errorf("fixed above plan: base 0 and overcommitted by 1000, got %+v", over)
	}
}

func TestS2_ManualEditPinsSpendBase(t *testing.T) { // A-001
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	mar := s.month("2026-03").prevIncome(usd(4000)).event(Event{Kind: EvSpendBaseEdit, Amount: usd(2800)}).run()
	if mar.SpendBase != usd(2800) || mar.PinnedSpendBase != usd(2800) || mar.Overcommitted != usd(800) {
		t.Fatalf("edit allowed and overcommitted by 800: %+v", mar)
	}
	apr := s.month("2026-04").prevIncome(usd(4000)).run()
	if apr.SpendBase != usd(2800) || len(apr.Absorption) != 0 {
		t.Errorf("a pin is never absorbed away: %+v", apr)
	}
	rem := s.month("2026-05").prevIncome(usd(4000)).event(Event{Kind: EvPinRemoved}).run()
	jun := s.month("2026-06").prevIncome(usd(4000)).run()
	if rem.PinnedSpendBase != 0 || jun.SpendBase != usd(2000) || len(jun.Absorption) != 1 {
		t.Errorf("once the pin is removed absorption cuts back: %+v %+v", rem, jun)
	}
}

func TestS2_AbsorptionRunsBeforeThisMonthsDecisions(t *testing.T) { // A-005
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	// the plan drops to 3000 and the user allocates permanently in the same month
	m := s.month("2026-03").prevIncome(usd(3000)).permanent(EnvSavings, usd(200)).run()
	if len(m.Absorption) != 1 || m.Absorption[0].To != usd(1000) {
		t.Fatalf("absorption uses the data-driven available (3000-2000): %+v", m.Absorption)
	}
	if m.Fixed != usd(2200) || m.Overcommitted != usd(200) {
		t.Errorf("the allocation then applies and overcommits: %+v", m)
	}
}

func TestS2_PotTakeBack(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	// pot 2000 allocated: 1000 discretionary, 1000 savings; late data later shrinks the pot to 1500
	m := s.month("2026-03").prevIncome(usd(5500)).once(EnvDiscretionary, usd(1000)).once(EnvSavings, usd(1000)).run()
	if m.Pot != usd(1500) || m.PotUnallocated != -usd(500) {
		t.Fatalf("pot: %+v", m)
	}
	if !m.Has(FlagTakeBack) || m.Envelopes[EnvDiscretionary] != usd(500) || m.Envelopes[EnvSavings] != usd(1000) {
		t.Errorf("discretionary gives back first: disc %v savings %v", m.Envelopes[EnvDiscretionary], m.Envelopes[EnvSavings])
	}
}

func TestS10_SpendingDrawsPoolThenDiscretionaryThenOverspend(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.keep = true
	s.month("2026-02").prevIncome(usd(4000)).run()
	m := s.month("2026-03").prevIncome(usd(4000)).spend(usd(1500)).run()
	if m.SpendLeft != usd(500) || m.OverspendGross != 0 {
		t.Fatalf("1500 out of 2000: %+v", m)
	}
	m = s.month("2026-04").prevIncome(usd(4000)).spend(usd(2300)).run()
	// March left 500 which swept to savings; April pool 2000, spend 2300
	if m.SpendLeft != -usd(300) || m.OverspendGross != usd(300) || m.OverspendOpen != usd(300) {
		t.Fatalf("overspent by 300: %+v", m)
	}
	n := s.month("2026-05").prevIncome(usd(4000)).run()
	if n.Deductions != usd(300) || n.SpendLeft != usd(1700) || !n.Has(FlagAutoCarried) {
		t.Errorf("unresolved overspend auto-carries into next month: %+v", n)
	}
}

func TestS9_DiscretionaryDrawsOnlyAfterPoolIsEmpty(t *testing.T) {
	s := newScenario(t).withFixed(usd(2000))
	s.month("2026-02").prevIncome(usd(4000)).run()
	s.month("2026-03").prevIncome(usd(5000)).once(EnvDiscretionary, usd(1000)).run()
	m := s.month("2026-04").prevIncome(usd(4000)).spend(usd(2400)).run()
	if m.SpendLeft != 0 || m.OverspendGross != 0 || m.Envelopes[EnvDiscretionary] != usd(600) {
		t.Fatalf("pool 2000 then 400 from discretionary: %+v", m)
	}
}

func TestS10_PerDay(t *testing.T) {
	today := civil.Date{Y: 2026, M: 10, D: 5}
	in := MonthInputs{Month: civil.YearMonth{Y: 2026, M: 10}, PrevIncome: usd(3100), Today: &today}
	m := ComputeMonth(nil, in)
	if m.PerDay*27+m.LastDayExtra != m.SpendLeft {
		t.Errorf("perDay*days + extra == spend_left: %+v", m)
	}
	last := civil.Date{Y: 2026, M: 10, D: 31}
	in.Today = &last
	if m := ComputeMonth(nil, in); m.PerDay != m.SpendLeft || m.LastDayExtra != 0 {
		t.Errorf("on the last day per-day is everything left: %+v", m)
	}
	neg := ComputeMonth(nil, MonthInputs{Month: in.Month, PrevIncome: usd(100), Today: &today, Outflows: []Outflow{{ID: "a", Date: today, Bucket: BucketSpending, Amount: usd(500)}}})
	if neg.PerDay != 0 || neg.SpendLeft >= 0 {
		t.Errorf("overspent: negative total, zero per day: %+v", neg)
	}
}

func TestS4_OverrideAndLearning(t *testing.T) {
	m := newScenario(t).month("2026-02").prevIncome(usd(4000)).override(usd(3000)).run()
	if m.Plan != usd(3000) {
		t.Errorf("override wins: %v", m.Plan)
	}
	in := MonthInputs{Month: civil.YearMonth{Y: 2026, M: 2}, PrevIncome: usd(4000), Learning: true}
	l := ComputeMonth(nil, in)
	if l.Plan != 0 || !l.Learning || !l.Has(FlagLearning) {
		t.Errorf("learning: %+v", l)
	}
	ov := usd(2500)
	in.PlanOverride = &ov
	if o := ComputeMonth(nil, in); o.Plan != ov || o.Learning {
		t.Errorf("a manual starting plan ends learning: %+v", o)
	}
	next := ComputeMonth(&l, MonthInputs{Month: civil.YearMonth{Y: 2026, M: 3}, PrevIncome: usd(3000)})
	if next.SpendBase != usd(3000) {
		t.Errorf("the first real plan after learning sets spend_base: %+v", next)
	}
}

func TestS5_BillsConsumeTheirReserveAndUnderageSweeps(t *testing.T) {
	in := MonthInputs{
		Month: civil.YearMonth{Y: 2026, M: 3}, PrevIncome: usd(3000),
		Lines: FixedLines{Bills: []Line{{ID: "rent", Amount: usd(1000)}, {ID: "power", Amount: usd(150)}}},
		Outflows: []Outflow{
			{ID: "1", Seq: 1, Bucket: BucketBills, LineID: "rent", Amount: usd(1000)},
			{ID: "2", Seq: 2, Bucket: BucketBills, LineID: "power", Amount: usd(100)},
			{ID: "3", Seq: 3, Bucket: BucketBills, LineID: "unknown", Amount: usd(30)},
		},
	}
	m := ComputeMonth(nil, in)
	if m.SpendLeft != m.SpendPool-usd(30) {
		t.Errorf("a bill with no line draws from spending: %+v", m)
	}
	if m.BillsUnderage != usd(50) {
		t.Errorf("underage = %v, want 50", m.BillsUnderage)
	}
	in.Month = civil.YearMonth{Y: 2026, M: 4}
	n := ComputeMonth(&m, in)
	if n.Envelopes[EnvSavings] < usd(50) {
		t.Errorf("underage swept to savings: %v", n.Envelopes[EnvSavings])
	}
	// overage on a bill beyond its reserve draws from spending
	over := in
	over.Outflows = []Outflow{{ID: "1", Seq: 1, Bucket: BucketBills, LineID: "power", Amount: usd(200)}}
	o := ComputeMonth(nil, over)
	if o.SpendLeft != o.SpendPool-usd(50) {
		t.Errorf("a bill over its reserve draws the excess from spending: %+v", o)
	}
}

func TestS8_RebuildComesFromSavingsFirst(t *testing.T) { // A-007
	in := MonthInputs{Month: civil.YearMonth{Y: 2026, M: 3}, PrevIncome: usd(4000), Lines: FixedLines{
		Savings: SavingsRule{Fixed: usd(300)},
		Rebuild: []Line{{ID: "r1", Amount: usd(200)}, {ID: "r2", Amount: usd(250)}},
	}}
	m := ComputeMonth(nil, in)
	// 300 savings: r1 takes 200 from it, r2 takes 100 and 150 more adds to fixed
	if m.Fixed != usd(300)+usd(150) || m.Envelopes[EnvEF] != usd(450) || m.Envelopes[EnvSavings] != 0 {
		t.Errorf("fixed %v ef %v savings %v", m.Fixed, m.Envelopes[EnvEF], m.Envelopes[EnvSavings])
	}
	if !m.Has(FlagRebuildFromFixed) {
		t.Error("spending was reduced by the rebuild: flag it")
	}
}
