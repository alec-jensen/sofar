# sofar: milestone plan

Work in order. Each milestone has tasks, tests that must exist, exit criteria, and any human gate. Tick boxes as you go and log progress in `docs/progress.md`.

Spec references use section numbers from `docs/spec.md` (S12a means section 12a). Architecture references use `docs/architecture.md` (A4 means section 4).

**Global rules for every milestone:** tests tagged with spec sections, all property invariants in A9.3 that apply must pass, explainability entries for budget movements, lowercase UI copy, no budget math outside `internal/budget`, nothing destructive to the owner's data.

---

## M-1. Audit the existing build

Spec: none. Architecture: A1, A4.

- [ ] Inventory the repo: stack and versions, schema, API, auth, sync, UI, tests, build and run commands.
- [ ] Keep/adapt/rewrite decision per component. Target: keep UI components and sound auth and sync plumbing, rebuild budget logic.
- [ ] Data migration plan for existing owner data (dry run and backup first).
- [ ] List conflicts between what exists and the spec.
- [ ] Fill in the Commands section of `CLAUDE.md`.
- [ ] Write `docs/audit.md` and any needed ADRs.

Exit: `docs/audit.md` exists and is accurate. **Human gate:** owner reviews the keep/adapt/rewrite table and the migration plan.

---

## M0. Foundations

Architecture: A1, A4, A7, A8, A12.

- [ ] Repo layout per A4. Go module, `web/`, `docs/`, `testdata/`, `tools/`.
- [ ] CI: lint, vet, unit tests, migration check, frontend build.
- [ ] Docker Compose (`app`, `worker`, `postgres`), config by env or file, secrets from files or env, structured logging with redaction.
- [ ] Migration tooling with versioned SQL.
- [ ] Health endpoints (`/healthz`, `/readyz`) and a metrics skeleton.
- [ ] `.claude/settings.json` verified.
- [ ] `docs/config-defaults.md` started (a table of every tunable default).

Exit: `docker compose up` gives a login page, CI is green, secrets never appear in logs.

---

## M1. Core types, event store, import

Spec: S0, S3, S11, S17, S19.5. Architecture: A2, A3.

- [ ] `internal/money` (Cents, rounding, per-day split with remainder to the last day).
- [ ] `internal/civil` (calendar dates, home TZ, month assignment, DST, date-only bank dates, boundary attribution tolerance).
- [ ] Event store with undo and idempotency keys.
- [ ] Tables: accounts, raw transactions, overlays, categories and groups (stable ids, defaults from S1a), merchants.
- [ ] CSV, OFX, QFX and QIF import with saved column mappings, dry-run preview, per-batch undo.

Tests: S3 month-assignment table tests, invariants 4, 11 (idempotent import), 13.

Exit: import a CSV, undo and redo it, defaults categories seeded, all unit tests pass.

---

## M2. SimpleFIN client, ingest, data discovery

Spec: S15, S11 (dedupe), S19.2, S19.15, S19.24, S12a (pending). Architecture: A7, A10. Probe: `docs/discovery/PROBE.md`.

- [ ] Build `tools/probe` and run it (read-only, within budget). Produce `docs/discovery/report.md` and redacted fixtures.
- [ ] SimpleFIN protocol v2 client: `errlist`, `connections`, `balances-only`, per-account filter, `start-date`, `end-date`, **`pending=1` on every pull**.
- [ ] Window-size discovery from provider warnings, quota handling, token bucket.
- [ ] Ingest pipeline: dedupe by (account, id), fingerprint fallback, **id-rotation heal**, pending to posted reconcile, coverage and gap tracking, balance snapshots.
- [ ] Sync jobs: two scheduled pulls per day, throttled manual sync, catch-up, per-account status.
- [ ] Provider interface seam, with CSV/OFX and manual accounts as providers.

Tests: S15 fixtures (v1 and v2, errlist, missing optional fields, `posted = 0` pending, window warnings, id rotation), a test that every scheduled pull sends `pending=1`, invariant 11.

Exit: scheduled pulls are idempotent against fixtures and against the live account, the report documents real behavior, amendments filed for any divergence. **Human gate:** owner supplies `SIMPLEFIN_ACCESS_URL` via environment (see README). Without it, work against the public demo URL and report what is blocked.

---

## M3. Budget core

Spec: S0, S2, S3, S4 (plan and pot), S10 (spend_left, per_day). Architecture: A2.

- [ ] `internal/budget.ComputeMonth` per A2: plan, fixed, spend_base, absorption, pot, spend_pool, spend_left, per_day, envelope double-entry entries.
- [ ] `internal/recompute`: dirty set, early cutoff, snapshots with input hash and engine version, `recompute --all` tool.
- [ ] Plan overrides, learning phase, partial-coverage months.
- [ ] Explainability: "why is my number $X" reads from entries.

Tests: golden worked examples A, B, C, D from S2 (to the cent), invariants 1, 2, 3, 4, 5, 6, 10, 13.

Exit: a month computes correctly from a seeded ledger, recompute equals incremental recompute, scenarios A to D pass. Walking skeleton: a SimpleFIN pull produces a rendered safe-to-spend number.

---

## M4. Income, bills, subscriptions, recurrence

Spec: S4, S5, S6, S14. Architecture: A4 (`recurrence`).

- [ ] Schedules: fixed interval (any duration) and calendar rules, DST handling, anchoring, grace, effective-dating, split deposits.
- [ ] Detection from median gaps (threshold configurable), "mark as recurring" from any transaction.
- [ ] Income types, boundary attribution, deposit type, unreviewed-deposit holding.
- [ ] Bills (last actual amount), sinking envelopes (persistent), price change flags, missing-bill alerts, ended-bill handling, bills gap.
- [ ] Subscriptions (reserved up front), payment plans (finite occurrences).
- [ ] Forecasts: extra-paycheck months, next plan so far.

Tests: schedule tests for 7-hour, 13-day, 3-week, calendar and DST cases, invariant 8, worked example A.

Exit: any-interval schedules behave, three-paycheck months label correctly and flow to the pot.

---

## M5. Savings, overspend, rollover

Spec: S2 (pot), S7, S8, S9, S10, S17. Architecture: A2, A7.

- [ ] General savings (fixed or percent), goals (priority, borrowing, date or monthly), emergency fund (target, rebuild), discretionary (draw order, per-transaction override).
- [ ] Pot allocation (one-time and permanent), standing lines, conversion of past allocations.
- [ ] Overspend flow with all cover sources, auto-carry at rollover, uncovered balances.
- [ ] Rollover job (derived, idempotent), sweeps, defaults, catch-up on startup.
- [ ] Rollover transfer checklist.

Tests: worked examples E, C, D, invariants 1, 2, 3, 6, 14, rollover twice yields same state.

Exit: the whole money flow from income to overspend works end to end with undo.

---

## M6. Categorization and rules engine

Spec: S1a, S11, S18a. Architecture: A5.

- [ ] Normalization and merchants with aliases.
- [ ] Rules DSL, evaluator with traces, stages, specificity, preview, backtest, debugger, versioning, import and export.
- [ ] Learning: label events, scope prompt data, rule induction with probation, ambiguity, drift, exceptions, undo learning.
- [ ] Case memory, logistic regression, calibration, arbitration, triage ordering, surprise flags, explanations.
- [ ] Prequential evaluation recorder and nightly metrics, rule health, retro-review, optional audit mode.
- [ ] Seed rules and starter prior.

Tests: S18a edge cases 1 to 15, invariant 12, simulation reports for teach-once and first-pass accuracy, ML evaluation gates (A9.10).

Exit: accuracy gates reported against simulated life, rules debugger reproduces any decision.

---

## M7. Reimbursements

Spec: S12, S12a, S12b. Architecture: A5.

- [ ] Transfers pairing, card payments, refunds, links (tentative and confirmed), tentative links net immediately and revert on reject.
- [ ] Receivables, held payments (`awaiting expense`), grace window, late matching, hidden-pending hint.
- [ ] Counterparties, aliases, profiles, patterns, expense-side prediction, personalized aging.
- [ ] Candidate generation, pair scorer, global assignment, ambiguity gate, dual-threshold decisions, proven-pattern graduation and strikes.
- [ ] Matching debugger.

Tests: every acceptance test in S12a (including the reported bug as a named scenario) and S12b, invariants 7 and 14.

Exit: the reported bug cannot recur, auto-tentative precision gate met in simulation.

---

## M8. PWA

Spec: S16, S19.3, S19.20, S19.7. Architecture: A6. Design: `docs/design-brief.md`.

- [ ] Snapshot cache, offline queue with idempotency keys, sync protocol, conflict rules.
- [ ] Review UI (swipe), scope chips, correction flow, review by merchant.
- [ ] Hero screens, pot allocation, overspend cover, receivables, onboarding wizard.
- [ ] Push (VAPID), iOS Home Screen install guide, Badging API where supported.
- [ ] Accessibility (WCAG 2.2 AA), empty, loading, error and offline states everywhere.

Tests: Playwright e2e for the flows above, offline queue and reconnect, accessibility checks.

Exit: offline e2e passes, review at swipe speed. **Human gate:** owner reviews the UI visually and on a phone before moving on.

---

## M9. Reports, alerts, forecasting, search

Spec: S19.1 to S19.4, S19.6, S19.8, S19.10, S19.11, S19.20 (reports). Architecture: A4.

- [ ] Cash timeline (card payments due, shortfall alerts), plan forecast, what-if sandbox.
- [ ] Drift detection, assigned vs held, data health page, hidden pending estimate.
- [ ] Alert catalog with toggles, thresholds, channels, insights feed.
- [ ] Global search, saved views, bulk edit, command palette.
- [ ] Splits, tags, receipts and OCR, order itemization.
- [ ] Config preview, as-of view, month recap, year in review, subscription intelligence.

Tests: alert catalog tests, drift on simulated gaps, invariant 9.

Exit: simulated ledgers produce correct forecasts and alerts.

---

## M9a. Gamification: the grove

Spec: S19.25 (and S0 principle 10, S0a vocabulary). Architecture: A9.11. Design: `docs/design-brief.md` (play layer).

- [ ] `internal/play`: growth events with integrity rules (caps, diminishing returns, dwell threshold), levels, nightly `play-settle` job (idempotent, keyed by date, home time zone).
- [ ] Tended days and weeks, rest mode, earned freezes, decay, longest streak.
- [ ] Quests generated from real opportunities (insights feed), weekly board, silent rotation.
- [ ] Achievements with evidence snapshots, sticky across recompute, hidden-share check for clean books.
- [ ] Fluency view from the real metrics in S18a.
- [ ] Grove state derivation and procedural SVG rendering, season by calendar month, goal trees, emergency fund, discretionary, sinking beds, hedge, seeds, sprouts, clearing.
- [ ] Season recap (derived, shareable image with amounts hidden), notification options (off by default), intensity dial and per-component toggles, taper, reset and export.
- [ ] Copy lint in CI.

Tests: all 10 acceptance tests in S19.25, plus the A9.11 differential test (budget state identical with and without the play module).

Exit: gamification can be turned off and on without any change to budget numbers, awards persist across recompute, and the lint passes. **Human gate:** owner plays with it for a few days and adjusts the dial and defaults.

---

## M10. Extended systems

Spec: S19.9, S19.12 to S19.14, S19.21 to S19.24.

- [ ] Net worth, manual assets and liabilities, holdings (no double counting, prefer total cost basis).
- [ ] Multi-currency with FX rates and fee detection.
- [ ] Debt payoff planner, loans, trips, tax module (optional), soft targets, pending polish.

Exit: feature tests plus invariants 8 and 9 pass.

---

## M11. Platform

Spec: S19.17 to S19.19. Architecture: A6, A8.

- [ ] REST API with OpenAPI, scoped tokens, rate limits, webhooks with signing and replay.
- [ ] MCP server (read-only tools, scoped tokens).
- [ ] AI assistant (opt-in, off by default, aggregates only by default, audit log).
- [ ] Observability: metrics, logs, diagnostics bundle.
- [ ] Passkeys, TOTP, recovery codes, OIDC, sessions, panic lock, audit log.
- [ ] Encrypted export, scheduled export, restore.

Tests: API contract tests, restore-from-backup test, security tests (A9.9).

Exit: a full backup restores to an identical state hash.

---

## M12. Hardening

- [ ] Performance at ten years of data (A9.8).
- [ ] Security review and chaos tests (A9.9, A9.5).
- [ ] Upgrade path and migration tests.
- [ ] User docs for self-hosting (install, TLS, backup, restore, upgrade).

Exit: all suites green, release checklist complete.
