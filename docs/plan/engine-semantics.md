# budget engine semantics (precise reading of S2 to S10)

`docs/spec.md` is the source of truth. This document pins the **order of operations** and the few places the spec leaves room for two readings, so every session implements `budget.ComputeMonth` the same way.

Items marked **[A-0xx]** are interpretations filed in `docs/spec-amendments.md`. Implement them as written here (they are the conservative readings) until the owner rules. If the owner changes one, change this document, the amendment and the tests together.

Every rule below needs a test tagged with its spec section. The worked examples A to F in S2 must pass **to the cent, month by month** (`internal/budget/scenarios_test.go`). The algorithm below was checked by hand against all six.

---

## 1. types (packages `internal/money`, `internal/civil`, `internal/budget`)

```go
package money
type Cents int64                       // never float
func PerDay(left Cents, daysLeft int) (perDay, lastDayExtra Cents) // floor; remainder on the last day
func Pct(base Cents, basisPoints int64) Cents                      // floor(base * bp / 10000)

package civil
type Date struct{ Y int; M time.Month; D int }   // a calendar date, never an instant
type YearMonth struct{ Y int; M time.Month }
func (ym YearMonth) Days() int
func DaysLeft(today Date) int                     // includes today
func MonthOf(t TxnTime, homeTZ *time.Location) YearMonth // S3: date-only stays a date, real timestamps converted

package budget  // PURE: no I/O, no time.Now, no maps iterated without sorting
type EnvelopeID string // "plan" "bills_reserve" "subs_reserve" "sinking:<billID>" "savings"
                       // "goal:<id>" "ef" "discretionary" "spend_pool" "pot" "next_month"
                       // "uncovered" "bills_gap" "income" "world"
type Entry struct {
    From, To EnvelopeID
    Amount   money.Cents // > 0
    Kind     string      // "fund_fixed" "fund_spending" "pot" "allocate" "spend" "cover"
                         // "absorb" "sweep_leftover" "sweep_pot" "sweep_underage" "carry" ...
    Ref      string      // event id or transaction id
}
```

`MonthInputs` carries everything already selected and classified by the impure layers (recompute, ingest, recurrence, categorize, reimburse). `ComputeMonth` only does arithmetic and ordering.

```go
type MonthInputs struct {
    Month          civil.YearMonth
    Today          *civil.Date      // set only when computing the current month
    PrevIncome     money.Cents      // I(M-1): counted income of the previous month (section 2)
    PlanOverride   *money.Cents     // S4 plan override (effective plan)
    Learning       bool             // S18: no complete prior month and no override
    Partial        bool             // S13: a budget account lacks full coverage last month (label only)
    Lines          FixedLines       // section 3
    TrailingOutflow [3]money.Cents  // last 3 months of actual bills+spending (EF target, S8)
    AbsorptionCauses []Cause        // why available fell vs last month (computed outside, attached to absorption)
    Events         []Event          // this month's budget events, sorted by (At, ID)
    Outflows       []Outflow        // this month's outflows, net of links, sorted by (Date, Seq)
    Income         []IncomeItem     // counted income received this month (for I(M), recaps)
}
type MonthState struct {
    Month                                   civil.YearMonth
    Learning, Partial                       bool
    Plan, Fixed, Available, SpendBase       money.Cents
    PinnedSpendBase                         money.Cents // last manual edit still in force [A-001], 0 if none
    CarryToNext                             money.Cents // next-month borrows chosen this month (the auto-carry is derived from OverspendOpen)
    Pot, PotAllocated, PotUnallocated       money.Cents
    Overcommitted                           money.Cents
    SpendPool, Deductions, SpendLeft        money.Cents
    PerDay, LastDayExtra                    money.Cents
    OverspendGross, CoversApplied           money.Cents
    OverspendOpen                           money.Cents // needs a decision (prompts)
    Income                                  money.Cents // I(M)
    Absorption                              []AbsorptionEntry
    Envelopes                               map[EnvelopeID]money.Cents // persistent + monthly balances
    Flags                                   []Flag      // overcommitted, auto_carried, borrowed_goal, unverified_savings ...
    Entries                                 []Entry
    EngineVersion                           int
    InputHash                               [32]byte
}
func ComputeMonth(prev *MonthState, in MonthInputs) MonthState
```

`prev == nil` means there is no earlier computed month.

---

## 2. income: what counts in I(M) (selected outside the engine)

The recompute layer builds `Income` for month M from transactions whose **month** (S3) is M:

- **Counts:** posted, not hidden, not pending, type income (salary, self-employment, interest, one-time income, gift/other, rewards/cashback, asset sale), including **deposit** type into a tracked account (S4), including manual income entries.
- **Provisional:** an unreviewed deposit that matches a known income schedule counts (S4).
- **Held out:** an unreviewed deposit that matches no schedule; a held payment (`awaiting expense`); a reimbursement, refund, transfer or loan proceeds (S4, S12, S12a).
- **Reversals** and clawbacks net against the original deposit's month (S4).
- **Boundary attribution** (S3): a scheduled item within `min(5 days, interval/4)` of its nominal date across a month boundary belongs to its nominal month (flagged, undoable). Not for short intervals: treat "short" as an interval under 28 days, so the rule applies to monthly and longer schedules only. **[A-010]**
- A user month override (S3) wins over everything.

`I(M) = sum(Income)`. `PrevIncome` for month M is `I(M-1)`.

## 3. fixed(M)

```
Lines.Bills          []Line   // monthly bills at their reserve amount (last actual, else most recent known, else expected; S5)
Lines.Subscriptions  []Line   // same rule (S6)
Lines.Sinking        []Line   // non-monthly bills: amount/months-in-period, or amount*30.4375/interval_days, with first-entry catch-up (S5)
Lines.PaymentPlans   []Line   // remaining installments due this month (S19.9)
Lines.Savings        SavingsRule // fixed cents, or basis points of the effective plan (S7)
Lines.Goals          []Line   // active (not paused, not complete) goal monthly amounts (S7)
Lines.EF             Line     // EF standing line (S8)
Lines.Rebuild        []Line   // EF rebuild lines (S8)
Lines.Standing       []Line   // standing lines from permanent allocations made in EARLIER months (S2)
Lines.TaxSetAside    *Line    // S19.22, a % of last month's self-employment income (computed outside)
```

- `savings = rule.Fixed` or `money.Pct(plan, rule.BasisPoints)`.
- **Rebuild redirect** (S8): each rebuild amount `R` is taken from general savings first. `fromSavings = min(R, savings)`, `savings -= fromSavings`, `extra = R - fromSavings`, and only `extra` adds to fixed. Funding entries: `plan -> ef` for `R`. **[A-007]**
- `fixed = sum(bills) + sum(subs) + sum(sinking) + sum(payment plans) + savings + sum(goals) + ef + sum(extra from rebuild) + sum(standing) + tax`

Standing lines created by a permanent allocation **in month M itself** are not in `Lines.Standing`. They are applied in step 4c, because they come out of month M's pot.

## 4. ComputeMonth, in order

**4a. plan**
```
if in.PlanOverride != nil   -> plan = *in.PlanOverride
else if in.Learning         -> plan = 0, state.Learning = true
else                        -> plan = in.PrevIncome
```

**4b. data-driven spend_base and absorption** (S2)
```
fixed_data   = fixed from section 3 (excludes this month's permanent allocations)
available    = plan - fixed_data
if prev == nil || prev.Learning || prev has no plan:
    base = max(0, available)                     // first plan: spend_base = plan - fixed
else:
    base = prev.SpendBase
    floor = prev.PinnedSpendBase (0 if none)      // [A-001]
    if available < base:
        newBase  = max(0, available, floor)
        absorbed = base - newBase
        base     = newBase
        record AbsorptionEntry{Amount: absorbed, Cause: in.AbsorptionCauses}  // the absorption ledger (S2); no envelope Entry, base is simply lower
```
- Absorption is checked against the **data-driven** available, before this month's own user events. **[A-005]**
- **Cause labeling** (S2 example C): compare with the previous month. If `fixed` rose because of standing lines from a dated raise, label "caused by standing lines added from the raise on [date]" with a one-tap remove. If the plan fell, label "plan dropped" plus "expected (schedule)" or "unexpected (missing deposit)" (S4). Causes are data, computed outside and passed in as `in.AbsorptionCauses`; the engine attaches them.
- **[A-001]** A manual spend_base edit (step 4c) sets a **pin**. Absorption never cuts below the most recent pin. While the pin exceeds what available can fund, the month is **overcommitted** (shown, allowed). The pin clears when the user edits again or removes it. This is the conservative reading of "manual edits are never auto-reversed".

**4c. this month's user events, in (At, ID) order**
```
SpendBaseEdit{Value}              -> base = max(0, Value); pin = Value   // state.PinnedSpendBase = pin; carries forward until the next edit or removal
SpendBasePinRemoved               -> pin = 0
Allocation{Dest, Amount, Permanent: true}:
    Dest == spending              -> base += Amount
    Dest == savings|goal|ef|disc  -> fixed += Amount; add standing line effective from this month
Allocation{Permanent: false}      -> recorded for step 4e (does not change fixed or base)
```
Allow everything: an allocation larger than the pot is applied and produces overcommitted, never an error.

**4d. pot and overcommitment**
```
available      = plan - fixed                 // fixed now includes 4c permanent lines
pot            = max(0, available - base)
overcommitted  = max(0, fixed + base - plan)
invariant      : plan + overcommitted == fixed + base + pot      (A9.3 #1)
               : pot > 0 and overcommitted > 0 never both
```

**4e. one-time allocations of the pot**
```
for each one-time Allocation (event order): potAllocated += Amount; Entry pot -> Dest
potUnallocated = pot - potAllocated   // may be negative if the user over-allocates: shown, allowed
```
If late data shrinks the pot below what was allocated (S2), the difference is **taken back** in this order: one-time allocations to discretionary (latest first), then to savings and goals (latest first). This is derived every recompute; no events are written. Emit entries labeled `take_back`.

**4f. spend pool and deductions**
```
pool       = base + sum(one-time allocations to spending)
deductions = prev.CarryToNext + max(0, prev.OverspendOpen)
             // next-month borrows chosen in M-1, plus the auto-carry of M-1's unresolved overspend (section 5.4, flagged auto_carried)
poolAvail  = pool - deductions
disc       = prev.Envelopes[discretionary] + this month's discretionary credits (standing line, allocations, moves, manual adds)
```

**4g. outflows, in (Date, Seq) order** (S9, S10, S5, S6, S7)

Every outflow is already net of reimbursement and refund links (tentative links net immediately, S12a), already split into parts (each part in its own bucket, S19.6), and excludes pending and hidden rows.
```
draw(a):                                  // the monthly pool first, then discretionary, then overspend
    fromPool = min(a, max(0, poolAvail)); poolAvail -= fromPool; a -= fromPool
    fromDisc = min(a, max(0, disc));      disc -= fromDisc;      a -= fromDisc
    if a > 0: poolAvail -= a; overspendGross += a; remember (outflow, a) for prompts

for o in Outflows:
    switch o.Bucket:
    spending:
        if o.FromDiscretionary: disc -= o.Amount           // may go negative (S9)
        else draw(o.Amount)
    bills, subscriptions:                                   // [A-004] subscriptions behave like bills
        rem = reserveRemaining[o.LineID]  (+ sinking balance for a sinking line, minus bills_gap for that line)
        take = min(o.Amount, rem); reserveRemaining -= take
        over = o.Amount - take
        if over > 0: by default draw(over); an OverageSource event can send it elsewhere (S5)
    savings:                                                // savings transfer: verifies the savings line, no draw
        mark savings verified for this month (S7)
    goal_purchase:                                          // S7 purchase linked to a goal
        fromGoal = min(o.Amount, goalBalance); goalBalance -= fromGoal
        if o.Amount > fromGoal: draw(o.Amount - fromGoal)
    neutral:                                                // transfers, card payments, loan proceeds: nothing
```
- Unreviewed outflows count under their **suggested** bucket (spending if none) (S11). They are not marked provisional in the math, only in the UI badge.
- Bills and subscriptions with no matching line (a new charge in a bill category) draw from the pool like spending, and are surfaced to recurrence detection.

**4h. covers** (S10)
```
covers = this month's Cover events, each {Source, Amount}, Source in:
   next_month | savings | goal:<id> | ef | bills_reserve | uncovered
apply in event order, then clamp: if sum(covers) > overspendGross (a refund shrank the overspend),
   reduce covers in REVERSE event order until sum == overspendGross     [A-006]
for each effective cover:
   next_month    -> poolAvail += c; carryToNext += c                     (entry next_month -> spend_pool)
   savings       -> poolAvail += c; savings -= c
   goal:<id>     -> poolAvail += c; goal -= c; flag borrowed_goal(id, c)
   ef            -> poolAvail += c; ef -= c; caller creates the rebuild line (S8)
   bills_reserve -> poolAvail += c; bills_gap += c (shown with the next due bill)
   uncovered     -> uncoveredDebt += c (explicit "leave uncovered"; poolAvail unchanged)   [A-003]
spendLeft      = poolAvail
overspendOpen  = overspendGross - sum(effective covers)            // >0 means a decision is still needed
```
- Discretionary is drawn automatically in step 4g, so it is **not** a cover source. The S10 "suggested order" starts with discretionary only for users who move money into it by hand. **[A-008]**
- Prompts (the overspend card) use posted outflows only and only for the current and previous month (S3). That filtering happens outside the engine.

**4i. per day** (S10, S17)
```
if in.Today != nil:
    daysLeft = civil.DaysLeft(*in.Today)
    perDay, lastDayExtra = money.PerDay(max(0, spendLeft), daysLeft)
    // invariant A9.3 #10: perDay*daysLeft + lastDayExtra == max(0, spendLeft)
    // on the last day perDay == max(0, spendLeft)
```

## 5. rollover: what month M+1 inherits from M (derived inside `ComputeMonth(M+1)` from `prev`)

These are computed, not stored as events, so late data recomputes them (S17). The rollover **job** only triggers the recompute of the new month and sends the digest.

1. **Leftover pool** (S2, S7): `max(0, prev.SpendLeft)` sweeps to general savings. **[A-002]** Discretionary does not sweep.
2. **Unallocated pot** (S2): `max(0, prev.PotUnallocated)` sweeps to general savings.
3. **Monthly bill and subscription underage** (S5, S6): unspent monthly reserve sweeps to general savings. Sinking balances persist.
4. **Unresolved overspend** (S10.4): `prev.OverspendOpen` carries into `M+1` as an auto-carry deduction, flagged `auto_carried`. A later Cover event for month M (the user resolves an old month) replaces the carry, and the deduction disappears on recompute.
5. **Next-month borrows** from covers in M become deductions in M+1 (`prev.CarryToNext`).
6. **Explicit uncovered** amounts accumulate in the `uncovered` debt envelope until a Settle event moves money into it from a source (S10.5, S0.3). They do not reduce later pools. **[A-003]**
7. **Planned credits at the start of M+1** (S7, S8, S9): general savings, goal lines, the EF line, rebuild lines, discretionary standing lines and sinking set-asides are credited to their envelopes. Savings that are not verified by month end are flagged `unverified_savings` (S7).
8. **Goal and EF caps** (S7, S8): a goal over its target sends the excess to general savings. The EF above its target sends the excess to general savings. The EF target is `N * mean(in.TrailingOutflow)` with N=3 by default, or a fixed override. A target change is not a withdrawal.

## 6. envelopes and entries

- Persistent envelopes: `savings`, `goal:*`, `ef`, `discretionary`, `sinking:*`, `uncovered`, `bills_gap`.
- Monthly envelopes (start empty every month): `plan`, `bills_reserve`, `subs_reserve`, `spend_pool`, `pot`.
- Every movement is one `Entry` with equal debit and credit (A9.3 #3). Funding a month: `income -> plan` for `plan`; `plan -> bills_reserve`, `plan -> subs_reserve`, `plan -> sinking:*`, `plan -> savings`, `plan -> goal:*`, `plan -> ef`, `plan -> spend_pool` (base), `plan -> pot`. Spending: `spend_pool -> world`, `discretionary -> world`. Covers, sweeps, carries and take-backs each have their own `Kind`.
- When overcommitted, the funding entries from `plan` total `plan + overcommitted`. Represent the shortfall as `overcommitted -> plan` with `Kind: "overcommitted"` so conservation still holds.
- The "why is my number $X" screen (S17) renders `Entries` grouped by `Kind`.

## 7. determinism

- `ComputeMonth` sorts `Events` by `(At, ID)` and `Outflows` by `(Date, Seq)` defensively, even if the caller sorted them.
- No maps are iterated without sorting their keys. No `time.Now`. No floats (basis points for percentages).
- `InputHash = sha256(canonical JSON of prev hash + inputs + EngineVersion)`. Same inputs give the same state (A9.3 #13).

## 7a. worked examples, traced (fixed $2000, spend_base $2000 unless noted)

| ex | month | plan | fixed | base | pot | pool | notes |
|---|---|---|---|---|---|---|---|
| A | Feb | 4000 | 2000 | 2000 | 0 | 2000 | I(Jan)=4000 |
| A | Mar | 6000 | 2000 | 2000 | 2000 | 2000 | one-time 2000 to the phone goal: potUnallocated 0 |
| A | Apr | 4000 | 2000 | 2000 | 0 | 2000 | spend_base never moved |
| B | Mar | 6000 | 2000 | 2000 | 2000 | 2500 | one-time 1500 savings + 500 spending |
| C | Mar | 6000 | 3500 | 2500 | 0 | 2500 | permanent: standing savings 1500, base +500 |
| C | later | 4000 | 3500 | 500 | 0 | 500 | available 500 < 2500: absorbed 2000, cause "standing lines from the raise" |
| D | drop | 2000 | 2000 | 0 | 0 | 0 | absorbed 2000 |
| D | recovery | 4000 | 2000 | 0 | 2000 | 0 | pot pre-filled "restore $2000 to spending"; untouched, sweeps to savings |
| E | any | | | | | | spendLeft 50, buy 230, disc 0: overspendGross 180; covers 100 next_month + 80 goal: spendLeft 0, next month deductions 100, goal -80 flagged borrowed |
| F | Sep/Oct | | | | | | the $50 October link nets the $100 September dinner: September spending 50; any September cover shrinks in reverse order |

## 8. what is NOT budget math (and so lives outside `internal/budget`)

Selecting transactions for a month, classifying income, choosing reserve amounts (last actual), schedules, link scoring, rule evaluation, causes and labels, prompt eligibility, and notification copy. Those packages pass plain values into `MonthInputs`.
