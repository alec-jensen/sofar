# ADR-0006: single-user, self-hosted, not a product

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S0, S19.16

## context
sofar is a personal finance app for one independent person who runs it themselves.

## decision
no households, multi-tenancy, billing, telemetry or admin console for other users. one owner, with strong auth (passkeys, totp), because the app is reachable outside the lan and holds bank data.

## consequences
simpler schema and auth. no sharing features.
