package budget

import (
	"time"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

// EnvelopeID names a virtual envelope. Persistent envelopes carry a balance
// from month to month; monthly ones start empty every month.
type EnvelopeID string

const (
	// Monthly envelopes.
	EnvPlan         EnvelopeID = "plan"
	EnvBillsReserve EnvelopeID = "bills_reserve"
	EnvSubsReserve  EnvelopeID = "subs_reserve"
	EnvSpendPool    EnvelopeID = "spend_pool"
	EnvPot          EnvelopeID = "pot"
	EnvNextMonth    EnvelopeID = "next_month"

	// Persistent envelopes.
	EnvSavings       EnvelopeID = "savings"
	EnvEF            EnvelopeID = "ef"
	EnvDiscretionary EnvelopeID = "discretionary"
	EnvUncovered     EnvelopeID = "uncovered"
	EnvBillsGap      EnvelopeID = "bills_gap"

	// Outside the budget.
	EnvIncome EnvelopeID = "income"
	EnvWorld  EnvelopeID = "world"
	EnvTake   EnvelopeID = "take_back"
)

// Goal returns the envelope of a goal.
func Goal(id string) EnvelopeID { return EnvelopeID("goal:" + id) }

// Sinking returns the envelope of a sinking fund (a non-monthly bill's set-aside).
func Sinking(id string) EnvelopeID { return EnvelopeID("sinking:" + id) }

func (e EnvelopeID) isGoal() bool    { return len(e) > 5 && e[:5] == "goal:" }
func (e EnvelopeID) isSinking() bool { return len(e) > 8 && e[:8] == "sinking:" }

// persistent reports whether an envelope keeps its balance across months.
func (e EnvelopeID) persistent() bool {
	switch e {
	case EnvSavings, EnvEF, EnvDiscretionary, EnvUncovered, EnvBillsGap:
		return true
	}
	return e.isGoal() || e.isSinking()
}

// Entry is one double-entry movement: Amount leaves From and enters To.
type Entry struct {
	From, To EnvelopeID
	Amount   money.Cents // always > 0
	Kind     string
	Ref      string
}

// Entry kinds.
const (
	KindIncome        = "income"
	KindFundBills     = "fund_bills"
	KindFundSubs      = "fund_subscriptions"
	KindFundSinking   = "fund_sinking"
	KindFundSavings   = "fund_savings"
	KindFundGoal      = "fund_goal"
	KindFundEF        = "fund_ef"
	KindFundRebuild   = "fund_rebuild"
	KindFundStanding  = "fund_standing"
	KindFundTax       = "fund_tax"
	KindFundSpending  = "fund_spending"
	KindPot           = "pot"
	KindOvercommitted = "overcommitted"
	KindAllocate      = "allocate"
	KindTakeBack      = "take_back"
	KindSpend         = "spend"
	KindSweepLeftover = "sweep_leftover"
	KindSweepPot      = "sweep_pot"
	KindSweepUnderage = "sweep_underage"
	KindCarry         = "carry"
	KindOpening       = "opening_balance"
)

// Line is one fixed amount reserved for a month.
type Line struct {
	ID     string
	Label  string
	Amount money.Cents
}

// StandingLine is a recurring allocation created earlier by a permanent
// allocation. Dest is where it goes: savings, goal:<id>, ef or discretionary.
type StandingLine struct {
	ID     string
	Label  string
	Amount money.Cents
	Dest   EnvelopeID
	Since  civil.YearMonth
}

// SavingsRule is the general savings contribution: fixed cents, or basis
// points of the effective plan.
type SavingsRule struct {
	Fixed          money.Cents
	BasisPoints    int64
	UseBasisPoints bool
}

// FixedLines is everything reserved off the top of the plan (engine-semantics section 3).
type FixedLines struct {
	Bills         []Line
	Subscriptions []Line
	Sinking       []Line
	PaymentPlans  []Line
	Savings       SavingsRule
	Goals         []Line // ID is the goal id
	EF            Line
	Rebuild       []Line
	Standing      []StandingLine
	TaxSetAside   *Line
}

// EventKind identifies a budget event.
type EventKind string

const (
	// EvSpendBaseEdit sets spend_base manually (Amount) and pins it (A-001).
	EvSpendBaseEdit EventKind = "spend_base_edit"
	// EvPinRemoved clears the spend_base pin.
	EvPinRemoved EventKind = "spend_base_pin_removed"
	// EvAllocation sends pot money to Dest, one-time or permanent.
	EvAllocation EventKind = "allocation"
	// EvCover covers an overspend from Source (M5).
	EvCover EventKind = "cover"
	// EvOpening sets an opening balance for a persistent envelope (Dest).
	EvOpening EventKind = "opening_balance"
	// EvMove moves money between persistent envelopes (From -> To) outside the plan.
	EvMove EventKind = "move"
)

// Event is a budget decision in force for a month.
type Event struct {
	ID        int64
	At        time.Time
	Kind      EventKind
	Amount    money.Cents
	Dest      EnvelopeID // allocation or opening balance destination, or move target
	Source    EnvelopeID // cover or move source
	Permanent bool
	Ref       string
}

// Outflow is spending in the month, already netted by reimbursement and
// refund links, already split into parts, excluding pending and hidden rows.
type Outflow struct {
	ID                string
	Seq               int64
	Date              civil.Date
	Bucket            string // spending, bills, subscriptions, savings, goal_purchase, neutral
	Amount            money.Cents
	FromDiscretionary bool
	LineID            string // the bill, subscription or sinking line it pays
	GoalID            string // for a goal purchase
	Unreviewed        bool   // counted under its suggestion; label only
}

// Outflow buckets.
const (
	BucketSpending      = "spending"
	BucketBills         = "bills"
	BucketSubscriptions = "subscriptions"
	BucketSavings       = "savings"
	BucketGoalPurchase  = "goal_purchase"
	BucketNeutral       = "neutral"
)

// IncomeItem is counted income received in the month.
type IncomeItem struct {
	ID          string
	Amount      money.Cents
	Type        string
	Provisional bool
}

// Cause explains why the plan or fixed costs changed, attached to absorption.
type Cause struct {
	Kind   string // standing_lines, plan_dropped_expected, plan_dropped_unexpected, fixed_rose
	Label  string
	Amount money.Cents
	Ref    string
}

// AbsorptionEntry records spend_base being cut because available fell (S2).
type AbsorptionEntry struct {
	Amount money.Cents
	From   money.Cents // spend_base before
	To     money.Cents // spend_base after
	Causes []Cause
}

// Flag marks something the UI should show about a month.
type Flag struct {
	Kind   string
	Ref    string
	Amount money.Cents
}

// Flag kinds.
const (
	FlagOvercommitted    = "overcommitted"
	FlagAutoCarried      = "auto_carried"
	FlagLearning         = "learning"
	FlagPartial          = "partial"
	FlagPinned           = "spend_base_pinned"
	FlagTakeBack         = "take_back"
	FlagOverAllocated    = "pot_over_allocated"
	FlagRebuildFromFixed = "rebuild_reduces_spending"
)

// MonthInputs is everything ComputeMonth needs for one month. The impure
// layers select and classify; this package only does arithmetic and ordering.
type MonthInputs struct {
	Month            civil.YearMonth
	Today            *civil.Date // set only when computing the current month
	PrevIncome       money.Cents // I(M-1)
	PlanOverride     *money.Cents
	Learning         bool
	Partial          bool
	Lines            FixedLines
	TrailingOutflow  [3]money.Cents // actual bills+spending of the last 3 months (EF target)
	AbsorptionCauses []Cause
	Events           []Event
	Outflows         []Outflow
	Income           []IncomeItem
}

// MonthState is the derived result for one month.
type MonthState struct {
	Month    civil.YearMonth
	Learning bool
	Partial  bool

	Plan      money.Cents
	Fixed     money.Cents
	Available money.Cents
	SpendBase money.Cents
	// PinnedSpendBase is the last manual edit still in force (A-001); 0 if none.
	PinnedSpendBase money.Cents
	// CarryToNext is the next-month borrow chosen by covers this month.
	CarryToNext money.Cents

	Pot            money.Cents
	PotAllocated   money.Cents
	PotUnallocated money.Cents
	Overcommitted  money.Cents

	SpendPool    money.Cents
	Deductions   money.Cents
	SpendLeft    money.Cents
	PerDay       money.Cents
	LastDayExtra money.Cents

	OverspendGross money.Cents
	CoversApplied  money.Cents
	OverspendOpen  money.Cents

	Income        money.Cents
	BillsUnderage money.Cents // unspent monthly bill and subscription reserve, swept to savings next month

	Absorption []AbsorptionEntry
	Envelopes  map[EnvelopeID]money.Cents
	Flags      []Flag
	Entries    []Entry

	EngineVersion int
	InputHash     string // hex sha256
}

// Has reports whether a flag of this kind is set.
func (s MonthState) Has(kind string) bool {
	for _, f := range s.Flags {
		if f.Kind == kind {
			return true
		}
	}
	return false
}
