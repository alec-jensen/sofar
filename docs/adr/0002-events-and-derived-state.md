# ADR-0002: events plus derived state

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S0, S17; docs/architecture.md A2

## context
late and edited transactions must recompute history, every decision must be undoable, and every number must be explainable.

## decision
raw bank data is immutable, user decisions are append-only events, all budget numbers derive from pure functions (`ComputeMonth`) over raw data, overlays, effective-dated config and events. snapshots are caches keyed by an input hash and engine version. recompute walks forward from the earliest dirty month with early cutoff.

## consequences
undo, replay and explainability come almost free. the engine must be deterministic and heavily tested. engine upgrades need a recompute-all tool.

## alternatives considered
mutable budget tables (rejected: cannot explain or safely recompute), full event sourcing for everything including raw data (rejected: raw data already has natural immutability).
