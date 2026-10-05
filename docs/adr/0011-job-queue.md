# ADR-0011: job queue: small in-repo postgres queue

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
jobs must be enqueued in the same transaction as the change that caused them, run idempotently, and catch up after downtime.

## decision
a `jobs` table with a unique key, `FOR UPDATE SKIP LOCKED` workers, enqueue inside the caller's transaction, and a scheduler guarded by a postgres advisory lock. no River.

## consequences
single user and few job kinds; architecture A1 already says sofar owns scheduling and catch-up.

## alternatives considered
River (rejected: durable periodic jobs are a paid feature).
