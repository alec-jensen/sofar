# ADR-0013: property tests: rapid

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the budget engine's invariants (architecture A9.3) need generated tests.

## decision
pgregory.net/rapid for generated invariants (test-only dependency).

## consequences
shrinking and seeded reproducibility.

## alternatives considered
testing/quick (rejected: no shrinking), gopter (rejected: less maintained).
