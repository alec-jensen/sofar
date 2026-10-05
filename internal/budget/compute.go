package budget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

type computer struct {
	in   MonthInputs
	prev *MonthState
	st   MonthState
	env  map[EnvelopeID]money.Cents
}

func (c *computer) entry(from, to EnvelopeID, amount money.Cents, kind, ref string) {
	if amount <= 0 {
		return
	}
	c.st.Entries = append(c.st.Entries, Entry{From: from, To: to, Amount: amount, Kind: kind, Ref: ref})
}

func sumLines(ls []Line) (t money.Cents) {
	for _, l := range ls {
		t += l.Amount
	}
	return
}

// ComputeMonth is the single place budget math lives. It is pure: the same
// prev, inputs and EngineVersion always give the same state. prev == nil means
// there is no earlier computed month. See docs/plan/engine-semantics.md.
func ComputeMonth(prev *MonthState, in MonthInputs) MonthState {
	in = normalized(in)
	c := &computer{in: in, prev: prev, env: map[EnvelopeID]money.Cents{}}
	c.st.Month, c.st.Partial, c.st.EngineVersion = in.Month, in.Partial, EngineVersion
	if prev != nil {
		for k, v := range prev.Envelopes {
			if k.persistent() {
				c.env[k] = v
			}
		}
	}
	c.rolloverFromPrev()
	c.plan()
	c.fixed()
	c.spendBase()
	c.events()
	c.pot()
	c.pool()
	c.outflows()
	c.perDay()
	c.finish()
	return c.st
}

// normalized sorts every input slice so callers need not, and copies them so
// the caller's data is never modified.
func normalized(in MonthInputs) MonthInputs {
	in.Events = append([]Event(nil), in.Events...)
	sort.SliceStable(in.Events, func(i, j int) bool {
		a, b := in.Events[i], in.Events[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.ID < b.ID
	})
	in.Outflows = append([]Outflow(nil), in.Outflows...)
	sort.SliceStable(in.Outflows, func(i, j int) bool {
		a, b := in.Outflows[i], in.Outflows[j]
		if a.Date != b.Date {
			return a.Date.Before(b.Date)
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.ID < b.ID
	})
	in.Income = append([]IncomeItem(nil), in.Income...)
	sort.SliceStable(in.Income, func(i, j int) bool { return in.Income[i].ID < in.Income[j].ID })
	l := &in.Lines
	for _, s := range []*[]Line{&l.Bills, &l.Subscriptions, &l.Sinking, &l.PaymentPlans, &l.Goals, &l.Rebuild} {
		*s = append([]Line(nil), *s...)
		ss := *s
		sort.SliceStable(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
	}
	l.Standing = append([]StandingLine(nil), l.Standing...)
	sort.SliceStable(l.Standing, func(i, j int) bool { return l.Standing[i].ID < l.Standing[j].ID })
	return in
}

// rolloverFromPrev derives what month M inherits from M-1 (engine-semantics section 5).
func (c *computer) rolloverFromPrev() {
	p := c.prev
	if p == nil {
		return
	}
	ref := "from:" + p.Month.String()
	if left := money.Max(0, p.SpendLeft); left > 0 { // A-002
		c.env[EnvSavings] += left
		c.entry(EnvSpendPool, EnvSavings, left, KindSweepLeftover, ref)
	}
	if pot := money.Max(0, p.PotUnallocated); pot > 0 {
		c.env[EnvSavings] += pot
		c.entry(EnvPot, EnvSavings, pot, KindSweepPot, ref)
	}
	if p.BillsUnderage > 0 {
		c.env[EnvSavings] += p.BillsUnderage
		c.entry(EnvBillsReserve, EnvSavings, p.BillsUnderage, KindSweepUnderage, ref)
	}
}

// plan is step 4a.
func (c *computer) plan() {
	switch {
	case c.in.PlanOverride != nil:
		c.st.Plan = *c.in.PlanOverride
	case c.in.Learning:
		c.st.Plan, c.st.Learning = 0, true
		c.st.Flags = append(c.st.Flags, Flag{Kind: FlagLearning})
	default:
		c.st.Plan = c.in.PrevIncome
	}
	if c.in.Partial {
		c.st.Flags = append(c.st.Flags, Flag{Kind: FlagPartial})
	}
	for _, it := range c.in.Income {
		c.st.Income += it.Amount
	}
	c.entry(EnvIncome, EnvPlan, money.Max(0, c.st.Plan), KindIncome, "")
}

// fixed is engine-semantics section 3, plus the funding entries.
func (c *computer) fixed() {
	l := c.in.Lines
	var fixed money.Cents
	fund := func(ls []Line, dest EnvelopeID, kind string, destFor func(Line) EnvelopeID) {
		for _, x := range ls {
			if x.Amount <= 0 {
				continue
			}
			to := dest
			if destFor != nil {
				to = destFor(x)
			}
			fixed += x.Amount
			c.entry(EnvPlan, to, x.Amount, kind, x.ID)
			if to != EnvBillsReserve && to != EnvSubsReserve {
				c.env[to] += x.Amount
			}
		}
	}
	fund(l.Bills, EnvBillsReserve, KindFundBills, nil)
	fund(l.Subscriptions, EnvSubsReserve, KindFundSubs, nil)
	fund(l.Sinking, "", KindFundSinking, func(x Line) EnvelopeID { return Sinking(x.ID) })
	fund(l.PaymentPlans, EnvBillsReserve, KindFundBills, nil)
	fund(l.Goals, "", KindFundGoal, func(x Line) EnvelopeID { return Goal(x.ID) })
	if l.TaxSetAside != nil {
		fund([]Line{*l.TaxSetAside}, EnvSavings, KindFundTax, nil)
	}

	// general savings, with the EF rebuild redirect (A-007): each rebuild amount
	// comes out of the savings contribution first; only the remainder adds to fixed.
	savings := l.Savings.Fixed
	if l.Savings.UseBasisPoints {
		savings = money.Pct(c.st.Plan, l.Savings.BasisPoints)
	}
	savings = money.Max(0, savings)
	fixed += savings
	remaining := savings
	var rebuildTotal, extraTotal money.Cents
	for _, r := range l.Rebuild {
		if r.Amount <= 0 {
			continue
		}
		from := money.Min(r.Amount, remaining)
		remaining -= from
		extra := r.Amount - from
		extraTotal += extra
		rebuildTotal += r.Amount
		if extra > 0 {
			c.st.Flags = append(c.st.Flags, Flag{Kind: FlagRebuildFromFixed, Ref: r.ID, Amount: extra})
		}
	}
	fixed += extraTotal
	c.entry(EnvPlan, EnvSavings, remaining, KindFundSavings, "")
	c.env[EnvSavings] += remaining
	c.entry(EnvPlan, EnvEF, rebuildTotal, KindFundRebuild, "")
	c.env[EnvEF] += rebuildTotal

	if l.EF.Amount > 0 {
		fixed += l.EF.Amount
		c.entry(EnvPlan, EnvEF, l.EF.Amount, KindFundEF, l.EF.ID)
		c.env[EnvEF] += l.EF.Amount
	}
	for _, s := range l.Standing {
		if s.Amount <= 0 {
			continue
		}
		fixed += s.Amount
		c.entry(EnvPlan, s.Dest, s.Amount, KindFundStanding, s.ID)
		c.env[s.Dest] += s.Amount
	}
	c.st.Fixed = fixed
}

// spendBase is step 4b: first plan, or absorption against the data-driven
// available, before this month's own events (A-005).
func (c *computer) spendBase() {
	available := c.st.Plan - c.st.Fixed
	p := c.prev
	if p == nil || p.Learning {
		c.st.SpendBase = money.Max(0, available)
		return
	}
	base := p.SpendBase
	c.st.PinnedSpendBase = p.PinnedSpendBase // A-001: carries until edited or removed
	if available < base {
		newBase := money.Max(money.Max(0, available), p.PinnedSpendBase)
		if absorbed := base - newBase; absorbed > 0 {
			c.st.Absorption = append(c.st.Absorption, AbsorptionEntry{Amount: absorbed, From: base, To: newBase, Causes: append([]Cause(nil), c.in.AbsorptionCauses...)})
		}
		base = newBase
	}
	c.st.SpendBase = base
}

type oneTime struct {
	ev     Event
	amount money.Cents // effective, after any take-back
}

// events is step 4c: this month's user events, in (At, ID) order.
func (c *computer) events() {
	for _, e := range c.in.Events {
		switch e.Kind {
		case EvSpendBaseEdit:
			c.st.SpendBase = money.Max(0, e.Amount)
			c.st.PinnedSpendBase = e.Amount
		case EvPinRemoved:
			c.st.PinnedSpendBase = 0
		case EvOpening:
			if e.Amount != 0 && e.Dest != "" {
				c.env[e.Dest] += e.Amount
				if e.Amount > 0 {
					c.entry(EnvWorld, e.Dest, e.Amount, KindOpening, e.Ref)
				}
			}
		case EvAllocation:
			if e.Permanent && e.Amount > 0 {
				if e.Dest == EnvSpendPool {
					c.st.SpendBase += e.Amount
				} else {
					c.st.Fixed += e.Amount
					c.env[e.Dest] += e.Amount
					c.entry(EnvPlan, e.Dest, e.Amount, KindFundStanding, e.Ref)
				}
			}
		}
	}
	if c.st.PinnedSpendBase > 0 {
		c.st.Flags = append(c.st.Flags, Flag{Kind: FlagPinned, Amount: c.st.PinnedSpendBase})
	}
}

// pot is steps 4d and 4e.
func (c *computer) pot() {
	st := &c.st
	st.Available = st.Plan - st.Fixed
	st.Pot = money.Max(0, st.Available-st.SpendBase)
	st.Overcommitted = money.Max(0, st.Fixed+st.SpendBase-st.Plan)
	if st.Overcommitted > 0 {
		st.Flags = append(st.Flags, Flag{Kind: FlagOvercommitted, Amount: st.Overcommitted})
		c.entry(EnvIncome, EnvPlan, st.Overcommitted, KindOvercommitted, "")
	}
	c.entry(EnvPlan, EnvSpendPool, st.SpendBase, KindFundSpending, "")
	c.entry(EnvPlan, EnvPot, st.Pot, KindPot, "")

	var allocs []oneTime
	var total money.Cents
	for _, e := range c.in.Events {
		if e.Kind == EvAllocation && !e.Permanent && e.Amount > 0 {
			allocs = append(allocs, oneTime{e, e.Amount})
			total += e.Amount
		}
	}
	// If late data shrank the pot below what was allocated, take the difference
	// back: discretionary first (latest first), then savings and goals (latest first).
	if over := total - st.Pot; over > 0 {
		for _, group := range []func(EnvelopeID) bool{
			func(d EnvelopeID) bool { return d == EnvDiscretionary },
			func(d EnvelopeID) bool { return d == EnvSavings || d.isGoal() },
		} {
			for i := len(allocs) - 1; i >= 0 && over > 0; i-- {
				if !group(allocs[i].ev.Dest) {
					continue
				}
				cut := money.Min(over, allocs[i].amount)
				allocs[i].amount -= cut
				over -= cut
				c.entry(allocs[i].ev.Dest, EnvPot, cut, KindTakeBack, allocs[i].ev.Ref)
				st.Flags = append(st.Flags, Flag{Kind: FlagTakeBack, Ref: allocs[i].ev.Ref, Amount: cut})
			}
		}
	}
	for _, a := range allocs {
		st.PotAllocated += a.ev.Amount
		c.entry(EnvPot, a.ev.Dest, a.ev.Amount, KindAllocate, a.ev.Ref)
		if a.amount > 0 && a.ev.Dest != EnvSpendPool {
			c.env[a.ev.Dest] += a.amount
		}
		if a.ev.Dest == EnvSpendPool {
			st.SpendPool += a.amount
		}
	}
	st.PotUnallocated = st.Pot - st.PotAllocated
	if st.PotUnallocated < 0 {
		st.Flags = append(st.Flags, Flag{Kind: FlagOverAllocated, Amount: -st.PotUnallocated})
	}
	// moves between persistent envelopes happen outside the plan
	for _, e := range c.in.Events {
		if e.Kind == EvMove && e.Amount > 0 && e.Source != "" && e.Dest != "" {
			c.env[e.Source] -= e.Amount
			c.env[e.Dest] += e.Amount
			c.entry(e.Source, e.Dest, e.Amount, KindAllocate, e.Ref)
		}
	}
}

// pool is step 4f; the result is held in poolAvail via SpendLeft until outflows run.
func (c *computer) pool() {
	st := &c.st
	st.SpendPool += st.SpendBase
	if p := c.prev; p != nil {
		st.Deductions = p.CarryToNext + money.Max(0, p.OverspendOpen)
		if p.OverspendOpen > 0 {
			st.Flags = append(st.Flags, Flag{Kind: FlagAutoCarried, Amount: p.OverspendOpen})
		}
	}
	st.SpendLeft = st.SpendPool - st.Deductions
}

// outflows is step 4g.
func (c *computer) outflows() {
	st := &c.st
	poolAvail := st.SpendLeft
	disc := c.env[EnvDiscretionary]
	reserve := map[string]money.Cents{}
	monthly := map[string]bool{}
	for _, l := range c.in.Lines.Bills {
		reserve[l.ID] += l.Amount
		monthly[l.ID] = true
	}
	for _, l := range c.in.Lines.Subscriptions {
		reserve[l.ID] += l.Amount
		monthly[l.ID] = true
	}
	for _, l := range c.in.Lines.PaymentPlans {
		reserve[l.ID] += l.Amount
		monthly[l.ID] = true
	}
	for _, l := range c.in.Lines.Sinking {
		reserve[l.ID] += l.Amount + c.prevBalance(Sinking(l.ID))
	}

	draw := func(a money.Cents, ref string) {
		fromPool := money.Min(a, money.Max(0, poolAvail))
		poolAvail -= fromPool
		a -= fromPool
		c.entry(EnvSpendPool, EnvWorld, fromPool, KindSpend, ref)
		fromDisc := money.Min(a, money.Max(0, disc))
		disc -= fromDisc
		a -= fromDisc
		c.entry(EnvDiscretionary, EnvWorld, fromDisc, KindSpend, ref)
		if a > 0 {
			poolAvail -= a
			st.OverspendGross += a
			c.entry(EnvSpendPool, EnvWorld, a, KindSpend, ref)
		}
	}
	for _, o := range c.in.Outflows {
		if o.Amount <= 0 {
			continue
		}
		switch o.Bucket {
		case BucketSpending:
			if o.FromDiscretionary {
				disc -= o.Amount
				c.entry(EnvDiscretionary, EnvWorld, o.Amount, KindSpend, o.ID)
			} else {
				draw(o.Amount, o.ID)
			}
		case BucketBills, BucketSubscriptions: // A-004: subscriptions behave like bills
			rem, ok := reserve[o.LineID]
			if !ok {
				draw(o.Amount, o.ID) // no matching line: draws like spending
				continue
			}
			take := money.Min(o.Amount, money.Max(0, rem))
			reserve[o.LineID] = rem - take
			if monthly[o.LineID] {
				c.entry(EnvBillsReserve, EnvWorld, take, KindSpend, o.ID)
			} else {
				c.env[Sinking(o.LineID)] -= take
				c.entry(Sinking(o.LineID), EnvWorld, take, KindSpend, o.ID)
			}
			if over := o.Amount - take; over > 0 {
				draw(over, o.ID)
			}
		default: // savings, goal purchases and neutral movements are handled in M5
		}
	}
	st.SpendLeft = poolAvail
	st.OverspendOpen = st.OverspendGross - st.CoversApplied
	c.env[EnvDiscretionary] = disc
	var ids []string
	for id := range reserve {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if monthly[id] {
			st.BillsUnderage += money.Max(0, reserve[id])
		}
	}
}

func (c *computer) prevBalance(e EnvelopeID) money.Cents {
	if c.prev == nil {
		return 0
	}
	return c.prev.Envelopes[e]
}

// perDay is step 4i.
func (c *computer) perDay() {
	if c.in.Today == nil {
		return
	}
	c.st.PerDay, c.st.LastDayExtra = money.PerDay(money.Max(0, c.st.SpendLeft), civil.DaysLeft(*c.in.Today))
}

func (c *computer) finish() {
	c.st.Envelopes = c.env
	var h string
	if c.prev != nil {
		h = c.prev.InputHash
	}
	raw, _ := json.Marshal(struct {
		Prev   string
		In     MonthInputs
		Engine int
	}{h, c.in, EngineVersion})
	sum := sha256.Sum256(raw)
	c.st.InputHash = hex.EncodeToString(sum[:])
}
