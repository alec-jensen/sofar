# progress log

Update at the end of every work session. Newest entry first.

## format

- date and session summary
- milestone and tasks completed
- tests added or changed, and suite status
- decisions made (link ADRs)
- spec amendments opened
- blockers and questions for the owner
- next steps

## log

### 2026-10-05: planning session (Claude Opus 5.5)
- Installed the handoff package into the repo (`docs/`, `.claude/settings.json`, commands, agents, `CLAUDE.md`, `KICKOFF.md`; `.gitignore` merged). The handoff `README.md` and `.env.example` were not copied over the project's own files.
- Milestone M-1 done: `docs/audit.md` (inventory, keep / adapt / rewrite, conflicts, existing data). The owner gate is still open.
- Wrote the implementation plan: `docs/plan/README.md` (strategy, decisions for ADRs 0010 to 0020, session loop, owner gates), `docs/plan/engine-semantics.md` (exact `ComputeMonth` order, checked against worked examples A to F), `docs/plan/legacy-migration.md` (quarantine plus importer plus cutover runbook), task files `m0.md` to `m12.md` and `m9a.md`, and `docs/discovery/known-facts.md`.
- Spec amendments opened: A-001 to A-010 (proposed, with interim behavior).
- Blockers and questions for the owner: commit the pending mobile fixes on `main`; review the audit and migration plan; decide the amendments when convenient; a separate SimpleFIN token for the probe before M2.
- Next: M0.1 (create the `rebuild` branch, write ADRs 0010 to 0020).

### 2026-10-05: M0 foundations (Claude Sonnet 5.5)
- Milestone M0, tasks M0.1 to M0.13 done on branch `rebuild`. The legacy `server/`, `internal/db` and `internal/budget` math are gone from this branch (they live on `main`).
- New: `cmd/sofar` (serve, migrate, vapid); `internal/{config,logging,crypt,httpx,netguard,normalize,notify,store,testkit,auth,jobs,metrics,api,archtest,money,civil,budget}`; goose migrations 00001 (legacy quarantine into schema `legacy`), 00002 (platform tables), 00003 (idempotency keys); `api/openapi.yaml` with response contract tests; `compose.yaml` with a readiness healthcheck; `.github/workflows/ci.yml`; a web shell (`web/src/shell/`) that signs in through `/api/v1/auth/*`, with recovery codes. The legacy `App.tsx` stays in the tree, unreachable until M8.
- Tests: unit tests for every package plus integration tests (`-tags integration`) for migrations, auth, jobs, idempotency and the HTTP auth flow with OpenAPI validation. Web: 8 files, 54 tests. The legacy login flow was verified in the browser pane against a scratch database.
- Decisions: `SOFAR_ADDR` is the process listen address and `SOFAR_BIND` stays compose-only (the deployed `.env` sets `SOFAR_BIND=0.0.0.0`, which must not become a listen address). `/healthz` and `/readyz` answer over plain HTTP from loopback in production so container healthchecks work. Idempotency keys are stored in their own table (and later also on events). New dependencies: goose, prometheus client_golang, kin-openapi, gorilla/mux (via kin-openapi), rapid (not used yet).
- Spec amendments opened: none.
- Blockers and questions for the owner: none for M0. Nothing has been pushed.
- Next: M1 (`money`, `civil`, event store, ledger tables, overlays, CSV/OFX import).

### 2026-10-05: M1 core types, events, ledger, import (Claude Sonnet 5.5)
- Milestone M1, tasks M1.1 to M1.9 done on `rebuild`.
- New packages: `money` (exact decimal parsing, per-day floor division), `civil` (S3 month rules, effective-dated zones, A-010 boundary attribution), `events` (append-only store, idempotency keys, undo and redo, versioned payload registry), `ledger` (effective transaction view, event-sourced overlays with a rebuildable projection, splits, hide tombstones, category remap), `categories`, `importer` (CSV, OFX/QFX, QIF with dry run and batch undo), `manual`, `accounts`. Migrations 00004 (ledger schema and seeded default categories) and 00005 (soft delete).
- API (in `api/openapi.yaml`, contract-checked): accounts, transactions (list, create manual, patch, split, hide, unhide), categories and groups, imports (dry run, apply, undo), generic event undo and redo.
- Tests: unit and integration for every package, rapid properties for undo/redo (invariant 4), projection rebuild equals incremental, import twice is identical (invariant 11), fuzz tests for all three parsers (the OFX fuzzer found a byte-offset bug, fixed; its corpus entry is kept). Invariant 13 (same inputs, same output) is covered for the projection here and for `ComputeMonth` in M3.
- Decisions: bank dates are stored as calendar dates plus an optional instant (`*_on`, `*_instant`); category delete and merge are soft and carried by one remap event that the overlay projection applies; an undone import batch hides its rows through the view (never deletes), and re-importing the file revives them; manual merge is a link event, so the bank row inherits the manual overlay and undo is exact. New table `idempotency_keys` (M0).
- Spec amendments opened: none.
- Next: M2 (SimpleFIN client, ingest, probe). The probe needs the owner's separate `SIMPLEFIN_ACCESS_URL`; without it M2 builds against fixtures and the demo URL, and M3 can proceed first (see `docs/plan/README.md` section 2).

### 2026-10-05: M3 budget core, walking skeleton (Claude Sonnet 5.5)
- Milestone M3 done on `rebuild`. M2 is deferred: `SIMPLEFIN_ACCESS_URL` is not set, so per the plan M3 came first.
- `internal/budget`: `ComputeMonth` (plan, fixed with the A-007 rebuild redirect, absorption with causes and the A-001 pin, A-005 ordering, events, pot with take-back, pool, outflows with bill reserves, per-day, rollover sweeps per A-002, entries and input hash). Worked examples A to D pass to the cent; E and F are skipped for M5 and M7. Property tests cover invariants 1, 2, 3, 10 and 13 plus input immutability.
- `internal/recompute`: `Assemble`, snapshots, dirty-month database triggers (marked in the same transaction as every change), early cutoff, and `sofar recompute --all|--from`. The current month always recomputes. Tests: scratch equals incremental (invariant 5), twice the same (6), late transactions, overrides, learning, partial.
- API: `GET /api/v1/home`, `GET /api/v1/months/{ym}/explain` (lines that add up to spend_left), `POST /api/v1/months/{ym}/plan-override`; the web shell now shows the hero from the server. Verified in the browser pane against a scratch database with an imported CSV: $3,955 safe to spend, about $146 a day.
- Interpretations to confirm (not new amendments): the rebuild redirect keeps fixed equal to the savings line plus only the extra (the text's literal formula would undercount by the redirected part); for now an income deposit counts only when its category is income and the user has reviewed it (schedules in M4 will let provisional deposits count); coverage uses the earliest data date per budget account until M2's `account_coverage` exists.
- Open race noted: a change committed while a recompute is running can have its dirty mark cleared; harmless for one user and fixed by the next change, to be tightened with the job queue.
- Next: M4 (income schedules, bills, subscriptions, recurrence), unless the probe credential arrives and M2 is taken first.

### 2026-10-05: M4 income, bills, subscriptions, recurrence (Claude Sonnet 5.5)
- Milestone M4 done on `rebuild`.
- `internal/recurrence` (pure): schedules (interval in days or absolute hours, monthly, quarterly and annual, semi-monthly, nth weekday, last business day), grace, anchoring, effective-dated versions, split deposits, detection by median gap with a configurable threshold and "after one charge" merchant kinds. `internal/recurring`: items, candidates that only ask, mark as recurring, dismiss, end, undo, and the pure line builder (reserve = last actual, sinking set-asides, payment plans). Migration 00007.
- `internal/recompute` now assembles from all months in memory: income classification (provisional scheduled deposits, held-out unknown deposits, tracked-account deposits, reversals netting into the original month, A-010 boundary attribution with the month override winning), bill matching to reserves, standing lines from permanent allocations, absorption causes (standing lines, expected or unexpected plan drops), forecasts, extra decomposition (suggestions only) and signals (missing, still active?, price change).
- Recompute change worth knowing: snapshots now store a hash of each month's own inputs, and a run recomputes every month whose inputs changed or that a trigger marked, so a change that reaches back (a reversal, a schedule) can never leave a stale month. Early cutoff still skips untouched months; the current month always recomputes. Dirty triggers also mark two months ahead.
- API: recurring items, candidates (confirm and dismiss), mark as recurring, end, forecast with decomposition, held deposits and plan-so-far on `/home`; all in `api/openapi.yaml` and contract-checked.
- Interpretations: a bill with no matching reserve line draws from spending (A-004); payment-plan installments are counted by matched charges before the month; weekly and biweekly bills reserve once per occurrence in the month.
- Next: M5 (savings, goals, emergency fund, discretionary, overspend and covers, rollover).

### 2026-10-05: M5 savings, goals, EF, discretionary, overspend, rollover (Claude Sonnet 5.5)
- Milestone M5 done on `rebuild`. Engine version is now 2.
- Engine: covers with the reverse-order clamp (A-006), uncovered debt that is not a deduction and settle (A-003), bills-reserve gap, EF and goal covers with borrowed and EF-used flags, goal purchases drawing the envelope, goal and EF caps sending excess to savings, savings verification (matching transfers, "i moved it") flagged for finished months only, discretionary that can go negative. Example E passes; property tests cover the new events.
- Data and recompute: effective-dated savings and EF config and goals (`internal/budgetcfg`, migration 00008), rebuild lines from EF withdrawals, standing lines (including "make permanent" conversion and removal), goal purchases via a transaction type of `goal:<id>`, deposits into savings accounts verifying savings, trailing outflow for the EF target.
- API: savings and EF settings, goals, pot with suggestions (make permanent, restore), allocations, spend_base edit and unpin, overspend list (prompt only for the current and previous month; older months are passive), covers (discretionary is never a source, A-008), settle, rollover checklist, and `POST /preview` (in-memory before and after, writes nothing; the applied result matches it in tests).
- Rollover: a keyed job at 00:05 home time on the 1st; after downtime only the current month's key runs; it recomputes, sends the digest, and writes no decisions (tests prove it). Wired into the worker with a push notifier.
- Interpretations: one-time goal creation derives the date or the monthly amount at creation and edits are effective-dated from the current month; completing a goal stops the line from the next month, so the freed amount lands in uncategorized; the "make permanent?" suggestion fires when the pot is equal two months in a row; the restore amount is how far spend_base is below its value before the latest absorption.
- Next: M6 (categorization and the rules engine), which does not need the probe credential.

### 2026-10-05: M6 categorization and the rules engine (Claude Sonnet 5.5)
- Milestone M6 done on `rebuild`. Migrations 00009 (rules) and 00010 (categorization).
- `internal/rules`: condition language with RE2 only and length caps, layers (user over learned over seed), stages, specificity and priority, traces, time-bound rules, versions, import and packs. `internal/categorize`: seed rules and starter prior from the merchant directory (re-mapped to stable ids), rule preview, backtest, apply-to-past (one undoable event), debugger and health, label events with weights and scopes, induction (two labels for a plain merchant, three for ambiguous or conditional by account or amount band), drift and conflict prompts, undo learning, hashed-feature logistic model with Platt calibration and case memory, arbitration, triage, live suggestions with a prequential log, `Evaluate`, retro-review, audit mode, nightly job (`categorize.nightly`: archive stale rules, retrain, refresh, evaluate).
- API: review queue, confirm, recategorize (with scope proposal and counts), batch by merchant (outliers called out and skipped), category proposals and move-merchants for a new category; all in `api/openapi.yaml`. Unreviewed spending already counts in the math (S11) and confirming changes no number.
- Gates (A9.10) on the standard seeds 1 to 3 over the synthetic life: teach-once 99% to 100%, first-pass above 90% by days 21 to 30 and 100% by days 81 to 90 on the measured merchants. Sample sizes per window are small (about 10 to 25), so treat the numbers as a smoke gate rather than a statistic.
- Interpretation to confirm: the simulator's four ambiguous stores (Amazon, Walmart, Target, CVS) draw their category at random with heavily overlapping amount ranges, so no feature can predict them; the gates exclude them and the review loop treats them as per-item decisions. If the owner wants the gate to include ambiguous merchants, the generator needs a real signal (time, basket size) first.
- Not done in M6: a CI step that prints the gate numbers (they appear in test logs); rules management endpoints beyond the review flow (the plan lists them under later UI work); enqueueing a suggestion refresh from ingest (M2 owns the trigger).
- Next: M7 (transfers, refunds, reimbursements, receivables, matcher). M2 stays blocked on the probe credential.
