# ADR-0020: sync request budget: token bucket

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
simplefin allows about 24 requests per day per access url, and the owner's real account must never be starved.

## decision
a persisted token bucket of 20 requests per rolling 24 hours per access url (the provider allows about 24), shared by scheduled, manual and backfill requests. the probe uses a separate access url.

## consequences
headroom under the provider cap and no shared-quota surprises.

## alternatives considered
fixed counters (rejected: restarts could exceed the cap).
