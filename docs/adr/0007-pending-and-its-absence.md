# ADR-0007: request pending and design for its absence

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S12a, S15, S19.24

## context
simplefin excludes pending transactions unless the client passes pending=1, and some institutions never return pending items. paybacks via p2p often post before the card charge they repay.

## decision
every pull sends pending=1. matching works with visible pending items (tentative links that carry over on posting) and without them (held payments, late matching, receivables). a payback is never linked on a guess.

## consequences
the reported mcdonalds bug cannot recur. matching must abstain, and the system needs held-payment and receivable states.
