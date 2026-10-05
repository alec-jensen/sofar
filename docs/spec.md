# sofar: Budget Logic Spec (full scope)

Status: consolidated from the design Q&A. Every rule here was either decided by Alec or derived from a decided rule. sofar is a **single-user, self-hosted personal finance app for one independent person**. It is not a product: no households, no multi-tenancy, no billing, no telemetry. Section 20 is the decisions log. Behavior changes after handoff go through `docs/spec-amendments.md` (see CLAUDE.md).

---

## 0. Principles

1. **You always spend last month's money.** `plan(M) = I(M-1)`. This month's income funds next month. It is never spendable this month unless you borrow it (which is the same as borrowing from next month's spending).
2. **Nothing becomes spendable on its own.** Money that frees up (income increase, cancelled bill, completed goal) lands in the **pot**, held out of spending until the user allocates it. Spending can only *fall* automatically (absorption), never rise.
3. **Allow everything, record everything, block nothing.** Warnings are inline and show consequences. Nothing is a blocking modal. Only deleting data asks for confirmation. Balances can go negative and are shown as debts with a "settle" action. The system's job is to keep tracking correct, not to restrain the user.
4. **Cash principle.** Income counts in the month the money hits.
5. **Every transaction is reviewed by the user.** Suggestions are pre-selected. Nothing auto-confirms.
6. **Everything is derived.** Stored data is the transaction ledger plus decision events. Budget numbers are computed. Any decision is undoable. Late or edited data recomputes history.
7. **The server never depends on the app being open.** Sync, rollover and defaults run on their own.
8. **Single user, self-hosted.** One person runs it for their own finances. There are no workspaces, households or shared accounts (section 19.16).
9. **No scope ceiling.** Everything in this spec is in scope. There is no v1 cutoff.
10. **Gamified, but a skin over truth.** sofar is a game layer on top of real finances (section 19.25). It rewards good data habits and the user's own goals, never alters budget math, never gates a feature, and never shames.

---

## 0a. Vocabulary and UI copy

Internal names are used in this spec. The UI uses plain, lowercase, casual copy (no exclamation points, no hype).

| Internal | UI copy |
|---|---|
| `spend_left` (hero) | safe to spend (total and per day) |
| `plan(M)` | this month's budget |
| `pot` | uncategorized |
| `spend_base` | spending |
| discretionary | discretionary |
| receivable | owed to you |
| held payment (`awaiting expense`) | waiting for the charge |
| absorption | spending adjusted |
| cover | covered from |
| pending | pending |
| rollover | new month |
| assigned vs held | assigned vs in your accounts |
| gamification XP | growth |
| streak | tended weeks |
| gamification world | the grove |
| engine accuracy view | fluency |

The product name is always written `sofar` in lowercase. The income type is **one-time income**, never "extra", to avoid colliding with the pot (`extra` appears only in "extra paycheck" labels).

---

## 1. Entities

| Entity | Notes |
|---|---|
| Account | `budget` (checking, credit cards), `tracked` (savings), or `balance-only` (investments) |
| Transaction | From SimpleFIN or manual. Bank fields are immutable. User changes are an overlay. |
| Transaction type | spending, bill, subscription, income (with income type), transfer (neutral), savings transfer, reimbursement, refund, deposit, loan proceeds (neutral), fees, cash (ATM) |
| Income type | salary, self-employment (e.g. photography), interest, one-time income, gift/other, rewards/cashback, asset sale |
| Non-income inflows | reimbursement, refund, transfer, loan proceeds |
| Top-level buckets | income, bills, savings, spending (fixed set) |
| Category group | Middle layer between bucket and category (section 1a). Display and reporting only. |
| Custom category | Must be placed in a group. Optional soft targets (section 19.23) that never affect plan math. |
| Schedule | Recurrence definition (section 14). Used by income streams, bills, subscriptions. |
| Bill | Recurring necessary expense |
| Subscription | Recurring unnecessary expense. Own category. |
| Sinking envelope | Persistent reserve for non-monthly bills |
| Goal | Savings goal (virtual envelope) |
| Emergency fund (EF) | Virtual envelope with target |
| Discretionary | Running balance of fun-spending money |
| Standing line | Recurring allocation created by a permanent decision |
| Allocation | Assignment of pot money to a destination |
| Cover | Assignment of an overspend to a source |
| Link | Reimbursement/refund tied to an original expense (tentative or confirmed) |
| Receivable | Money someone owes the user (section 12a) |
| Counterparty | A person or payer with a learned profile (reimbursement lag, amounts, categories) |
| Event | Immutable record of a user or system decision |

All savings buckets (general savings, goals, EF, discretionary) are **virtual envelopes**. Sofar tracks transactions, not real balances in savings accounts.

---

## 1a. Categories

### Structure

Three levels: **bucket -> group -> category**.

- Buckets are fixed: income, bills, savings, spending. A separate system **Neutral** set holds non-budget categories.
- Every category belongs to exactly one group. Every group belongs to exactly one bucket. A transaction's bucket derives from its category, so moving a transaction to a category in another bucket changes how it counts (for example bills reserve vs spending pool).
- Groups are for display, collapsing and report rollups. Optional **soft targets** exist at the category, group and tag level (section 19.23). They never change the plan math.
- Discretionary is not a category. Purchases drawn from it keep their normal spending category.

### Defaults

**Income**
- Earned: Salary, Self-employment
- Passive: Interest, Rewards / cashback
- Other income: One-time income, Gift / other, Asset sale

**Bills**
- Housing & utilities: Rent / mortgage, Utilities, Internet & phone
- Insurance & debt: Insurance, Loan payments
- Other: Other bills

**Subscriptions** (sub-bucket of spending, reserved up front)
- Subscriptions: Streaming & media, Software & cloud, Memberships, Other subscriptions

**Savings**
- Core savings: General savings, Emergency fund, Investments
- Goals: Debt payoff, plus one category per user-created goal

**Spending**
- Food & dining: Groceries, Dining out
- Transportation: Fuel, Auto & transit (maintenance, parking, tolls, rideshare)
- Shopping & home: Shopping, Home & household
- Lifestyle: Entertainment, Travel, Personal care, Gifts & donations
- Health & education: Health & medical, Education
- Business & financial: Business expenses, Fees & interest, Cash & ATM
- Other: Other

**Neutral** (system categories, excluded from the budget)
- Transfers: Transfer, Credit card payment, Loan proceeds
- Links: Reimbursement, Refund (assigned by links, not picked by hand)
- Excluded: Excluded (hidden transactions)

### Rules

- **Stable internal IDs.** Default categories and groups keep a permanent ID. Renaming never breaks rules.
- **Custom categories and groups.** A custom category must be placed in a group. A custom group must be placed in a bucket. Both can be created from the review queue. Groups cannot span buckets. Custom ones can have soft targets (section 19.23).
- **Deleting a category** requires reassigning its transactions, defaulting to "Other" in the same bucket. Deleting a group requires moving or deleting its categories.
- **Merging** two categories moves transactions and rules to the survivor.
- **Investments** is a savings category. A contribution to an investment account is recorded as a **savings transfer** and counts toward savings (section 12).
- **Unmatched transactions** land in the Other category of the bucket suggested by amount sign (spending for outflows, income "Gift / other" for inflows) and are flagged for review.
- **Suggestions.** Handled by the categorization and rules engine (section 18a): seed rules, user rules, learned rules, case memory, and a per-user model. The optional AI suggester is part of the AI assistant (section 19.17).

---

## 2. Core model and math

```
I(M)           counted income received in month M
               (excludes transfers, reimbursements, refunds, loan proceeds)
plan(M)        effective plan = override if set, else I(M-1)
fixed(M)       bills reserve (monthly bills at last month's actual amount
               + sinking set-asides)
               + subscriptions reserve
               + general savings
               + goal contributions
               + EF contribution
               + standing lines
spend_base(M)  stored spending level, event-sourced (see below)
available(M)   plan(M) - fixed(M)
pot(M)         max(0, available(M) - spend_base(M))   // uncategorized, held out
spend_pool(M)  spend_base(M) + one-time allocations to spending
spend_left     spend_pool + covers into spending
               - posted spending drawn from the monthly pool
               - deductions carried in from last month's borrow
per_day        max(0, spend_left) / days left in month incl. today
discretionary  running balance, not monthly. Draws only after spend_left hits 0.
```

### spend_base rules

`spend_base(M)` is a pure function of history, recomputed from events whenever data changes:

- **First plan:** `spend_base = plan - fixed`.
- **Absorption:** if `available(M) < spend_base(M-1)`, then `spend_base(M) = available(M)` (floored at 0). The reduction is written to the **absorption ledger**. The pot is drained first (it is already unassigned), then spend_base absorbs.
- **Never rises automatically.** It only rises by a user event: a permanent allocation to spending, or a manual edit.
- **Overcommitted:** if `fixed(M) > plan(M)`, spend_base is 0 and the app shows "fixed costs exceed plan by $X". Nothing is blocked.
- **Manual edits** to spend_base or standing lines are never auto-reversed. If they push commitments over the plan, the app shows "overcommitted by $X" and allows it.

### Pot behavior

- The pot is recomputed fresh each month, not cumulative.
- An unallocated pot defaults to **general savings at the end of the month it appears in**.
- Allocation destinations: general savings, a specific goal, EF, discretionary, spending. Splits are allowed.
- Each allocation is **one-time** or **permanent**.
  - One-time: applies this month only.
  - Permanent: creates a **standing line** at the allocated amount. A permanent allocation to spending raises spend_base.
- If the pot repeats at the same size two months in a row, the app suggests "make permanent?" The previous month's split is pre-filled as a suggestion only.
- A past one-time allocation can later be converted to a standing line.
- Unspent one-time spending money sweeps to savings at month end (leftover rule). To keep it, send it to discretionary instead.
- If late data shrinks the pot below what has been allocated, the difference is taken back from discretionary first, then savings.

### Extra decomposition (suggestions only)

When recurring schedules exist, the pot is broken down by stream, for example "salary: +1 occurrence (+$2000)". Default suggestions:

- Extra occurrence of a recurring stream: one-time.
- Per-deposit amount increase: permanent.
- Lump (photography, gift, asset sale): one-time.

The user is always prompted. Suggestions never apply themselves.

### Worked examples

Setup for A to D: fixed $2000, spend_base $2000.

**A. Three-paycheck month.** Jan $4000, Feb $6000, Mar $4000.
- Feb: plan $4000, pot $0.
- Mar: plan $6000, pot $2000, labeled "extra occurrence of salary", suggested one-time. The user sends $2000 to the phone goal, one-time. Spending pool stays $2000.
- Apr: plan $4000, pot $0. spend_base never moved, so there is no drop.
- Mid-Feb the app shows "next month's plan so far: $X, expected $Y by schedule". This is a forecast only.

**B. Temporary spike, split.** Pot $2000: $1500 to savings and $500 to spending, both one-time. March pool is $2500. The next month the plan returns to $4000 and spending is unaffected.

**C. Permanent raise, later loss.**
- Plan stays $6000, so the pot is $2000 again, since spend_base did not move. The app nudges "make permanent?".
- The user makes it permanent: $1500 becomes a standing savings line and spend_base rises $500 to $2500. fixed = $3500, available = $2500, pot = $0.
- Later the plan falls to $4000: available = $500, which is below spend_base $2500, so $2000 is absorbed and spend_base becomes $500. The shortfall is labeled "caused by standing lines added from the raise on [date]" with a one-tap remove.

**D. Drop and recovery.** Plan falls to $2000: available = $0, so spend_base absorbs $2000 and becomes $0 (absorption ledger entry). Plan recovers to $4000: pot = $2000, pre-filled as "restore $2000 to spending", tracked by the absorption ledger. Untouched, it goes to savings at month end.

**E. Overspend.** spend_left is $50, the user buys $230, discretionary is $0. Overspend $180. The user covers $100 from next month's spending and $80 from the phone goal. Next month's pool is reduced by $100. The goal envelope is reduced by $80 and flagged "borrowed $80". The transaction stays in spending.

**F. Reimbursement across months.** A $100 September dinner is spending. A $50 October Venmo is linked. It nets against September, so September spending is $50. If September had an overspend cover, the cover shrinks and the source is refunded (reverse order).

---

## 3. Time

- **Calendar months.** The home time zone decides month membership and all calculations.
- **Display** is in the local zone when traveling. When the display zone differs from the calculation zone, a message explains why.
- **Home zone changes** are effective-dated. Past months keep their original calculation zone.
- **Bank dates are calendar dates.** If the bank reports a date-only value (midnight UTC), treat it as a calendar date and do not convert zones. Convert only real timestamps. This prevents a transaction on the 1st landing in the previous month.
- **Transaction month** = the bank's transaction date if provided, else the posted date.
- **Months never close.** A late transaction recomputes its month and everything downstream. A quiet "history changed" note says what moved and why.
- **Prompts** (overspend, pot) are generated only for the current and previous month. Older months get a passive marker and a manual "resolve" button, so a sync cannot ambush the user about an old month.
- **Boundary attribution.** A recurring item (income or bill) with an expected date is attributed to its nominal month if the actual transaction falls within the tolerance window across a month boundary. Tolerance = `min(5 days, interval/4)`. It does not apply to short intervals. The attribution is flagged and undoable.
- **User overrides.** The user can reassign any transaction to any month.

---

## 4. Income

### Types and counting

- Counted income types are listed in section 1. Reimbursements, refunds, transfers and loan proceeds are never income.
- Interest is income (type interest), so it counts in I(M).
- Rewards/cashback is income (other). Asset sale is income (other), with one-time suggested. Loan proceeds are neutral.
- Manual income entries are allowed (cash tips, off-account gigs).
- Business expenses are spending, in the default category "Business expenses". An optional tax set-aside and deductible tags are in section 19.22.
- Income is recognized when the money hits. A deposit paid in advance counts when it arrives.
- **Deposit type.** A deposit is an external inflow into a tracked (non-budget) account, such as a split direct deposit straight to savings. It counts as income in I(M) for the chosen income type, and it is also recorded as the matching savings movement (auto-verifies the matching savings line, section 7).

### Unreviewed deposits and the plan

- A deposit matching a known income schedule counts in the plan provisionally.
- An unknown deposit (a Venmo, a transfer-in) is held out of the plan until reviewed. A reclassification could otherwise inflate next month's plan.
- Held deposits show as "pending review: $X, not yet in next month's plan".

### Reversals and clawbacks

- A payroll reversal or clawback nets against the original deposit's month, the same rule as refunds. Plan recomputes. If allocations now exceed the pot, take back from discretionary first, then savings.

### Plan override

- The user can override `plan(M)` with a note, for example for partial data, learning, or a known one-off.
- The next month's delta uses the **effective** (overridden) plan.

### Income loss mid-month

- Mid-month, nothing changes. Spending drops only when the next plan is computed (we cannot tell whether income is lost or just not yet received).
- The next month's drop is labeled "expected (schedule)" if the schedule predicted it, or "unexpected (missing deposit)" otherwise.
- Income that arrives late lands in the later month and appears as pot per the normal rules (the user can choose where it goes).

---

## 5. Bills

- **Definition.** A bill is a recurring necessary expense. Sources: auto-detected, manual, or "mark as recurring" on a transaction (section 14).
- **Reserve amount.** Sum of known bills. Each bill uses its **last actual amount**. If there is no last amount (new bill, skipped month), use the most recent known amount, or the user's expected amount.
- **New bill.** Detection (default threshold 3 occurrences, configurable) asks before adding. Adding a bill raises fixed immediately (user-made change) and reduces the pot first, then spend_base.
- **Ended bill.** Asks "still active?" when no occurrence appears for 2 intervals. A cancelled bill frees its reserve into the **pot**.
- **Price change.** An amount outside the tolerance is flagged. The reserve updates to the last actual amount.
- **Missing bill.** Alert when an expected occurrence is missing past the grace window (25% of the interval, capped at 3 days, editable).
- **Overage.** If a bill comes in higher than reserved, the extra comes out of spending unless the user says otherwise.
- **Underage (monthly bills).** The difference sweeps to general savings at month end.
- **Month assignment.** A bill always counts in the month of the transaction unless the user changes it. Partial payments both count. Card bills count on the date charged to the card.
- **Non-monthly bills (sinking envelopes).**
  - Annual and quarterly: monthly set-aside = amount / months in period.
  - Fixed intervals: monthly set-aside = `amount * 30.4375 / interval_days`.
  - First entry catch-up: amount / months until due, then the normal rate.
  - Persistent. No month-end sweep. An overfunded envelope keeps its balance for the next cycle.
  - Underfunded charge: the shortfall is a bill overage (comes out of spending).
- **Bills reserve borrowing.** The user may cover an overspend from the bills reserve. This creates a tracked **bills gap**, shown with the next due bill, and the gap flows as an overage when the bill posts.
- **Shared bills.** A roommate's Venmo is a reimbursement and nets against the bill.
- **Subscriptions** are separate (section 6).

---

## 6. Subscriptions

- Unnecessary recurring items go in a **subscription** category, a sub-bucket of spending.
- Reserved up front like bills, so the hero number already excludes them. Included in `fixed(M)`.
- Cuttable: cancelling or dropping one frees its reserve into the **pot**.
- Same detection, price-change, and missing-occurrence rules as bills.
- Underage sweeps to savings like monthly bills.

---

## 7. Savings

### General savings

- A fixed dollar amount or a percentage of the effective plan, the user's choice.
- It is a **target, not a cap.** Any leftover or unallocated money automatically goes to savings.
- No floor. Savings drops only when the user drops it, or if it is a percentage and the plan falls.
- The monthly amount is credited to the envelope at rollover (planned). It is **verified** when a matching transfer into a linked savings account appears. If it is not matched by month end, it is flagged.
- If the savings account is not linked, the user taps "I moved it".
- Interest is income (type interest).

### Savings account activity

- **Outflow from a linked savings account** triggers a prompt: cover an overspend, goal purchase, EF use, or just moving money.
- **Contributions** that move money into savings are recognized as savings transfers.

### Goals

- **Definition.** Name, target total, and either a monthly amount or a date (never both). The other is derived.
- **Separate from general savings.** Goal contributions are standing lines in addition to general savings.
- **Priority.** User-set order. It is the suggested order for taking from goals when balancing. It never auto-cuts.
- **Date-based goal falls behind** (for example after borrowing). The user decides: raise the monthly amount, or let the finish date slip. Nothing auto-adjusts.
- **Approaching or missing deadline.** Alert when it is approaching and looks like it will miss, and again if it misses.
- **Completed.** The monthly contribution stops. Asks where the freed contribution goes (default: pot, then savings).
- **Overfunded.** Excess goes to general savings.
- **Purchase.** A purchase linked to a goal closes the goal. It draws from the goal envelope and is excluded from spending math up to the goal balance. The excess, if any, counts as spending. Remaining goal balance asks where it goes.
- **Abandoned.** Asks where the accumulated balance goes.
- **Target changed.** Derived values update.
- **Paused.** Any goal can be paused without deleting.
- **Borrowed from.** Taking from a goal to cover an overspend is allowed and always shown on the goal. The user then chooses repay (standing line) or slip the date or reduce the target.
- Goals are virtual envelopes.

---

## 8. Emergency fund

- A separate virtual envelope.
- **Target.** Configurable. Default: 3 months of spending, defined as `N * trailing 3-month average of actual (bills + spending) outflow`. A fixed dollar override is allowed. A target change is not treated as a withdrawal. The UI shows % funded.
- **Funding.** Its own monthly standing line, plus one-off allocations from the pot, plus an optional opening balance.
- **Priority.** Same suggestion list as goals, default rank 1 (suggestion only).
- **Use.** Only by deliberate choice, typically as an overspend cover. A withdrawal that covers an overspend leaves the transaction in spending (stats stay honest). A cover entry moves virtual money from the EF to spending. Impact is previewed before confirm. There is no gatekeeping.
- **Rebuild.** After a withdrawal, a rebuild line is created: `withdrawal / N months`. The user picks N at withdrawal time (suggested default 6, see section 20). It redirects **general savings** into the EF first. Only if general savings is $0 does it reduce spending. The user can edit or delete it. Rebuilding is optional.
- **Excess above target** goes to general savings.
- A withdrawal from a real linked savings account triggers the section 7 prompt.

---

## 9. Discretionary

- Fun-spending money. It is a **running balance, not monthly**. It does not sweep.
- Funded by allocations (one-time or standing), moves from savings (virtual), and manual adds.
- **Draw order.** Purchases draw from the monthly spending pool first. When the pool hits 0, purchases draw from discretionary. Overspend triggers only when both are exhausted.
- **Per-transaction override.** Any transaction can be tagged "from discretionary". It still counts as spending in stats but does not use the monthly pool. A refund returns the money to discretionary.
- No cap. It can go negative.
- The UI shows it on a separate line below the hero number.

---

## 10. Spending and overspend

### Hero number

- Monthly spending only: `spend_left`, with the per-day figure. Discretionary is shown separately, for example "$0 left + $120 discretionary".
- When overspent, the total shows negative and per-day shows 0. Per-day weighting is flat. On the last day, per-day is everything left.
- **Cash check.** If free cash across budget accounts, minus upcoming known bills, is less than `spend_left`, an amber line shows the real figure. The hero stays plan-based.
- **Stale data.** Data older than 48 hours puts a "data as of" label on the hero number.

### Overspend flow

1. A prompt is created when **posted** spending crosses `spend_pool + discretionary balance`. Delivered by push and in-app (after a sync). Several overspends in a day collapse into one card, each resolvable separately.
2. The user covers the overspend from one or more sources, in any amount and any split:
   - next month's spending (borrow), no cap
   - general savings
   - any goal
   - EF
   - bills reserve (creates a bills gap)
   - leave uncovered
3. Suggested order (suggestion only): discretionary, next month's spending, general savings, goals (lowest priority first), EF.
4. **Unresolved at rollover** auto-carries into next month's pool, flagged "auto-carried". The user can reassign later, which refunds the carry.
5. **Uncovered** (chosen or beyond all sources) carries as a visible negative balance into following months until settled. No multi-month cap, no block.
6. A refund or reimbursement that shrinks an overspend shrinks the cover, and money returns to its source in reverse order.
7. Covering from the EF triggers a rebuild line (section 8). Covering from a goal marks it borrowed (section 7).

### Related rules

- **Planned purchases** are savings goals. There is no separate planned-expense concept.
- **"Should I buy this" tool.** Uses `spend_left` plus discretionary. If the purchase would overspend, it names which source would be hit. It informs only.
- **ATM cash** counts as spending at withdrawal.
- **Outgoing P2P** defaults to spending and is reviewed like everything else.
- Pending transactions are never used for overspend prompts (section 19.24 shows them separately).

---

## 11. Transactions

- **Review.** All transactions go to the review queue. The suggestion is pre-selected. Nothing auto-confirms. Unreviewed transactions count immediately in the math under their suggested category (spending if there is none), with an "n unreviewed" badge. Exception: unknown deposits are held out of the plan (section 4).
- **Rules and suggestions.** Fully specified in section 18a. In short: user rules beat learned rules, which beat the statistical layers. A new rule applies to future and unreviewed transactions. Reviewed ones are untouched unless the user taps "apply to past". The user reviews every transaction daily and every correction teaches the engine.
- **Dedupe.** Key = (account, bank transaction id). A fallback fingerprint (account, date, amount, normalized description) flags "possible duplicate". **Id rotation heal:** if an unknown incoming id matches a stored transaction on (posted, amount, description, payee) and the stored id was not returned in the same response, re-key the stored row and keep its overlay instead of creating a duplicate (section 18a).
- **Bank edits.** A changed posted transaction updates in place, keeps the user's category, and logs the change.
- **Vanished transactions** are kept and flagged "missing from bank".
- **Overlay.** User edits (category, notes, month, tags, splits) are an overlay. Bank fields are never overwritten by the user, and bank updates never overwrite the overlay.
- **Hide.** A reversible soft delete (tombstone) keyed by bank transaction id. A hidden transaction is excluded from the math and never re-imported by sync.
- **Manual transactions.** Allowed. When a synced transaction later matches (same amount, within 3 days), suggest a merge.
- **Splits.** First-class (section 19.6).
- **Foreign currency.** Use the bank's account-currency amount.
- **Notes, tags and receipts** are supported (section 19.6).
- **Custom categories.** Must be placed in a group, which belongs to a bucket (section 1a). Optional soft targets only (section 19.23).
- **Normalization.** Merchant matching is fuzzy and normalized, so statement name drift does not break rules or recurrence detection.
- **Fees.** Bank and overdraft fees are spending, category "fees".

---

## 12. Transfers, refunds, reimbursements

- **Internal transfers.** Auto-paired when opposite sign, equal amount, between linked accounts, within 3 days. Paired = neutral. Still shown in review.
- **One-sided transfers.** Ask: neutral, savings, or spending. Contributions to an investment account default to **savings transfer** (counts toward savings, category Investments).
- **Credit card payments.** A payment to a linked card is a transfer. A payment to an unlinked card is spending (the purchases are invisible), and the user can recategorize.
- **Refunds and reimbursements net against the original expense's month.** This rule is the same for both. It applies even across months, and it works in either order (reimbursement before expense is allowed).
- **Matching** is specified in section 12a (receivables first, abstain when unsure, never auto-link on a guess).
- **Unmatched reimbursement.** Held outside income for 30 days, then asks.
- **Overpayment.** The excess asks, defaulting to income (other).
- **Links** are many-to-many and partial amounts are allowed. One payment can reimburse multiple expenses, and multiple payments can reimburse one.
- **Employer reimbursements** work the same way and never touch the baseline.
- **Expecting reimbursement.** An expense counts in full until the money lands. Each receivable can optionally be treated as netted now (section 12a).
- **Chargebacks** are treated as refunds. Reversals are negatives.
- **Roommate bill split** is a reimbursement.

---

## 12a. Receivables and reimbursement matching

### The failure this section prevents

You paid for a meal for two with a card. Your dad paid you back $11 by P2P. His **payback posted within minutes, but the card charge was still pending**, so it was not in the feed. The matcher saw only the payback, found no true counterpart, and linked it to some other transaction. The wrong reimbursement silently corrupts that month's spending.

This ordering is **systematic, not rare.** P2P payments (Zelle and similar) post almost immediately, while card purchases, especially restaurants, sit pending for days and may change amount when tips post. So paybacks routinely arrive in the feed before the expense they repay.

Root causes:
1. **Pending was never requested.** The protocol excludes pending transactions by default and includes them only when the client passes `pending=1`. If the pull does not ask, a pending charge cannot be seen at all.
2. **Closed-world matching.** The matcher only considers transactions it can see. When the true counterpart is invisible, the next-best visible candidate looks like a match.
3. **No abstain.** There was no minimum evidence threshold and no margin test between candidates.
4. **Amount and time only.** A partial reimbursement can be any amount up to the expense, so an amount rule cannot tell the right expense from a wrong one. Dating by posted date also penalizes the correct pairing, because the expense posts *after* the payback.
5. **Auto-linking instead of suggesting.** A guess was written as fact.
6. **No state for "payback received, expense not here yet".** Without it, the only options are to link wrongly or to drop the payment.

### Principles

1. **The matcher must be able to say "no match".** An unmatched payback is a valid state, not an error to resolve.
2. **Never auto-link on a guess.** Only two narrow cases may link without a human decision (an exact receivable match, and a proven learned pattern, section 12b), and even those are tentative and go through review.
3. **Receivables first.** An open receivable gets first claim on an incoming payment, so a payback cannot be captured by an unrelated expense.
4. **Your budget shows your share, not what you fronted.** Netting is real, but only against real evidence.
5. **Linking must work with invisible data, in both directions.** The expense may be invisible when the payment arrives (pending charge), or the payment may be invisible when the expense posts. The user can also declare what is owed before any money appears.
6. **Ask the provider for pending data, and design for the cases where it never comes.**

### The receivable object

A **receivable** is money someone owes the user.

- **Fields:** payer (a counterparty), amount (exact or a range), the originating expense or a split part, expected-by date (optional), status, notes.
- **Status:** `expected`, `partially received`, `received`, `written off`, `overdue` (derived from age).
- **Created by:**
  - one tap on any expense ("Dad owes me $11")
  - the split flow ("split with someone" at purchase time: your part vs their part, which creates a receivable for their part)
  - confirming a suggestion
  - a rule action (for example "dining with this payer")
- **Counts in full** against spending until money lands (the cash principle and section 12). Optional per receivable: **treat as netted now** (the user trusts the payer). If it is never paid, the receivable can be written off, which keeps it counted as spending with no change to the math, undoable.
- **Dashboard card:** "Owed to you", with aging.

### Matching an incoming payment

Order of evaluation for any incoming P2P-like deposit:

1. **Open receivable from the same payer.** If one open receivable matches by payer and amount (within a small tolerance for fees) and falls within a plausible window, link it **tentatively** (state `tentative`) and surface a one-tap confirm in the daily review. If several open receivables match, suggest the oldest and ask.
2. **Candidate expense scoring** (suggestion only unless a proven pattern applies, section 12b). Features:
   - **counterparty:** a payer already known as a reimburser (learned: "Dad pays back dining and groceries, usually within 3 days")
   - **amount relation:** equals an expected receivable; a clean fraction of an expense (1/2, 1/3, 1/4); at most the remaining unreimbursed amount; a round number
   - **time:** inside the payer's learned payback lag, or a default window
   - **expense type:** food, dining, groceries, rideshare, tickets, bills (categories historically reimbursed)
   - **memo text** matching the merchant or purpose
   - **uniqueness:** how many candidates remain
   - **constraints:** candidates already fully reimbursed are excluded
3. **Abstain.** If the best score is under the threshold, or the gap to the second-best candidate is small, or the best candidate would over-reimburse, do not link. The deposit becomes a **held payment** (`awaiting expense`): outside income and outside the plan. During the grace window (default 5 days) the system waits for the matching expense to appear (late match, below). After the window it becomes an **unlinked reimbursement**, held for 30 days (section 12), with an "owed to you" prompt asking what it was for.
4. **Unknown payer, no receivable, no candidate.** Treat as an unclassified deposit (held out of the plan until reviewed).

**Tentative links net immediately.** The money did land, so a tentative link nets the expense in the math right away, flagged as tentative. Rejecting or unlinking it reverts the netting. Confirming only changes the label and the learning signal.

**Hard constraints:**
- A link may not exceed the expense's remaining unreimbursed amount unless the user overrides. The excess flow in section 12 applies.
- Rejecting a suggested link records a negative pair that reduces similar suggestions.

### Calibration and learning

- Link suggestions use the same calibrated-confidence machinery as categories (section 18a), with labels from every confirm and reject. Full learning design: section 12b.
- **Counterparty profiles** are learned: typical amounts, lag, categories reimbursed, and whether the payer also sends non-reimbursement money (allowance, gift, income).
- A payer with a profile of both reimbursement and gift money is flagged ambiguous. Their deposits always go to review with candidates and no auto-tentative link unless a receivable exists.

### Pending and invisible items (both directions)

**1. Ask for pending.** Every pull passes `pending=1`. Pending items are marked by the pending flag, and `posted` may be 0 while pending. Some institutions never return pending items even when asked, so everything below must also work without them.

**2. When the pending expense is visible.** A payback can be linked **tentatively** to the pending expense. The link carries over when the charge posts (matched by account, merchant, a date window and an amount tolerance for tip adjustments). If the pending item vanishes, or posts at a different amount, the link is re-evaluated, and any unlinked remainder returns to `expected`.

**3. When the pending expense is not visible (your case).**
- The payback does **not** link to the best weak candidate. If no candidate clears the threshold, it becomes a **held payment** in state `awaiting expense`: outside income, outside the plan, shown as "Dad paid $11, waiting for the matching expense".
- **Grace window.** The system holds before declaring "no candidate" and asking. Default 5 days, then learned per account from the observed lag between a card purchase happening and posting. After the window it asks, as in section 12 (30-day hold).
- **Late match.** Every newly arrived or newly posted expense is checked against held payments. An expense that fits (merchant type, amount at or above the payback, transaction date at or before the payment, payer pattern) is suggested as the link, or auto-tentative under a proven pattern (section 12b).
- **Dating.** Use the transaction date (`transacted_at`) when provided, else the posted date. Because posting lags the real event, a candidate that *posts after* the payment is not penalized, up to the account's typical posting delay.
- **User declaration.** On the held payment, the user can say "this was for something not posted yet" and optionally add hints (merchant, approximate amount). That creates a placeholder that binds to the expense when it appears.

**4. Hidden pending hint.** Accounts report both a balance and an available balance. A persistent gap not explained by visible items suggests pending holds the feed is not showing. Semantics vary by institution and by account type (cards differ), so this is a **soft hint only**: it raises the probability that the real counterpart is invisible, which makes abstaining and holding stronger. It never drives a link.

**5. When the payment is the invisible one.** If the expense posts and the payback is not yet in the feed, a receivable ("Dad owes me $11") is the mechanism (see above). When the payment finally posts, it matches the receivable first.

**6. Optional manual "mark received".** The user may mark a receivable received before the feed shows it. The ledger entry is a manual transaction. When the real transaction arrives, a merge is suggested (section 11).

### Quick actions

- **"Dad paid me back"** on any expense: creates a receivable and optionally marks it received.
- **Split with someone** at purchase time: your share vs theirs.
- **Link picker:** shows open receivables, pending items (when available) and scored candidates, and lets the user link one payment across several expenses.
- **Unlink** with undo, and **write off**.

### Edge cases

1. **The payment posts while the expense is still pending or invisible (the reported bug).** Handled by held payments, the grace window and late matching (above).
2. The payment posts at a different amount than expected (a transfer fee, a tip).
3. The payment is cash: mark received manually.
4. Overpayment: the excess asks (section 12).
5. Two installments from the same payer: partially received, then received.
6. One payment covers two expenses: many-to-many.
7. The expense was on a different account or month: nets against the expense's month (section 12).
8. A receivable is never paid: aging alerts at 14, 30 and 60 days (configurable), then write-off.
9. A receivable marked "netted now" is later unpaid: show the unrecovered amount and offer write-off.
10. Two open receivables, same payer, same amount: suggest the oldest, ask.
11. The payer's deposit has no receivable and no candidate but resembles past reimbursements: suggest "reimbursement for what?" with a picker, never auto-link.
12. A pending inbound that is cancelled: the tentative link dissolves and the receivable returns to expected.
13. Platform double entries (a payment app balance and the bank) are deduped by amount, direction and window.
14. The payer sends a recurring allowance: the schedule engine flags a recurring pattern with no receivable, and asks "allowance or reimbursement?".
15. A payment received in a later month than its expense: no effect on the plan (reimbursements are not income), and the original month is recomputed.

### Acceptance tests (regression)

1. **The reported bug.** Dad's $11 posts. The McDonald's charge ($22.40) is pending and not in the feed. A larger unrelated expense E2 = $60 exists and is a plausible amount-only candidate. Expectation: no link to E2. The payment is held as `awaiting expense`.
2. **Same case, later.** The McDonald's charge posts two days later. Expectation: late match suggests E1 as the link (or auto-tentative under a proven pattern). E2 is untouched.
3. **Pending requested.** The same case with a provider that returns pending items. Expectation: the payback links tentatively to the pending charge, and the link carries over when it posts, even if the amount changes by a tip.
4. **Pending never provided, receivable exists.** E1 posts, the receivable "Dad, $11" exists, and the payment posts later. Expectation: matched to the receivable first, tentatively.
5. **No receivable, no candidate scores above threshold.** Expectation: held, then asks after the grace window.
6. **Two candidates inside the ambiguity margin.** Expectation: abstain and show both.
7. **A suggested link would exceed the expense's remaining amount.** Expectation: excluded as a candidate.
8. **Payer with no reimbursement history, round amount.** Expectation: ask, do not link.
9. **A pending inbound item is tentatively linked, then disappears.** Expectation: the link dissolves cleanly and the receivable returns to expected.
10. **A posted expense is dated after the payment by the account's typical delay.** Expectation: not penalized for the date order.
11. **A persistent available-vs-ledger balance gap.** Expectation: the hidden-pending hint raises the abstain bias but never creates a link.
12. **Pull request check.** Every scheduled pull sends `pending=1`. Expectation: verified in tests of the sync client.

---

## 12b. Learning reimbursement patterns

Section 12a defines what a safe match is. This section defines how the system learns to make better ones, so repeat situations (Dad pays back lunch, a roommate pays their share of utilities) become fast and nearly effortless, without ever guessing on a first encounter.

### What it learns

1. **Counterparty identity.** Map raw payer strings ("ZELLE FROM JOHN SMITH", a Venmo handle, a truncated name) to a **counterparty** the user names once ("Dad"). Aliases are fuzzy-matched, merged across platforms (Zelle and Venmo for the same person), and survive name or handle changes.
2. **Payer profile.** For each counterparty:
   - how often their deposits are reimbursements vs something else (gift, allowance, income)
   - typical payback lag, as a distribution, not a single number
   - typical amount and typical **share ratio** (a half, a third, an exact bill share)
   - categories and merchants they reimburse
   - rounding habits (rounds up to the nearest dollar, includes tip)
   - reliability (share of receivables fully repaid, and how late)
3. **Reimbursement patterns.** Human-readable rules induced from confirmed pairs, for example:
   - "Dad + Dining: about 50% of the total, within 3 days"
   - "Roommate + Utilities: a fixed $X on about the 3rd of each month"
   - "Friend X + concert tickets: the full ticket price, usually the same day"
4. **Expense-side prediction.** When a new expense posts, predict whether it is likely to be reimbursed, by whom, and for roughly how much and by when. This turns repeat situations into a one-tap "create receivable" suggestion.
5. **Recurring reimbursements.** Inbound payments that follow a schedule are linked to their bills by the schedule engine (section 14). When the bill posts, the receivable for the other person's share can be created automatically under an established pattern.
6. **Personalized aging.** "Overdue" is measured against that payer's own lag distribution, so a payer who always pays in 2 days triggers a nudge on day 5, and one who pays in 3 weeks does not.

### Pipeline

1. **Candidate generation.** For each inbound payment, candidates are: open receivables from that payer (or unknown payers), plus expenses with remaining unreimbursed amount inside a lookback window (payer-specific once learned, otherwise 30 days). Same currency is required, and account, direction and remaining amount must be consistent.
2. **Pairwise scoring.** A calibrated model scores each (payment, candidate) pair. Features:
   - payer profile fit (reimbursement likelihood, lag percentile, share ratio, category affinity)
   - amount relation (equals a receivable, equals a clean fraction, within the remaining amount, round amount, tip or fee deltas)
   - time gap relative to the payer's lag distribution, measured on transaction dates, with tolerance for the account's posting delay
   - expense features (category, merchant, amount, day and time, account, split or tag history)
   - memo and description text similarity to the expense (including emoji tokens)
   - uniqueness (how many plausible candidates exist) and constraints
3. **Global assignment.** Instead of choosing greedily per payment, solve the whole open set at once (min-cost bipartite matching with capacities). That resolves one payment covering several expenses, several payments covering one expense, and the same expense being claimed twice. An **ambiguity gate** triggers when the best assignment barely beats the second best.
4. **Three-way decision from calibrated confidence:**
   - **Auto-tentative** (only in the cases below), shown in review for one-tap confirm
   - **Suggest**, with the top candidates and reasons
   - **Abstain**, leaving the payment unlinked (section 12a)

### When auto-tentative linking is allowed

Auto-tentative means "linked, but waiting for your confirm". It is never silent and never skips review. It is allowed in exactly two cases:

1. **Exact receivable match** (section 12a).
2. **Proven pattern.** A payer-and-pattern pair graduates after **3 consecutive confirmations with no rejections** and calibrated confidence above the threshold. Any rejection or unlink strikes the pattern: one strike puts it back to suggest-only, and it must earn graduation again. A **materiality gate** applies: amounts above a threshold (default $100) are never auto-tentative.

Everything else stays suggestion-only, however confident the model is.

### Training signals

- **Confirm** a suggested or tentative link: positive.
- **Reject** a suggestion: negative for that pair and a weaker negative for similar pairs.
- **Manual link** the user made themselves: strong positive.
- **Unlink after confirming:** strong negative, and a strike on the pattern.
- **Classify a deposit as "not a reimbursement"** (gift, allowance, income): trains the payer's reimbursement-vs-other classifier.
- **Create a receivable by hand:** a label for expense-side prediction.
- **Edit the linked amount** or **write off a receivable:** adjusts the payer's share ratio and reliability.
- Training uses only user-supplied labels, never the system's own guesses (the same rule as section 18a).

### Pattern induction

- A pattern needs a minimum of 3 consistent confirmed pairs (a share ratio within tolerance, a lag inside a window, a category or merchant group).
- New patterns start in **probation** (suggest-only) and are shown with provenance, editable and disableable, like learned categorization rules.
- Patterns decay when not reconfirmed. A payer who stops reimbursing a category moves back to suggest-only, and the user is asked "Dad stopped paying back dining: update?".
- A user-authored reimbursement rule always beats a learned pattern.

### Counterparty management

- A **counterparty list** with profile cards ("Dad: 14 paybacks, median lag 2 days, median share 50%, reliability 100%").
- Merge and split counterparties, edit aliases, and set a policy: "always reimbursement", "never reimbursement" (an allowance), or "ask".
- Optional per-counterparty setting: **treat receivables as netted immediately** (off by default), for payers the user fully trusts.

### Explainability and tooling

- Every suggestion shows its reasons ("Dad usually reimburses dining within 3 days. $11 is 49% of $22.40. No other candidate.").
- A **matching debugger**, like the rules debugger, replays any payment and shows candidates, features, scores, the assignment, and why it won or abstained.
- All decisions are events, versioned and undoable (section 17). Model snapshots are versioned so past suggestions are reproducible.

### Evaluation and safety

- **Auto-tentative precision target: 99% or better.** A wrong link corrupts a month, so precision matters more than coverage.
- Track prequentially (prediction recorded before the label): precision and recall for suggestions, auto-tentative precision, abstain rate, false-link rate, time to link, and calibration by confidence bucket.
- If a pattern's recent precision drops below its threshold, it is auto-demoted and the user is notified.
- Learning is local. Counterparty data never leaves the server.

### Cold start

- Before enough labels exist, a starter prior applies (round amounts, nearby dates, food categories) to *suggest* only.
- The first few cases for each new payer always ask. Active learning asks the most informative question first (for example "is this deposit from Dad usually a payback or an allowance?").
- Backfilled history can bootstrap profiles: past deposits from a named counterparty are shown in a review pass so the user labels them once.

### Edge cases

1. A payer changes behavior (stops paying for a category): decay, a strike, and an update prompt.
2. Tip, tax and rounding make the share 49% instead of 50%: the share ratio tolerance is learned per payer.
3. The same person pays via two platforms: alias merge, with deduping of double entries.
4. A payer's handle or name changes: alias healing re-points the profile.
5. The memo is just an emoji or empty: emoji tokens are features, and an empty memo contributes nothing.
6. One payment covers several expenses: global assignment tries combinations within tolerance, and asks if more than one combination fits.
7. The payment arrives before the expense posts (common for P2P vs card): it is held, and when a plausible expense appears the late match logic of section 12a applies. The pattern's learned lag is measured in transaction-date terms, with the account's typical posting delay learned separately.
8. A payer who sometimes sends gifts and sometimes paybacks: flagged ambiguous, and every deposit asks (no auto-tentative).
9. Two counterparties with the same name or masked names ("JOHN S"): disambiguated by account, amount patterns and the user's naming.
10. A recurring fixed share (roommate utilities): the schedule engine matches by the due date window, and auto-tentative applies only after the pattern is proven.
11. A payer pays more or less than predicted: the difference asks (section 12), and the share ratio updates.
12. A user bulk-unlinks or rewrites past links: retrain from the corrected labels, and demote affected patterns.
13. A newly learned pattern conflicts with a user rule: the user rule wins, and the system asks what to do.
14. Cash paybacks produce no bank signal: manual "mark received" still trains the expense-side predictor.
15. The payback arrives in a different account than the expense was charged to: link regardless of account (section 12).

### Data model additions

`counterparty`, `counterparty_alias`, `reimbursement_pattern` (conditions, ratio, lag window, support, strikes, state), `link_event` (payment, candidate, source, confidence, outcome), `pair_feature_snapshot` (features at decision time, for reproducibility). All event-sourced and undoable.

### Acceptance tests

1. After 3 confirmed Dad-pays-half-of-dining pairs, the next one is auto-tentative and appears in review.
2. One rejection of an auto-tentative link demotes the pattern to suggest-only.
3. A deposit from a payer with mixed gift and reimbursement history never auto-links.
4. An amount above the materiality gate is never auto-tentative, however confident.
5. With two candidates inside the ambiguity margin, the system abstains and shows both.
6. A payer who always pays in 2 days gets an overdue nudge near day 5, and one who pays in 3 weeks does not.
7. Retraining after a bulk unlink changes later suggestions as expected.

---

## 13. Accounts

- Each account is `budget`, `tracked` or `balance-only`.
- Credit card spending counts on the swipe date. Interest on a linked card is spending (category "fees"). A card payment is a transfer if the card is linked.
- Loan payments are bills. Extra principal is a goal type ("debt payoff").
- Closed accounts keep their history and transactions still count. A disconnected account keeps the app running and shows a banner.
- **Coverage tracking.** Each account records its earliest data date and sync gaps. A month where any budget account lacks full coverage is flagged "partial". The plan from a partial month is labeled and overridable.
- **Account identity.** SimpleFIN may change account ids on re-link. Map by institution, name and mask, and dedupe history instead of duplicating it.
- **Balances** drive drift detection, the cash timeline and net worth (sections 19.1, 19.2, 19.13). They never change the budget math.
- Net worth and investments are covered in section 19.13. Joint accounts and households are not supported (single-user app, section 19.16).

---

## 14. Recurrence engine

### Schedule model

- **Fixed interval.** Any positive duration: 7 hours, 13 days, 3 weeks.
- **Calendar rule.** Monthly on a day (31 clamps to month end), semi-monthly (1st and 15th), nth weekday, last business day.
- **Time handling.** Day-multiples use home-time calendar arithmetic. Hour and minute intervals use absolute time, which avoids DST drift.
- **Anchoring.** An interval schedule re-anchors to the last observed occurrence. A calendar rule keeps its anchor.
- **Missing occurrence grace.** 25% of the interval, capped at 3 days, editable.
- **Effective-dated.** A schedule change (biweekly to semi-monthly) applies from its date.
- **Split deposits.** Two deposits from the same stream within tolerance count as one occurrence.
- **Variable amounts.** Fixed, or variable with tolerance. Forecasts use a trailing average.

### Detection and manual creation

- **Detection** infers an interval from the median gap of matching transactions. The default threshold is 3 occurrences (configurable).
- **Mark as recurring.** On any transaction: set type (bill, income, subscription), interval or calendar rule, amount (fixed or variable with tolerance), next expected date, and category. It attaches matching history and bypasses the threshold. This is how annual bills enter (detection alone would take years).
- **Forecasts.** The expected number of occurrences in a month is generated from the schedule. Months with extra occurrences are labeled (for example "extra paycheck"). The next plan is forecast mid-month.

---

## 15. Sync (SimpleFIN)

- **Scheduled pulls.** Two per day, about 06:00 and 18:00 home time. Bank update times vary by bank and by day, so a single pull can lag a day.
- **Manual sync.** 1 hour cooldown, cap about 6 per day. The UI notes that bank data updates about daily.
- **Quota.** A quota warning pauses sync for 24 hours and notifies. Exceeding limits can disable the token.
- **Window size is discovered.** Documented at 90 days per request, but there are recent reports of Bridge guidance for 45-day windows. Do not hardcode. Honor Bridge warnings. Treat date-range cap warnings as non-fatal. Chunk requests accordingly.
- **Backfill.** Do not assume depth. History varies per institution (some provide 1 to 2 years, others under 90 days). Walk backward in windows until windows come back empty. Spread a deep backfill over several days within quota. A backfill in progress marks affected plans as provisional.
- **Backfilled transactions** go to the normal review queue. No prompts for months older than the previous month.
- **Gap recovery.** Request from (last success minus 7 days overlap), chunked. A gap larger than what the Bridge returns is marked "data gap" and the user can add manual transactions.
- **Per-account status.** Partial failure keeps what succeeded.
- **Auth failure.** Push notification plus a persistent banner. A dead token or 403 starts a re-claim flow with a new setup token.
- **Always request pending.** Every pull passes `pending=1`. The protocol excludes pending transactions by default, and some institutions never return them even when asked. Pending rows are stored separately and reconciled when they post (section 19.24).
- **Idempotent.** Unique by (account, bank transaction id).
- **Protocol v2.** Use structured `errlist` errors (the older `errors` list is deprecated), the `connections` list, `balances-only` requests for cheap balance refreshes (cash timeline, drift detection, stale checks), and per-account filtering to retry only a failing account.
- **Stale** after 48 hours (configurable).
- **Pending** is supported when the provider supplies it (section 19.24). It is optional in the protocol, so its absence is normal.
- **Rollover job.** Runs at 00:05 home time on the 1st. It is derived and idempotent, keyed by date, so late data recomputes it. It catches up on startup after downtime. The server runs in UTC and calculates in home time.
- **Rollover digest.** One notification: "October plan: $X (+/- $Y vs last month), pot: $Z."

---

## 16. Notifications, offline, multi-device

- **Alerts** come from the catalog in section 19.3. Default push: auth failure, sync paused, overspend, projected shortfall, and the **daily review reminder** (at a user-set time, with how many items need attention vs look fine). Everything else is opt-in per alert.
- Quiet hours. At most 1 reminder per day. A weekly digest after 3 days away. Prompts persist while the user is away. Defaults apply only where specified (pot defaults to savings at month end, unresolved overspend carries at rollover).
- **What the server may do alone:** sync, dedupe, suggest categories and pairs, roll over the month, sweep leftover to savings, default the pot to savings, carry unresolved overspend. It never confirms transactions, picks an overspend source (except the rollover carry default), or edits goals.
- **Offline.** The PWA reads cached data and queues review actions. On reconnect the server is the source of truth.
- **Conflicts.** Last-write-wins per field. An explicit review decision beats a sync update.

---

## 17. Data and architecture

- **Ledger plus events.** Everything else is derived. No stored budget numbers except event-sourced values (spend_base events, standing lines).
- **Explainability.** A "why is my number $X" breakdown for any month.
- **Undo.** Every decision is an event, undoable at any time.
- **Config changes.** Bills, savings amount and settings are effective-dated.
  - **User-made config changes** and **data-driven changes** (late, edited or newly arrived transactions) apply immediately by recomputation. For example, a new bill reduces the pot and then spend_base now, and a late deposit raises next month's plan now.
  - **Scheduled jobs** (rollover, sweeps, default allocations) run at rollover, and are themselves derived, so late data recomputes them.
  - Old months stay as they were unless the user hits "recompute".
- **Money** is stored as integer cents. Per-day figures round down. Leftover cents roll into the last day.
- **Export** as CSV and JSON. Backups are handled outside the app. Data is kept forever, with a wipe-all option.
- **Security.** TOTP, 8 recovery codes, 30-day sliding sessions, lockout with backoff. The SimpleFIN access URL is encrypted and never logged or returned.
- **Empty states.** Define copy for no income, no bills, no goals. Large one-off transactions need no special handling.

### Architecture decisions (decided)

- **Stack:** Go backend, React frontend, Postgres, installable PWA (offline-first, IndexedDB). Full detail in the separate architecture document.
- **The server is authoritative for all budget math.** The PWA renders cached derived snapshots. Offline actions are queued with idempotency keys and recomputed on the server. The math is never duplicated in TypeScript.
- **ML runtime:** Go-native core (rules, nearest-neighbor case memory, logistic regression, calibration). It runs with nothing extra installed. An optional sidecar (OCR, embeddings, local models) may be added, but nothing in the core depends on it.
- **Existing build:** reuse the current UI components and the auth and sync plumbing. Rebuild the budget logic from this spec.
- **Self-hosted operations:** a Docker Compose deployment (app, worker, Postgres), configuration by environment or file, secrets from files or environment, TLS via a reverse proxy, in-app encrypted export plus Postgres dumps for backup, and versioned migrations.

---

## 18. Onboarding and learning

- On connect, determine coverage per account. If a **complete prior calendar month** exists for all budget accounts, `plan(current)` uses it and there is no learning phase.
- Otherwise the app shows **learning** and tracks only. The user may type a manual starting plan at any time.
- Initial setup: bills list, general savings, goals, EF target. `spend_base = plan - fixed`.
- Backfilled history enters normal review. Old months are derived for baselines but generate no prompts.
- A partial-coverage month is flagged and its plan is overridable.

---

## 18a. Categorization and rules engine

### Goals (proposed, measured continuously)

- **Teach-once.** For a non-ambiguous merchant, one confirmed label makes the next occurrence correct at least 95% of the time.
- **First-pass accuracy** (suggestion accepted unchanged), measured prequentially: at least 90% by day 30 and at least 95% by day 90.
- **Review speed.** Median 3 seconds or less per routine transaction.
- **No silent changes.** Every categorization is explainable and undoable.

### Principles

1. **Learn only from user labels,** never from the engine's own guesses (no self-training feedback loop).
2. **Explainable first.** Every suggestion carries its source and a plain-language reason.
3. **Nothing auto-confirms.** The user reviews every transaction daily (section 11). Confidence orders the queue and shapes the UI. It never skips review.
4. **Corrections teach scope, not just category.** A one-off is recorded as an exception and never trains the engine.
5. **Local by default.** No transaction data leaves the server unless the user opts in to an external suggester.
6. **Deterministic and versioned.** The same ledger, events and model snapshot produce the same suggestion.

### Input fields

Only `description` is required by the SimpleFIN protocol. Bridge-provided data may also include `payee`, `memo`, `MCC` and `transacted_at`, so every field below may be missing and the engine must degrade gracefully. Available signals:

- raw description, payee, memo, MCC (a weak signal, never trusted alone)
- amount, direction, pending flag
- posted and transacted dates (time of day only when it is a real timestamp)
- account and account type
- user notes and tags
- any extra payload

### Pipeline

1. **Ingest.** Bank fields are immutable. Output is stored as a *suggestion* in the overlay.
2. **Normalize** to a canonical merchant entity (below).
3. **Structural detectors** (decide the transaction's *type* before its category):
   - transfer pairing and card payments
   - recurring-schedule match (bill, subscription, income)
   - refund candidate and reimbursement candidate
   - P2P parsing (memo and counterparty)
   - loan proceeds and deposit type
4. **Rules**, staged (pre, default, post), user rules before learned rules.
5. **Statistical layers:** case memory, per-user linear model, starter prior, and an optional opt-in external suggester.
6. **Arbitration** produces one suggestion with calibrated confidence, a reason, and up to 3 alternatives.
7. **Triage** orders the review queue.
8. **Review** produces a label event, and learning updates run.

A suggestion stays live and may change as new knowledge arrives until the transaction is confirmed.

### Merchant normalization

- Strip processor prefixes (for example `SQ *`, `TST*`, `PAYPAL *`), store numbers, POS and terminal codes, dates, phone numbers and trailing location tokens. Keep the processor as a separate feature.
- Map raw strings to a **merchant entity** with aliases. Merchants are the key rules and models use.
- **Fuzzy alias clustering** (character n-gram similarity) proposes merges. Examples: `AMZN Mktp` and `AMAZON.COM`, or a statement name that drifts over time. The user can merge or split any merchant.
- Alias healing: a bank renaming a descriptor does not orphan rules or history.
- Multi-category merchants (Amazon, Walmart, Target, Costco, gas stations) are flagged **ambiguous**. A merchant-only rule is never trusted for them (see Learning).

### Features

- **Text:** character n-grams (2 to 5), word unigrams and bigrams, over normalized merchant, description, memo and payee.
- **Merchant id** and processor.
- **Amount:** log magnitude, exact-amount match, amount relative to this merchant's history (for example a $60 fill-up vs a $4 snack at the same station), and roundness (round amounts suggest P2P or cash).
- **Direction,** account, and account type (a business card pushes toward Business expenses).
- **Recurrence:** membership in a schedule, cadence, and "same amount as last occurrence".
- **Calendar:** day of week, day of month, and holiday proximity.
- **Context:** the previous transaction at the same merchant, trip context (a cluster of out-of-area spending), and user notes or tags.
- **P2P:** counterparty and memo text (a Venmo memo of "pizza" is evidence of dining).

### Rule model

**Two layers.**
- **User rules** are authored or explicitly approved by the user. They always beat learned rules.
- **Learned rules** are induced from your labels (see Learning). They are human-readable, editable, disableable, and show provenance.

**Stages (as in other rule engines):** `pre` (rename and normalize), `default` (categorize), `post` (override). Rules within a stage run least to most specific, so more specific rules win. A rule may be marked **stop processing**.

**Ordering within a layer:** specificity, then explicit priority (drag to reorder), then recency.

**Conditions** (AND, OR, NOT, nestable):
- merchant (canonical), raw description, payee, memo, MCC
- amount: exact, between, at least, at most, or within a percentage of a value
- direction, account, account type
- date: day of week, day of month, date range, time window
- pending flag
- linked bill, subscription or schedule
- tags and notes
- current category
- first time this merchant is seen
- operators: is, contains, starts with, ends with, regex, one of, fuzzy (similarity at least N), and negations

**Actions:**
- set category or tag, set merchant (rename), set a note (prepend or append)
- set type (transfer, income type, loan proceeds, deposit)
- mark expecting reimbursement, link to a bill or subscription, tag "from discretionary"
- assign to a month, hide, flag for review with a reason
- skip learning for this transaction
- split by percentage or amount (once splits ship)

**Tooling:**
- **Live preview:** "matches N existing transactions", with the list, before saving.
- **Backtest:** shows exactly what a rule would have changed historically, as a diff. Apply retroactively with undo.
- **Rule debugger:** paste or select any transaction to see every rule that evaluated, in order, with why each matched or did not, and which one won.
- **Versioning:** every rule edit is a version. Roll back any rule.
- **Time-bound rules** (for example "tag as trip during these dates").
- **Import and export** (JSON), and shareable seed rule packs.
- **Rule health** (below).

### Statistical layers

Arbitrated by calibrated confidence. Each reports a category, a confidence and a reason.

1. **Case memory (nearest neighbors).** Finds your past labeled transactions most similar in merchant, amount, account and context. Highest precision on personal data, and fully explainable ("similar: Chevron $48, 9 times, all Fuel").
2. **Per-user linear model.** Logistic regression over the features above with class weighting, updated incrementally from your labels. Trained per user, never on shared data.
3. **Starter prior.** A built-in model and seed rules so the first import is not blank. It is replaced in influence as personal labels accumulate and never trains on its own guesses.
4. **Optional external suggester (opt-in; section 19.17).** A local or API LLM for merchants nothing else recognizes. It receives only the normalized merchant string, never amounts, accounts or notes. Output is labeled "AI guess" and does not train anything until the user confirms it.

### Learning

**Label events.** Every review produces a record: suggested category, source, confidence, final category, scope chosen, and timestamp. Weights:
- Explicit pick: 2
- Correction of a wrong suggestion: 3
- Quick confirm: 1
- Quick confirm in under a second or during a rapid streak: reduced silently (guards against rubber-stamping, never blocks)

**Scope prompt after a correction.** The engine proposes the most likely generalization as the pre-selected default, shows how many past transactions it would change, and the user adjusts with one tap:
- This time only (an **exception**, never trains)
- This merchant
- This merchant when the amount is in this range
- This merchant on this account
- Anything matching this description pattern

**Rule induction.**
- After 2 consistent confirmed labels for a non-ambiguous merchant, a learned merchant rule is created.
- Amount-, account- or memo-conditioned rules need 3 consistent labels with a clean separation.
- Learned rules start in **probation** for their first 5 matches (still shown with reasons) and are promoted after those are accepted.
- Example output: "Shell, under $12, is Groceries (6 of 6)."

**Ambiguity.** When a merchant's labels are contradictory and not separable by amount, account or memo, it is flagged ambiguous. It always goes to review with its top 2 or 3 candidates and no learned rule is created.

**Drift and recency.** Older labels decay. Three consecutive corrections against a learned rule prompt "update this rule?".

**Conflicts with user rules.** If your correction disagrees with a user rule, the user rule still wins, and the engine asks: edit the rule, add an exception, or delete the rule.

**Undo.** Every label event and rule change is undoable. A mis-tap gets an immediate undo, and "undo learning" removes a label's influence without changing the transaction.

**Retroactive scope.** Reviewed transactions are never changed unless the user chose to apply to the past.

### Confidence, triage and self-evaluation

- **Calibrated confidence.** Counts-based (Beta posterior) for rules, Platt or isotonic calibration for the model, tracked against actual outcomes.
- **Review ordering:** uncertain items first (top candidates shown), then unusual-amount flags, then new merchants, then routine items that look fine.
- **Surprise flags.** A merchant seen many times with a consistent category but an outlying amount ("usually $40, now $212") is flagged for attention. This catches miscategorization and fraud.
- **Explanations.** Each suggestion shows its source and reason, for example "Learned rule: Shell, at least $25, is Fuel (14 confirmations)".
- **Prequential accuracy.** Every transaction records the suggestion before the label arrives. The dashboard shows:
  - overall acceptance rate
  - rate by source and confidence bucket (calibration curve)
  - confusion between categories
  - worst merchants
  - trend over time
- **Rule health:** each rule shows hits, accepted, corrected, precision, and last hit. Detected issues:
  - shadowed rules (never fire)
  - overlapping rules
  - conflicting rules
  - stale rules (no hits for 90 days, archived, not deleted)
  - low-precision rules (below threshold over the last 10 hits, auto-demoted)
- **Retro-review ("possible past errors").** Past confirmed transactions that the engine now strongly disagrees with are surfaced for a second look. This finds label noise.
- **Optional audit mode** (off by default). For a small random share of high-confidence items, the suggestion is hidden and the user labels it blind. This measures true accuracy and counters automation bias.

### Daily review loop

- **Daily review reminder** at a user-set time (push). It says how many items need attention vs look fine.
- **Queue persists** if the user skips days. Skipping never affects learning weights.
- **Review by merchant.** Batch mode groups the queue by merchant: one label applies to the group, with amount outliers called out.
- **Keyboard shortcuts** and swipe gestures. One tap confirms. Recategorizing opens the scope chips above.
- **Onboarding active learning.** On first import, the queue is ordered by transaction frequency times spend, so about 20 labeled merchants cover most of the volume.
- **New category prompt.** Creating a category triggers "move these merchants into it?" with candidates found from history.

### Special cases

- **Multi-category merchants** (Amazon, Walmart, Target, Costco, gas stations): learn amount clusters and account and memo signals. Flag as ambiguous. Never apply a merchant-only rule. Suggest a split when splits ship.
- **P2P:** counterparty-level learning (this person is always the rent share) plus memo text. Direction matters.
- **Structural detectors run first.** Transfers, card payments, bills, subscriptions and income schedules take their category from the linked object, not from text. Account pairs for transfers are learned.
- **Refund and reimbursement candidates.** A positive amount at a merchant you previously paid, within a window and at or under the original amount, suggests a refund link. A P2P inflow suggests a reimbursement link only under the rules in section 12a: receivables first, scored candidates, and abstaining when unsure. Link suggestions are never auto-applied.
- **Accounts as signal.** A business card or account pushes toward Business expenses. Learned automatically through the account feature.
- **Trips.** A cluster of out-of-area spending suggests a trip tag and Travel.
- **Tags** are learned the same way categories are.
- **Pending to posted.** A label and suggestion carry over when a pending transaction posts with a changed description or amount.
- **Transaction id rotation.** Some institutions re-issue ids for transactions already served. If an incoming id is unknown but a stored transaction matches on (posted, amount, description, payee) and the stored id was not returned in the same response, treat it as an id change: re-key the stored row and keep its overlay and labels, instead of creating a duplicate.
- **Imports (CSV/OFX)** run through the same normalization, so differently formatted descriptions still map to the same merchants.

### Data model

All event-sourced and undoable (section 17):
- `merchant`, `merchant_alias`
- `rule` (layer, stage, conditions, actions, priority, enabled, provenance, stats), `rule_version`
- `label_event` (transaction, suggested, source, confidence, final, scope, weight, session, time)
- `exception` (transaction, rule or pattern)
- `model_snapshot` (versioned, so past suggestions are reproducible)
- `suggestion` (transaction, category, source, confidence, reason, alternatives, model version)

Bank fields are never overwritten. User edits (category, notes, tags) are an overlay that sync never touches (a known failure in other apps is a sync overwriting the user's memo on every pull).

### Operations and privacy

- Models are small and trained locally. Rules and case memory update instantly after each label. The linear model updates incrementally and is re-fit in a background job after a batch of corrections.
- Nothing leaves the server unless the user enables the optional external suggester. Even then, only normalized merchant strings are sent.
- Re-evaluation of unreviewed suggestions runs when rules or the model change. Reviewed transactions are never touched.

### Edge cases

1. A bank renames a descriptor: alias healing re-points rules and history.
2. A category is renamed: stable IDs, nothing changes. Merged: rules, labels and model weights move to the survivor. Deleted: forced reassignment (section 1a).
3. A new category is created: the model starts cold for it. The new-category prompt proposes merchants from history.
4. The same merchant legitimately has two categories: ambiguity flag plus amount, account and memo conditions.
5. A correction contradicts a user rule: user rule wins, the engine asks what to do.
6. A learned rule starts failing: auto-demoted after low precision, shown in rule health.
7. The user changes their mind about a merchant permanently: drift prompt after repeated corrections.
8. Rubber-stamping: silent weight reduction, surprise flags, retro-review, optional audit mode.
9. A one-off: recorded as an exception, never trains.
10. Concurrent edits on two devices: label events are append-only, so both are kept and the later one is current.
11. Corrupt or bad training (many mis-taps): undo learning, plus rollback to an earlier model snapshot.
12. A merchant never seen before: starter prior, then the optional suggester if enabled, otherwise "new merchant" flag at the top of the queue.
13. Different accounts, same merchant, different purpose (personal vs business card): account feature plus account-conditioned learned rules.
14. A recurring item shares a merchant with discretionary spending (Amazon Prime vs Amazon shopping): the schedule engine splits them by cadence and amount.
15. Backfilled history is labeled in review like everything else and trains the engine the same way.

---

## 19. Extended systems (all in scope)

There is no v1 cutoff. Everything below is built. Each system obeys section 0: allow everything, record everything, block nothing.

### 19.1 Cash timeline and forecasting

- **Projected balance timeline** per budget account for the next 60 to 90 days. Starting point: the latest balance. Applied: expected bills, subscriptions, sinking charges, scheduled income, payment-plan installments, pending items, and credit card payments due. Uncertain amounts show a range based on past variability.
- **Credit card float.** Track statement balance, current balance, due date and minimum due for each card. The timeline and the cash check subtract card payments due, because card spending counts at swipe but the cash leaves later.
- **Shortfall alerts.** Fire when the projected balance drops below a user-set buffer (default $0). The alert names the earliest day and the cause.
- **Plan forecast.** Next month's plan so far, plus a 12-month projection built from schedules and trailing history.
- **What-if sandbox.** Simulate losing income, a raise, a new bill, a big purchase, or an extra-paycheck month. Compare scenarios side by side. Sandbox changes never write to the ledger.

### 19.2 Reconciliation and data health

- **Drift detection (read-only).** Compare each account's reported balance (as of its balance date) with the ledger-implied balance since the anchor. A mismatch is flagged with a probable cause: missing transaction, duplicate, pending item, or id rotation. One tap adds a reconciliation adjustment or resolves the cause.
- **Assigned vs held.** A read-only indicator: the sum of all virtual envelopes (savings, goals, emergency fund, discretionary, sinking, pot, bills reserve) vs the money actually held in budget and tracked accounts. It never alters the math. Optional rollover reality check.
- **Data health page.** Coverage per account, sync gaps, duplicates found, ids healed, review backlog, failing connections, stale accounts, rule health, and categorization accuracy.
- **Hidden pending estimate.** A persistent gap between balance and available balance that visible items do not explain is surfaced as "about $X of pending activity not shown by your bank feed". A soft hint (semantics vary by institution and account type) used to bias reimbursement matching toward abstaining (section 12a).
- **Duplicate connection detection** (the same institution connected twice).
- **Credit utilization** per card, and negative-balance flags.

### 19.3 Alerts and insights

**Alert catalog.** Every alert has a toggle, threshold, channel, quiet hours, and digest grouping.

- low balance; projected shortfall
- large transaction (fixed threshold or a multiple of that merchant's median)
- unusual amount at a known merchant; new merchant above an amount
- duplicate suspected
- subscription price increase; new recurring detected; trial converting
- overdraft, late, annual and interest fees
- bill due soon with reserve short; bill missing
- overspend; pot unallocated; income received or missing (late paycheck)
- goal behind or approaching its deadline; emergency fund below target
- receivable overdue (relative to the payer's own typical lag); payment received that matches a receivable; ambiguous payback needs a choice; pattern demoted after a rejection
- sync failure; stale data; daily review reminder
- card utilization high; large pending; unusual refund

**Defaults.** Push only for auth failure, sync paused, overspend, projected shortfall, and the daily review reminder. All others are opt-in.

**Insights feed** (ranked, dismissable, snoozable, each with a "why"): category vs trailing average, subscription creep, spending pace vs plan, savings rate, merchant concentration, anomalies. Weekly digest.

**Channels.**
- **Web push.** On iOS it requires iOS 16.4 or later and the app installed to the Home Screen. It does not work from a Safari tab, and iOS has no background sync, so the app syncs on open.
- Email (SMTP), ntfy, Gotify, Pushover, Telegram, Matrix and Discord webhooks, a generic signed webhook, and an in-app inbox.
- Per-alert routing, and the Badging API for the review count where supported.

### 19.4 Search, views and bulk operations

- **Global search** across description, memo, notes, merchant, category, tags, amount ranges and dates. A structured query language with saved searches and smart lists.
- **Bulk edit:** select many, then recategorize, tag, hide, link a reimbursement, mark reviewed, change month, or split. All undoable.
- **Command palette** and full keyboard navigation.
- **Saved views** usable as dashboard cards.

### 19.5 Import, export and portability

- **Import** CSV, OFX, QFX and QIF. Column-mapping presets are saved per institution. Imports dedupe against existing transactions by fingerprint, show a dry-run preview, and each batch is undoable.
- **Migration importers** for other budgeting apps, with a category-mapping wizard.
- **Export** CSV, JSON and OFX. **Full backup:** an encrypted archive of the ledger, events, rules and model snapshots. Restore from it. Scheduled export to a local path, S3-compatible storage or WebDAV.
- **Tax export:** a year-end report by category and tag.

### 19.6 Splits, tags, receipts and itemization

- **Splits are first-class.** A transaction splits into N parts, each with its own category, amount (parts sum to the total), tags and note. Rule action `split`. Learned split patterns are suggested (for example "Costco: 70/30 groceries and household"). Each part counts in its own bucket. Reimbursement links are per part. A refund allocates across parts.
- **Tags** are free-form or typed (trip, tax-deductible, reimbursable, project). Learned like categories, assignable by rules, filterable, and reportable.
- **Receipts.** Attach an image or PDF. Local OCR (a self-hosted OCR engine or a local vision model) extracts merchant, date, total and line items. It auto-matches a transaction by amount, date and merchant, and proposes splits from line items through the category rules. Configurable retention.
- **Order itemization.** Import an order-history export (for example Amazon's CSV) and optionally parse forwarded order emails. Match to charges (one charge may combine several orders) and itemize into splits with a learned item-to-category map.

### 19.7 Onboarding wizard

Resumable, skippable steps, re-runnable later:
1. Connect a provider.
2. Classify accounts (budget, tracked, balance-only, liability).
3. Coverage check with backfill progress.
4. Review detected recurring items (income, bills, subscriptions, payment plans).
5. Set general savings, goals, emergency fund, discretionary.
6. Teach-me queue (merchants by frequency times spend).
7. First plan reveal with a plain explanation.
8. Notification setup (including the iOS Home Screen install guide).

### 19.8 Previews, time travel and recaps

- **Config preview.** Every change shows before and after for plan, spend_base, pot, per-day, emergency fund target and goal dates, before confirming.
- **As-of view.** See any month as it was on a past date from the event log, and diff it against now.
- **Month recap** at rollover: plan, extra and allocations, overspend and covers, saved, emergency fund percentage, goal progress, absorption, categorization accuracy, and biggest changes. Exportable as PDF or image. **Year in review.**

### 19.9 Payment plans and loans

- **Payment plans** (BNPL and installment purchases): a finite-occurrence schedule with total, installment amount and remaining count. Detected from the first installment. Remaining installments count in the bills reserve. Completion frees the reserve into the pot. Early payoff supported. Category comes from the originating purchase.
- **Loans:** an amortization schedule from rate and term, with interest vs principal.

### 19.10 Subscription and bill intelligence

- Annualized cost, total monthly and annual, price history chart, renewal calendar, duplicate-service detection.
- **Trials:** mark a trial with a duration, or detect one from a small first charge. Alert before conversion.
- **Cancellation tracker.** Mark cancelled, verify no further charges, and alert if one persists. A user-maintained cancel-guide list.
- **Negotiation toolkit** (no third-party service): for bills with price increases, generate talking points and a letter, and track the outcome and the savings.

### 19.11 Savings helpers

- **Rollover transfer checklist.** SimpleFIN is read-only, so sofar cannot move money. It lists the exact transfer to make per real account, one tap to confirm, and verifies against transactions.
- **Goal projections** with what-if contributions and milestones.

### 19.12 Debt payoff planner

- Debts from liability accounts and loans: APR, minimum and balance (from the provider or manual).
- Strategies: avalanche, snowball, or custom order. Simulate payoff date and total interest, and compare scenarios.
- Extra payments are funded from a pot allocation or a standing line. Progress and interest paid so far. Integrates with the Debt payoff goal.

### 19.13 Net worth, investments, assets and liabilities

- **Net worth** is assets minus liabilities, with daily history from balance snapshots, broken down by account type.
- **Manual assets and liabilities** (home, vehicle, valuables) with valuation history.
- **Holdings** are a Bridge extension, not a standardized protocol field, and exist only for institutions and accounts that provide them. Fields: symbol, shares, market value, cost basis, and a per-share purchase price. Rules:
  - A SimpleFIN investment account's balance already includes the market value of its holdings. Never sum holdings on top of the account balance (that double-counts net worth).
  - Prefer the reported total cost basis. If only the per-share purchase price exists, multiply by shares.
- **Performance and allocation** views: gain and loss, allocation, and contributions (savings transfers) vs growth.
- **Income handling:** cash that lands in a budget account (dividends, interest) is income. Reinvested amounts are not cash and do not touch the plan.
- Pluggable price source for manually tracked holdings. Optional simple growth projections.

### 19.14 Multi-currency

- Each account has its own currency. Transactions are stored in the account currency. The plan is computed in the home currency.
- **FX rates** from Frankfurter, a free, open-source rates API with no key that can be self-hosted. Daily cache, converted at the transaction date. Weekends and holidays use the last available rate. The source is pluggable.
- Foreign spending uses the bank-charged amount as authoritative. FX fees are detected (category "Fees & interest").
- Net worth revalues balances at the current rate.

### 19.15 Providers

- **Provider interface:** an internal seam (accounts, transactions, balances, holdings, errors and rate limits behind a normalized schema, with per-provider adapters for quirks such as id rotation and pending handling). It exists for clean code and testing, not because a second live provider is planned.
- **SimpleFIN (the only live provider, protocol v2):** structured `errlist`, the `connections` list, `balances-only` requests, per-account filtering, and `pending=1` on every pull.
- **CSV and OFX import** and **manual accounts** are providers too, and cover institutions SimpleFIN cannot reach.
- If SimpleFIN is down or failing for an account, the fallback is CSV/OFX import and manual entry.
- Credentials are encrypted.

### 19.16 Intentionally not built

- **Households, multi-user, shared accounts, roles, invitations, guest links and settle-up.** sofar is for one independent person.
- **Product features:** billing, hosted multi-tenancy, telemetry or analytics, an admin console for other users.
- **A second live bank-data provider.** SimpleFIN only.
- **A credit score view** (no data source through SimpleFIN) and a **bill-negotiation concierge** (a human service, not software). The negotiation toolkit in 19.10 covers the useful part.

### 19.17 AI assistant (opt-in, grounded)

**Decided defaults:** off by default. When enabled it sees **aggregates only** unless the user widens the scope. The model is the user's own choice: a local model or their own API key. Nothing is sent anywhere by default, and there is no telemetry.

- **Natural-language Q&A** over the ledger via read-only tools: spending queries, "why is my number $X", month comparisons, scenarios. Answers cite the transactions behind them.
- **Natural-language rule creation** ("tag Uber over $40 on weekends as work"). It produces a rule draft with a preview. It never applies automatically.
- **Natural-language search**, monthly review narrative, and merchant-only categorization suggestions (the optional external suggester).
- **Models:** local, API (user key), or none. **Privacy controls:** choose whether the assistant sees aggregates or transactions, redaction of account numbers and names, per-tool permissions, and an audit log of every prompt and tool call.
- Write actions require confirmation and are undoable.
- **MCP server** exposing the same read-only tools (writes gated, scoped tokens) so external agents can query.

### 19.18 Platform: API, webhooks, observability

- **REST API** with OpenAPI, versioned. Scoped tokens (read-only, write, per-resource) and rate limiting.
- **Webhooks:** HMAC-signed, retried with backoff, with a delivery log and replay. Events: `transaction.created`, `transaction.updated`, `review.completed`, `overspend.created`, `pot.created`, `rollover.completed`, `sync.failed`, `alert.fired`, `goal.completed`, `ef.withdrawn`. Optional SSE or MQTT stream. CLI.
- **Observability:** `/healthz`, `/readyz`, Prometheus metrics (sync age per account, review backlog, accuracy, job durations), structured logs, optional OpenTelemetry, and a redacted diagnostics bundle.
- **Operations:** versioned schema migrations, scheduled encrypted export, and restore tests.

### 19.19 Security

- **Auth:** passkeys (WebAuthn) first, then TOTP and recovery codes. OIDC single sign-on for self-hosted identity providers.
- **Sessions:** list and revoke, trusted devices, login history with alerts, and step-up authentication for sensitive actions (export, token creation, data deletion).
- **Audit log** (append-only). **Panic lock** (revokes every session immediately). **Privacy mode** (amounts hidden until tapped).
- Encryption at rest for secrets, with optional full database encryption. CSP, CSRF protection, rate limiting, brute-force lockout, and dependency scanning.

### 19.20 UX and platform

- **PWA:** installable, offline-first, share target for quick-add and receipt capture, home screen shortcuts, app lock, and the Badging API for the review count.
- **Glance endpoint:** a read-only JSON feed of the hero number, per-day, and review count, for dashboards and automation tools that cannot use PWA widgets.
- Keyboard-first desktop UI, dark, light and system themes from design tokens, WCAG 2.2 AA accessibility, localization (locale, currency formats, week start, right-to-left), undo toasts everywhere, skeleton loading, empty states and error recovery.
- **Customizable dashboard** cards: hero number, per-day, pot, upcoming timeline, review queue, insights, goals, emergency fund, discretionary, owed to you, net worth.
- **Reports:** spending by category, group, merchant and tag over time, month over month, income vs spending, net cash flow, savings rate, plan history (plan, pot, spend_base, absorption ledger), cash-flow sankey, spend pace vs plan, subscription and trip reports. Custom report builder with saved reports. Export as CSV, PDF and PNG.

### 19.21 Trips

- A trip has a name, dates, destination, tag, optional linked goal, and an optional soft target.
- Auto-tagging by date range and out-of-area spending. Multi-currency aware. Per-trip report. A travel mode switches display to local currency and time.

### 19.22 Tax module (optional)

- **Tax set-aside envelope:** a standing line equal to a user-set percentage of the previous month's self-employment income (income funds next month, so this fits the model). Optional and never forced.
- Quarterly estimated-payment reminders on user-set dates. Payments are recorded in the "Taxes" category.
- Deductible tags, a year-end export by category and tag, and an optional mileage log.
- Informational only, not tax advice.

### 19.23 Soft targets

- Optional per category, group, tag or trip. Monthly or rolling.
- Informational only: they never change the plan math. Progress bars, threshold alerts, history comparison, and a suggested target from the trailing 3-month average.

### 19.24 Pending transactions

- **Requested on every pull** (`pending=1`). The protocol excludes them by default, the pending flag is optional, and `posted` may be 0 while pending. Some institutions never return pending items even when asked, so every feature must also work without them.
- Shown in a separate pending strip. Included in a secondary "safe to spend after pending" figure and in the cash timeline.
- **Excluded** from the plan, overspend prompts, and rule learning.
- Matched to the posted transaction by account, merchant, date window and an amount tolerance (tip adjustments). Suggestions and labels carry over.
- A pending expense or a pending inbound payment can be tentatively linked as a reimbursement counterpart (section 12a). If the provider does not return pending items at all, held payments, late matching and receivables are the mechanism, and the design never depends on seeing pending data.
- A pending item that never posts is dropped after a configurable period, with a note. Vanished holds are handled.

### 19.25 Gamification: the grove

sofar is gamified, on purpose, as a real game layer and not a decoration. The metaphor is **the grove**: a calm garden that grows from the user's actual finances and habits, in the app's sage and moss palette. The grove is a skin over the truth. It never changes budget math, never gates a feature, and never shames.

#### Design principles

1. **Skin over truth.** Gamification reads the ledger, events and snapshots. It never writes to budget tables, never changes `ComputeMonth` output, and never gates, hides or slows any feature (section 0, principle 3).
2. **Reward the habits that make sofar accurate, and the user's own goals.** Points come from reviewing, teaching the engine, planning, tidying data and collecting what is owed. **Never reward spending less.** "On plan" is a neutral status, not a prize. Rewarding restraint risks reinforcing unhealthy restriction and ignoring bigger-picture goals.
3. **Goodhart guard.** Rewards must not corrupt the training signal. Review XP depends on deliberate, quality actions, never on speed. A speed bonus or combo meter is forbidden.
4. **No shame, no loss framing.** No "you failed", "streak broken" or "you'll lose". Copy never uses guilt. No push notification threatens a loss.
5. **Forgiving streaks.** Weekly cadence, earned freezes, rest mode, decay instead of reset, and milestones that are kept forever (below). Perfectionist streaks that reset to zero create anxiety and eventual abandonment, and the overall streak effect fades.
6. **Overjustification guard.** Expected, contingent, tangible rewards can undermine intrinsic motivation. Prefer informational feedback ("your engine got your fuel purchases right 19 of 20 times") and unannounced celebration of real milestones over escalating points. Stimulation tapers as habits form (below).
7. **Local only.** No leaderboards, no social features, no telemetry, no real-money rewards, no loot boxes or random rewards, no countdown timers. Optional share cards hide amounts.
8. **A dial, not a mandate.** Off, quiet or full, per component. Default is full. The user can turn it down at any time without losing anything.
9. **Sticky awards, deterministic progress.** Progress meters recompute from history. Awards are recorded as events with their evidence and are never revoked by a recompute.
10. **Calm.** Playful, never loud. Reduced-motion and sound-off defaults are respected. Celebrate with visuals and warmth, not exclamation points.

#### Intensity

| Level | What the user sees |
|---|---|
| off | nothing. No grove, no XP, no badges, no streaks. No computation beyond a flag. |
| quiet | weeks tended, badges, month recap highlights, text only. No animation. |
| full (default) | the grove, growth and levels, quests, badges, celebrations, haptics, season recap. |

Per-component toggles exist (grove, growth, quests, badges, streaks, celebrations, haptics, recap). A **celebration intensity** slider (0 to 3) controls animation size. **Taper:** an optional setting (on by default) makes celebrations quieter over time as habits form, unless the user opts to keep them.

#### The core loop: tending the grove

- A **tended day** is one where the day's new transactions are all reviewed, or at least 5 deliberate decisions were made. A day with nothing new to review counts as tended automatically.
- A **tended week** is at least 4 tended days (configurable). The streak counts **consecutive tended weeks**.
- **Rest mode** pauses the streak without cost (travel, illness, a break). A **freeze** is earned (1 per 4 tended weeks, maximum 3), never bought, and applies automatically to a missed week.
- **Decay, not reset.** A missed week with no freeze reduces the streak by a quarter (rounded down), not to zero. The **longest streak and every badge are preserved forever**.
- The home screen shows no streak counter by default (to avoid number anxiety). The streak appears inside the grove as a growth ring.
- Finishing a day's review plays a short "all caught up" moment where something in the grove grows. No combos, no timers.

#### Growth (XP) and levels

Growth comes from actions that improve sofar's accuracy or the user's own plan. Tunable defaults (also in `docs/config-defaults.md`):

| Action | Growth |
|---|---|
| deliberate category pick | 3 |
| quick confirm (only if viewed at least 1.5 s) | 1 |
| fixing an engine mistake (recategorize a wrong suggestion) | 5 |
| choosing a scope when prompted | 2 |
| teaching a new merchant | 5 |
| batch-by-merchant confirm | 2 per merchant (not per transaction) |
| allocating the uncategorized amount | 10 |
| creating or adjusting a goal | 5 |
| setting an emergency fund target | 10 |
| resolving an overspend | 5 |
| confirming a reimbursement link | 5 |
| resolving a data drift | 10 |
| classifying an unknown deposit | 2 |
| collecting a receivable (received) | 8 |
| marking a recurring item | 5 |
| a cancelled subscription verified (no further charge) | 15 |

**Integrity rules**
- A soft daily cap of 60 growth, with 50% value after 40.
- Diminishing returns on repeated identical actions.
- A confirm under 1.5 seconds, or a rapid streak of confirms, earns 0 and is never accused.
- Hidden transactions, undone actions and duplicates earn nothing. An action undone before the nightly settle earns nothing.
- Growth and awards are computed in a nightly settle on settled state, so a quick undo cannot be farmed.

**Levels** (cumulative growth thresholds, tunable): seed 0, sprout 100, seedling 300, sapling 700, bloom 1500, thicket 3000, grove 6000, orchard 12000, woodland 24000, old growth 48000.

#### The grove: a visual world derived from real state

Everything below is **derived** and re-renders from the ledger, so it cannot be faked.

| Real thing | In the grove |
|---|---|
| savings goal | a tree growing with progress. Mature when complete, with a small plaque naming what it was for. Borrowed from: pruned (neutral color, never shown as failure) |
| emergency fund | a greenhouse or old oak, filling with months of cover. After a withdrawal it is "regrowing" |
| discretionary | a flower patch |
| sinking funds | seed beds that fill until their bill is due |
| subscriptions | a hedge. Cancelling one trims it and the freed money appears as seeds |
| uncategorized (the pot) | a pile of seeds waiting to be planted. Allocating them is planting |
| owed to you | sprouts that bloom when the money arrives |
| debt payoff | a clearing where weeds recede as the balance falls |
| engine fluency | the grove's overall health and a fluency meter (below) |
| the current month | the season of the real calendar month, changing gently |
| tended weeks | a growth ring on the oldest tree |

- Rendering is procedural from stable ids and a seed (SVG or canvas), light and dark, no heavy assets.
- Accessibility: a text description of the grove state for screen readers, full keyboard navigation, reduced-motion respected.
- Privacy mode hides amounts. A goal's plaque shows its name only.

#### Engine fluency: teaching sofar

Fluency shows how well the engine knows the user, using the real metrics from section 18a:

- first-pass accuracy over 30 days (learning below 70%, getting it 70 to 85, 85 to 90, fluent 90 to 95, native 95 or more)
- share of transaction volume covered by known merchants
- teach-once success rate
- "caught" count: engine mistakes the user corrected (a real training signal, and worth celebrating)
- average review time per transaction (informational, never a target)

Nothing here rewards speed. It rewards teaching well.

#### Quests

A weekly board of up to 3 quests, generated from **real opportunities** in the insights feed, never from restriction:

- plant the uncategorized (the pot is non-zero)
- collect what you are owed (an overdue receivable)
- teach 5 new merchants (new merchants in the queue)
- clear the old queue (unreviewed items older than 2 days)
- close the gap (an unresolved drift)
- name your counterparties
- set an emergency fund target
- look at your subscriptions (annualized cost)
- spot a duplicate or a possible miscategorization (the retro-review list, framed as a bug hunt)
- blind taste test (audit mode): label a few blind and agree with the engine's accuracy

User-defined challenges (for example a no-dining-out week) are allowed because they are the user's own goals. The app never suggests restriction challenges. Quests never expire punitively: an unfinished quest quietly rotates away, with no failure state.

#### Achievements

Badges are earned from real events, kept forever, and show their **evidence** ("earned oct 2026: your first goal reached 100%"). Families:

- **habit:** first tended week, 4, 12, 26, 52 tended weeks, longest streak tiers
- **teaching:** fluency tiers, first merchant taught, 100 mistakes caught, a perfect blind test
- **money:** first goal completed, emergency fund at 1, 3 and 6 months, a debt cleared, saved-amount milestones the user chose
- **clean books:** a month with every transaction reviewed and no drift (not awarded if an unusual share of transactions were hidden that month)
- **collector:** first receivable collected, all receivables collected for a quarter
- **explorer:** used the explainer, ran a what-if, imported a CSV, linked a reimbursement
- **seasons:** first full year, a three-paycheck month handled, a year in review
- **secret** (hidden until earned): for example "caught a double charge"

There are no negative badges. Achievements never gate anything.

#### Rewards

Cosmetic and informational only: grove decorations and plants, color themes, app icons, titles, season recap artwork, dashboard card skins. **Never** features, never data, never anything behind a wall. Rewards are deterministic and tied to real milestones (no random drops). Unannounced celebrations for genuine milestones are allowed, and progress toward them is not displayed as a countdown.

#### Season recap

At rollover the month becomes a season: a short, shareable recap derived from real data: weeks tended, engine accuracy change, goals advanced, receivables collected, pot planted, biggest wins, and one gentle "next" suggestion. Exportable as an image with amounts hidden by default. Skippable.

#### Notifications

Gamification pushes are **off by default**. Optional: a weekly recap, and a quiet "your grove grew" digest. Never a streak-loss reminder, never a guilt message, never more than one per day, and quiet hours apply.

#### Copy rules

- Lowercase, warm, calm. No exclamation points, no guilt, no countdowns.
- Never the words failed, lost, broken, missed or behind in gamification copy.
- Examples: "all caught up. something grew." "you taught sofar 3 new merchants this week." "fuel purchases: right 19 of 20 times."
- A CI check lints gamification strings for forbidden words and punctuation.

#### Data and architecture

- Module `internal/play`: reads events and snapshots only, never writes budget tables.
- Tables: `play_settings`, `play_xp_events`, `play_awards` (with evidence snapshot), `play_streaks` (weeks, freezes ledger, rest periods), `play_quests`, `grove_state` (derived cache).
- A nightly `play-settle` job (idempotent, keyed by date, home time zone) computes the day's XP, tended-day status, awards and quests.
- Enabling gamification starts from now by default. An optional one-time **backfill** can grant badges (not growth) for past achievements.
- Export includes play data. A **reset grove** action deletes all of it.

#### Edge cases

1. Time zone and DST: days are home-time calendar days. Travel mode pauses the streak rather than breaking it.
2. A day with no new transactions counts as tended automatically.
3. Goal deleted: its tree moves to a quiet "retired" area. Abandoned goal: the tree goes to a "wild" area, never shown as a loss.
4. Goal borrowed from: the tree is pruned (neutral), and recovers as it is repaid.
5. Undo, hide and duplicates earn nothing, and an undo before the nightly settle removes the growth.
6. A recompute never revokes an earned award. Progress meters (levels aside) recompute honestly.
7. Hiding transactions to chase "clean books" is detected by an unusual hidden share and silently withholds the award.
8. Multi-device and offline: actions queue, and growth is computed server-side at settle.
9. Rubber-stamping: reduced growth only, no accusation.
10. Streak freezes accumulate beyond the cap: they stop accruing, never waste.
11. The user turns gamification off and on: nothing is lost. Earned awards are preserved.
12. A large data import: imported or backfilled transactions earn no growth.
13. Retro-review later disagrees with an old label: no clawback, and it lowers the quality weight of future quick confirms only.
14. Month of zero transactions: the week is tended automatically.
15. The year rolls over: yearly badges and the season recap handle the boundary in home time.

#### Acceptance tests

1. With gamification off, no play data is computed and no UI element appears.
2. Running the budget engine with and without the play module produces identical `MonthState` hashes.
3. 100 confirms in 30 seconds earns near zero growth.
4. A deliberate correction of an engine mistake earns more than a confirm.
5. A missed week with a freeze keeps the streak. Without one, the streak drops by a quarter and the longest streak is unchanged.
6. An award earned before a recompute is still present afterward, with its evidence.
7. Hiding 40% of a month's transactions withholds "clean books".
8. A goal reaching 100% adds a mature tree and an award with evidence.
9. All gamification copy passes the forbidden-word and punctuation lint.
10. With celebration intensity 0 and reduced motion on, no animation plays.

---

## 20. Decisions log

### Decided by Alec

- **Deposit type** semantics are confirmed: an external inflow into a tracked account that counts as income and records the savings movement.
- **AI assistant:** opt-in, off by default, aggregates only by default, the user's own model (local or own API key). sofar is self-hosted, for an individual, and not a product.
- **Households and multi-user:** not built. Personal finances for one independent person (section 19.16).
- **Providers:** SimpleFIN only. No second provider (section 19.15).
- **Auto-tentative reimbursement links:** two cases (exact receivable match, and a proven pattern) link tentatively and wait for confirmation (sections 12a and 12b).
- **Tentative links net immediately** and revert on reject.
- **The server is authoritative for all math.** Go-native ML core. Reuse the current UI and plumbing, rebuild the logic.

### Requested by Alec, design proposed (confirm or adjust)

- **Gamification:** "almost gamified, or maybe just plain out gamified." Designed as section 19.25 (the grove): garden metaphor, growth and levels, weekly forgiving streaks with earned freezes, quests drawn from real opportunities, sticky badges with evidence, cosmetic-only rewards, an off, quiet or full dial with full as the default. Guardrails: never rewards spending less, never gates features, never alters budget math, protects the learning signal from speed-chasing, no shame, local only.

### Accepted as proposed

These were proposed in this spec and accepted without changes:

- Emergency fund rebuild suggestion of 6 months, chosen at withdrawal time (section 8).
- Goal purchase edge cases (section 7).
- All tunable defaults exposed in settings (manual sync cap, quiet hours, 48h stale, 3-occurrence threshold, 30-day reimbursement hold, 5-day held-payment grace window).
- Default category groups (section 1a).
- Correction scope prompt always shown as one-tap chips with the best guess pre-selected (section 18a).
- Learning thresholds: 2 labels for a merchant rule, 3 for conditional rules, 5-match probation (section 18a).
- Audit mode off by default (section 18a).
- Accuracy targets: 95% teach-once, 90% first-pass by day 30, 95% by day 90 (section 18a).
- Receipt OCR: a lightweight self-hosted OCR engine by default, with an optional local vision model for hard receipts (section 19.6).
- Tax module included as designed, optional (section 19.22).
- Receivable netting default: count in full until money lands, with an optional per-receivable "treat as netted now" (section 12a).
- Reimbursement learning thresholds: 3 confirmations to graduate, one strike to demote, $100 materiality gate, 99% auto-tentative precision target (section 12b).
- Per-payer auto-net setting, off by default (section 12b).
- Not building: a credit score view and a bill-negotiation concierge (section 19.16).

### Still open

None that block implementation. Facts that need real data are covered by the data-discovery probe in the architecture document.
