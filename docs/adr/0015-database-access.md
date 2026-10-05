# ADR-0015: database access: database/sql with pgx stdlib

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the legacy code uses database/sql with pgx and hand-written sql.

## decision
hand-written sql in repository packages under internal/store. no orm and no sql code generation.

## consequences
matches the existing code and keeps queries reviewable.

## alternatives considered
sqlc and gorm (rejected: extra tooling and indirection).
