# ADR-0010: migrations: goose with embedded sql

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the legacy app runs an idempotent schema.sql at every start with no history. the rebuild needs real, versioned migrations.

## decision
pressly/goose v3, sql files embedded from internal/store/migrations, run at startup and by `sofar migrate`. the legacy schema.sql is retired; migration 00001 quarantines legacy tables into the `legacy` schema.

## consequences
versioned, permissive licence, plain sql.

## alternatives considered
sqlx-style inline schema (rejected: no history), atlas (rejected: heavier).
