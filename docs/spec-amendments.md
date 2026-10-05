# spec amendments

The spec (`docs/spec.md`) is the source of truth for behavior. It changes only through this file, with the owner's approval.

## how to propose an amendment

1. Add an entry below with: id, date, spec section, what the spec says, what you found (evidence, fixture or test name), the proposed change, and the conservative reading you are following in the meantime.
2. Continue implementing the more conservative reading.
3. Ask the owner. When approved, update `docs/spec.md`, mark the entry applied, and record it in the spec's decisions log (section 20).

## entry template

```
### A-000: title
- status: proposed | approved | applied | rejected
- date:
- spec section:
- spec says:
- finding / evidence:
- proposed change:
- interim behavior:
- owner decision:
```

## entries

A-001 to A-010 were filed while writing the implementation plan (`docs/plan/engine-semantics.md`). Each is a place where the spec allows two readings or leaves an order unstated. Implement the interim behavior; the owner decides.

### A-001: manual spend_base edits versus absorption
- status: proposed
- date: 2026-10-05
- spec section: S2 (spend_base rules)
- spec says: absorption sets `spend_base(M) = available(M)` when available falls below last month's spend_base. Separately: "manual edits to spend_base or standing lines are never auto-reversed", and overcommitment is shown and allowed.
- finding / evidence: a manual edit above what available can fund is cut back by the very next month's absorption check, which reverses it automatically.
- proposed change: a manual edit sets a **pin**. Absorption never cuts below the most recent pin. While the pin exceeds available, the month shows "overcommitted by $X". The pin clears when the user edits spend_base again or removes it.
- interim behavior: as proposed.
- owner decision:

### A-002: leftover monthly spending money at month end
- status: proposed
- date: 2026-10-05
- spec section: S2 (pot behavior), S7 (general savings)
- spec says: S2 says "unspent one-time spending money sweeps to savings at month end". S7 says "any leftover or unallocated money automatically goes to savings".
- finding / evidence: S2 names only one-time money; S7 names all leftovers. The two differ for leftover base spending.
- proposed change: all positive leftover of the monthly spending pool (base plus one-time) sweeps to general savings at rollover. Discretionary never sweeps.
- interim behavior: as proposed (follows S7, the broader rule).
- owner decision:

### A-003: "leave uncovered" versus unresolved overspend
- status: proposed
- date: 2026-10-05
- spec section: S10 (overspend flow, steps 4 and 5), S0 principle 3
- spec says: unresolved overspend auto-carries into next month's pool. Uncovered overspend "carries as a visible negative balance into following months until settled".
- finding / evidence: it is unclear whether an explicit "leave uncovered" also reduces next month's spending.
- proposed change: unresolved (no decision by rollover) reduces next month's pool, flagged "auto-carried". Explicit "leave uncovered" goes to an `uncovered` debt balance shown with a "settle" action and does **not** reduce later pools.
- interim behavior: as proposed.
- owner decision:

### A-004: subscription charges and the spending pool
- status: proposed
- date: 2026-10-05
- spec section: S6, S1a
- spec says: subscriptions are a sub-bucket of spending, reserved up front in `fixed(M)`, with the same rules as bills.
- finding / evidence: if a subscription charge also drew from the spending pool, it would be counted twice (once in fixed, once in spending).
- proposed change: a subscription charge consumes its subscription reserve line. Only an overage beyond the reserve draws from spending, exactly like bills. Reports still group subscriptions under spending.
- interim behavior: as proposed.
- owner decision:

### A-005: absorption runs before the month's own decisions
- status: proposed
- date: 2026-10-05
- spec section: S2 (spend_base rules, worked example C)
- spec says: absorption applies when `available(M) < spend_base(M-1)`. A permanent allocation raises fixed or spend_base.
- finding / evidence: if a permanent allocation made this month were included before the absorption check, the user's own allocation could trigger absorption of their own spending.
- proposed change: absorption compares last month's spend_base with this month's **data-driven** available (before this month's user events). This month's permanent allocations and manual edits are applied afterwards.
- interim behavior: as proposed. Worked example C passes either way; this matters only when allocating in a month that also has a plan drop.
- owner decision:

### A-006: covers shrink in reverse order
- status: proposed
- date: 2026-10-05
- spec section: S10 step 6, S2 example F
- spec says: "a refund or reimbursement that shrinks an overspend shrinks the cover, and money returns to its source in reverse order".
- finding / evidence: "reverse order" could mean the reverse of the suggested source order or the reverse of when the covers were made.
- proposed change: reverse of when the covers were made (the newest cover shrinks first).
- interim behavior: as proposed.
- owner decision:

### A-007: emergency fund rebuild math
- status: proposed
- date: 2026-10-05
- spec section: S8 (rebuild)
- spec says: a rebuild line "redirects general savings into the EF first. Only if general savings is $0 does it reduce spending."
- finding / evidence: when general savings is above $0 but below the rebuild amount, the spec does not say whether the remainder reduces spending.
- proposed change: take `min(rebuild, general savings)` from general savings, and the remainder adds to fixed (reducing spending).
- interim behavior: as proposed.
- owner decision:

### A-008: discretionary is drawn automatically, not chosen as a cover
- status: proposed
- date: 2026-10-05
- spec section: S9 (draw order), S10 (cover sources and suggested order)
- spec says: purchases draw from discretionary automatically once the pool hits 0, and overspend triggers only when both are exhausted. The suggested cover order starts with discretionary.
- finding / evidence: by the time an overspend exists, discretionary is already at 0 or below, so it cannot be a useful cover source.
- proposed change: discretionary is never a cover source. The cover sheet lists next month's spending, general savings, goals, the EF, the bills reserve, and leave uncovered.
- interim behavior: as proposed.
- owner decision:

### A-009: demo mode
- status: proposed
- date: 2026-10-05
- spec section: S17 (server authoritative), ADR-0003
- spec says: budget math is never reimplemented in TypeScript.
- finding / evidence: the current app has a demo mode that computes budgets in the browser from sample data. The spec does not mention a demo.
- proposed change: remove the client-side demo in M8. Add a server-side demo (`sofar demo` seeds a synthetic year from the simulation generator into a throwaway database) in M12, or drop the demo entirely.
- interim behavior: the demo keeps working until M8 replaces the client, then is removed. The server-side demo is not built unless the owner approves it.
- owner decision:

### A-010: "short intervals" in boundary attribution
- status: proposed
- date: 2026-10-05
- spec section: S3 (boundary attribution)
- spec says: tolerance is `min(5 days, interval/4)` and "it does not apply to short intervals".
- finding / evidence: "short" is not defined.
- proposed change: boundary attribution applies only to schedules with an interval of 28 days or more (monthly and longer). Weekly and biweekly items count in the month they land.
- interim behavior: as proposed, configurable (`docs/config-defaults.md`).
- owner decision:
