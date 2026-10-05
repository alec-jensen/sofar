# sofar: Architecture, Test Plan and Build Order

Companion to `docs/spec.md` (the logic spec). The spec says what sofar does. This document says how to build it. If they disagree, the spec wins for behavior and this document wins for technical structure.

sofar is a **single-user, self-hosted** app. There is no multi-tenancy, no billing and no telemetry.

---

## 1. Stack and deployment

- **Backend:** Go. One binary with two roles selectable by flag: `api` and `worker` (they can also run together in one process for small installs).
- **Database:** Postgres (the only required service).
- **Frontend:** React installable PWA, offline-first (IndexedDB), service worker.
- **Jobs:** a Postgres-backed queue (for example River). Jobs are enqueued in the same transaction as the data change that caused them. River's durable periodic jobs are listed as a paid feature, so do not rely on them: sofar owns its schedule and catch-up logic (every job is idempotent and keyed by date, see section 7).
- **Optional sidecar** (not required to run): OCR, embeddings and local models, spoken to over HTTP.
- **Deployment:** Docker Compose (`app`, `worker`, `postgres`). TLS terminates at a reverse proxy the user already runs. Config by environment variables or a config file. Secrets by files or environment. Versioned SQL migrations run on startup.
- **Backups:** the user's Postgres dumps, plus an in-app encrypted export that can fully restore (ledger, events, rules, model snapshots).

---

## 2. Core design: events plus derived state

1. **Raw bank data is immutable.** `raw_transactions` stores exactly what the provider returned, plus the full payload JSON.
2. **User decisions are events.** An append-only `events` table records every decision (a review, a link, an allocation, a config change). Undo is a new event that reverses an earlier one.
3. **Everything else is derived** by pure functions from raw data, overlays, config versions and events. No budget number is stored as truth.
4. **Snapshots are caches.** Per-month snapshots store derived state with an input hash so unchanged months are not recomputed.
5. **Determinism.** The same inputs and the same `engine_version` always produce the same output.

### The compute function

```go
// package budget (pure, no I/O)
type MonthInputs struct {
    Month        YearMonth
    HomeTZ       *time.Location
    Income       []IncomeItem       // counted income received in this month
    PrevIncome   Cents              // I(M-1), after overrides
    PlanOverride *Cents
    Config       ConfigAt           // bills, savings, goals, EF, standing lines at this month
    Events       []Event            // allocations, covers, spend_base edits, conversions
    Spending     []SpendItem        // posted, netted by links, split parts
    Today        civil.Date         // for per-day math
}

type MonthState struct {
    Plan, Fixed, SpendBase, Pot, Overcommitted Cents
    SpendPool, SpendLeft, PerDay               Cents
    Absorption                                 []AbsorptionEntry
    Envelopes                                  map[EnvelopeID]Cents
    Entries                                    []EnvelopeEntry // double-entry movements for explainability
    EngineVersion                              int
    InputHash                                  [32]byte
}

func ComputeMonth(prev MonthState, in MonthInputs) MonthState
```

`ComputeMonth` is the single place budget math lives. It must be pure and heavily tested. Everything else (sync, API, UI) calls it and never reimplements it.

### Recompute strategy

- Months form a chain: month M depends on M-1 (plan, spend_base, envelope balances). A change in an old month cascades forward.
- Keep a **dirty set** of months. When a transaction, link, config version or event changes, mark its month (and the month it feeds, via the plan) dirty.
- `Recompute(fromMonth)` walks forward from the earliest dirty month calling `ComputeMonth`, with **early cutoff**: if a month's recomputed state equals its stored snapshot and the next month's own inputs are unchanged, stop.
- The current month is recomputed on every relevant change. Expect small constants (months in the thousands at most for decades of history).
- A `recompute --all` tool exists for engine upgrades. Snapshots record `engine_version`.

### Envelope ledger

Every virtual envelope movement is a double-entry pair (debit one envelope, credit another) stored as `Entries` in the snapshot. Envelopes: plan, bills reserve, sinking (one per bill), general savings, goals (one each), emergency fund, discretionary, spend pool, pot, next-month borrow, uncovered. This makes the explainability screen ("why is my number $X") a straight read of the entries, and makes conservation testable.

---

## 3. Data model (Postgres)

All money is `bigint` cents with an ISO currency code. Timestamps are `timestamptz`. Calendar dates are `date`.

**Accounts and sync**
- `accounts` (id, provider, provider_account_id, name, institution, currency, kind[budget|tracked|balance_only|liability], active, opened_at)
- `provider_connections` (id, provider, access_url_enc, status, last_ok_at, error_json)
- `sync_runs` (id, started_at, finished_at, window_start, window_end, requested_pending, result_json, errlist_json)
- `account_coverage` (account_id, earliest_date, gaps_json)
- `balance_snapshots` (account_id, balance, available_balance, balance_date)
- `raw_transactions` (id, account_id, provider_txn_id, posted_at, transacted_at, amount, description, payee, memo, mcc, pending, payload jsonb, first_seen_at, last_seen_at) with unique (account_id, provider_txn_id)
- `txn_id_aliases` (account_id, old_id, new_id) for id-rotation healing

**Overlay and categories**
- `transaction_overlays` (raw_txn_id, category_id, month_override, notes, tags[], hidden, from_discretionary, reviewed_at, type, split parts in `txn_splits`)
- `categories`, `category_groups` (stable ids, bucket, parent group)
- `merchants`, `merchant_aliases`
- `suggestions` (txn_id, category_id, source, confidence, reason, alternatives jsonb, model_version)

**Rules and learning**
- `rules`, `rule_versions` (layer, stage, conditions jsonb, actions jsonb, priority, enabled, provenance, stats)
- `label_events` (txn_id, suggested, source, confidence, final, scope, weight, session_id, at)
- `exceptions` (txn_id, rule_or_pattern)
- `model_snapshots` (kind, version, weights bytea, trained_through, metrics jsonb)

**Budget configuration (effective-dated)**
- `config_versions` (id, effective_month, kind, payload jsonb) for bills, subscriptions, savings, goals, EF, standing lines, home TZ
- `schedules` (id, owner_type, owner_id, kind[interval|calendar], params jsonb, anchor, tolerance, effective_from)
- `bills`, `subscriptions`, `sinking_envelopes`, `payment_plans`, `goals`, `emergency_fund`, `standing_lines`

**Events and derived**
- `events` (id bigserial, at, actor, kind, subject_type, subject_id, payload jsonb, reverses bigint null, idempotency_key unique)
- `month_snapshots` (month, engine_version, input_hash, state jsonb, computed_at)

**Reimbursements**
- `counterparties`, `counterparty_aliases`
- `links` (id, payment_txn_id, expense_txn_id|split_id, receivable_id, amount, state[tentative|confirmed], created_by[user|auto_receivable|auto_pattern])
- `receivables` (id, counterparty_id, amount, expense_ref, expected_by, status, netted_now)
- `held_payments` (txn_id, state, grace_until, hints jsonb)
- `reimbursement_patterns` (id, counterparty_id, conditions jsonb, ratio, lag_window, support, strikes, state)
- `link_events`, `pair_feature_snapshots`

**Gamification**
- `play_settings`, `play_xp_events` (derived, deterministic), `play_awards` (with evidence snapshot, sticky), `play_streaks` (weeks, freezes ledger, rest periods), `play_quests`, `grove_state` (derived cache)

**Platform**
- `alerts`, `notification_subscriptions` (web push), `api_tokens`, `webhook_endpoints`, `webhook_deliveries`, `audit_log`, `sessions`, `credentials` (passkeys, TOTP), `fx_rates`, `receipts`, `net_worth_snapshots`, `holdings`

---

## 4. Module layout (Go)

```
cmd/sofar                 main (api and/or worker)
internal/money            Cents, currency, rounding, per-day split
internal/civil            calendar dates, home TZ, month assignment, DST
internal/budget           ComputeMonth, envelopes, absorption (PURE)
internal/recompute        dirty set, early cutoff, snapshot store
internal/events           event store, undo, idempotency
internal/recurrence       schedules, detection, mark-as-recurring, forecasts
internal/provider         interface + simplefin v2 client + csv/ofx + manual
internal/ingest           dedupe, id-rotation heal, pending reconcile, coverage
internal/normalize        merchant normalization, aliases
internal/rules            DSL parse/eval, stages, specificity, preview, backtest, debugger
internal/learn            case memory, logistic regression, calibration, induction
internal/categorize       arbitration, triage, label events, evaluation recorder
internal/reimburse        receivables, candidate gen, pair scoring, assignment, patterns
internal/alerts           catalog, thresholds, channels
internal/forecast         cash timeline, plan forecast, what-if
internal/reports          queries, saved reports
internal/play             gamification: growth, streaks, quests, awards, grove state (reads only, never writes budget tables)
internal/api              REST + OpenAPI, SSE, webhooks
internal/auth             sessions, passkeys, TOTP, tokens, OIDC
internal/jobs             queue workers, schedules, catch-up
internal/notify           web push, email, ntfy, webhooks
internal/store            migrations, repositories
web/                      React PWA
docs/                     specs, ADRs
testdata/                 fixtures, scenarios, golden files
tools/probe               SimpleFIN discovery probe (section 10)
```

Rules: `budget`, `money`, `civil` have no I/O and no dependencies on other internal packages. Every other package depends inward on them, never the reverse.

---

## 5. Categorization and matching engine design

- **Rules DSL** is JSON (stored in `rules.conditions` and `rules.actions`) with a typed Go evaluator. Conditions are an AND/OR/NOT tree over fields and operators from the spec (section 18a). The evaluator returns a **trace** (every rule evaluated, why it matched or not) so the debugger is free.
- **Specificity score** per rule is computed from its conditions (more and tighter predicates score higher). Staged execution: pre, default, post, then learned layer, then statistical layers.
- **Preview and backtest** run the rule against history in SQL where possible, otherwise streamed in Go, and return a diff without writing.
- **Learning pieces (Go-native):**
  - case memory: nearest-neighbor over a feature vector (normalized merchant id, amount features, account, context)
  - logistic regression over hashed character n-gram and word features, updated online with SGD and refit in a background job
  - calibration: Beta posteriors for rules, Platt or isotonic scaling for the model, refreshed from the evaluation log
  - rule induction from consistent label clusters, with probation
- **Prequential evaluation:** every suggestion is persisted before the label arrives. A job aggregates accuracy by source, confidence bucket and category.
- **Reimbursement matcher:** candidate generation in SQL (open receivables, expenses with remaining amount in the lookback window), pair features in Go, a calibrated scorer, and min-cost assignment (the Hungarian algorithm is enough at these sizes). A state machine governs links, receivables and held payments. A trigger on every new or newly posted expense runs **late matching** against held payments.
- **Snapshots:** model weights are versioned in `model_snapshots` so a past suggestion is reproducible.

---

## 6. API and client

### API

- REST with OpenAPI, versioned under `/api/v1`. JSON. Cursor pagination.
- **Auth:** session cookies (HTTP-only, SameSite) with passkeys first, TOTP and recovery codes second, optional OIDC. API tokens are scoped (read, write, per resource) and hashed at rest. Step-up auth for export, token creation and data deletion.
- **Idempotency:** every mutating request accepts an `Idempotency-Key`, stored on the resulting event.
- **Live updates:** server-sent events for review queue, snapshot changes and alerts.
- **Webhooks:** HMAC-signed, retried with backoff, delivery log and replay.
- **MCP server** exposing read-only tools (writes gated) with scoped tokens, sharing the same service layer as the REST API.

### Client (PWA)

- **Authoritative data stays on the server.** The client caches derived snapshots (`MonthState`) with `engine_version` and `computed_at`, plus the review queue, in IndexedDB.
- **Offline writes** go into a queue of intents with idempotency keys. The UI applies an optimistic local change only for review actions (confirm, recategorize). All budget numbers update only from server snapshots.
- **Sync protocol:** `GET /sync/changes?cursor=` returns changed entities and fresh snapshots. The client pushes its queue on reconnect and then pulls.
- **Conflicts:** last-write-wins per field, except explicit review decisions always win over sync-sourced updates.
- **Push:** Web Push with VAPID. On iOS it works only for an installed Home Screen app on iOS 16.4 or later, and there is no background sync, so the app refreshes on open. The Badging API sets the review count where supported.
- **Review UI** follows the swipe pattern (right to confirm, left to recategorize) with the scope chips from the spec.

---

## 7. Jobs and scheduling

| Job | Schedule | Notes |
|---|---|---|
| `sync` | about 06:00 and 18:00 home time, plus throttled manual | requests `pending=1`, window size discovered from provider warnings, per-account status |
| `rollover` | 00:05 home time on the 1st | derived and idempotent, keyed by month |
| `recompute` | on demand (dirty set) | early cutoff |
| `retrain` | after a batch of labels, plus nightly | model snapshot written |
| `evaluate` | nightly | prequential metrics and rule health |
| `alerts` | after sync and hourly | catalog evaluation |
| `fx`, `prices` | daily | cached |
| `held-payment sweep` | hourly | grace window expiry, late match |
| `cleanup`, `export` | nightly | scheduled encrypted export |

- **Catch-up:** on startup, every job type computes which keyed runs are missing (for example rollovers since the last completed month) and enqueues them. All jobs are idempotent by key.
- **Singleton execution:** a Postgres advisory lock ensures only one worker runs scheduled jobs.
- **Sync rate discipline:** a token bucket enforces the provider's daily request cap with headroom, and a quota warning pauses sync for 24 hours.

---

## 8. Security

- Treated as an internet-reachable service holding bank data.
- Secrets (the SimpleFIN access URL, API keys) are encrypted with AES-GCM using a key from a file or environment, and never logged or returned by the API.
- Passkeys (for example via a maintained WebAuthn library), TOTP (a standard OTP library), recovery codes, session list and revocation, login history, a panic lock.
- Rate limiting and lockout with backoff, strict CSP, CSRF protection, dependency scanning in CI.
- An append-only `audit_log`. Redacted diagnostics bundle for support.
- Logs redact amounts, descriptions and account numbers by default.
- The optional AI assistant is off by default, sees aggregates only unless widened, and logs every prompt and tool call.

---

## 9. Test plan

Every test carries a spec tag in its name, for example `S12a_T1_reported_bug`, so coverage can be traced back to the spec.

### 9.1 Unit tests (pure packages)

- `money`: rounding, per-day split with remainder to the last day, negative values.
- `civil`: month assignment across time zones, DST, date-only bank dates (the 1st at midnight UTC must stay in its month), boundary attribution tolerance.
- `budget`: table-driven tests for every rule in sections 2 to 10 of the spec.

### 9.2 Golden scenarios

- Worked examples A to F from the spec (three-paycheck month, temporary spike, permanent raise and later loss, drop and recovery, overspend and cover, reimbursement across months), asserted to the cent, month by month.
- Every acceptance test in sections 12a and 12b as an end-to-end scenario from raw transactions to final state.
- The reported bug as a named scenario: payback posts, McDonald's pending and invisible, a larger unrelated expense exists, expect no link, a held payment, and a correct late match when the charge posts.

### 9.3 Property-based tests (invariants)

Generate random ledgers and event sequences and assert:

1. **Conservation:** `plan = fixed + spend_base + pot - overcommitted`, with the pot and the overcommitment never both positive.
2. The pot is never negative. `spend_base` never rises without a user event.
3. Every envelope movement has equal debit and credit.
4. `undo` followed by `redo` restores identical state.
5. **Recompute from scratch equals incremental recompute** (state hashes match).
6. Rollover run twice yields the same state.
7. Linked reimbursements never exceed the expense's remaining amount unless overridden, and netting never creates income.
8. A transaction contributes to `I(M)` at most once and never when it is a transfer, link, or neutral type.
9. Splits sum to the total, and no transaction is counted in two buckets.
10. `per_day * days_left + remainder = spend_left`.
11. Applying the same sync pull twice yields an identical ledger. Id-rotation healing preserves overlays and labels.
12. Learning uses only user labels (no label event with a system source).
13. Same inputs and engine version give the same output.
14. Rejecting a tentative link reverts the math exactly to its prior state.

### 9.4 Simulation harness

A seeded synthetic life generator produces years of data: salary at any interval, irregular side income, monthly and annual bills, subscriptions with price hikes, merchants with ambiguous categories, P2P paybacks with realistic lag, pending-then-posted card purchases with tip changes, duplicate and rotated ids, missing days, and bank outages. Runs assert the invariants above and measure categorization and matching quality (teach-once accuracy, first-pass accuracy, auto-tentative precision).

### 9.5 Provider and sync tests

- Recorded fixtures for protocol v1 and v2 shapes, `errlist` errors, `pending=1` responses, missing optional fields (payee, memo, MCC, `transacted_at`), `posted = 0` for pending, window-size warnings, quota warnings, id rotation.
- A test that **every scheduled pull sends `pending=1`**.
- Fault injection: timeouts, partial account failures, token revocation, clock skew.

### 9.6 Replay and migration tests

- Replaying the event log reproduces exactly the same state.
- Migration tests: old schema to new with data preserved. An engine-version upgrade followed by `recompute --all` yields explainable diffs only.

### 9.7 UI and end-to-end (Playwright)

- Review swipe flows, scope chips, overspend cover, pot allocation, onboarding wizard, receivable creation, offline queue and reconnect, push permission flow on a simulated installed PWA.
- Accessibility checks (WCAG 2.2 AA) on every screen.

### 9.8 Performance and scale

- Ten years of transactions (hundreds of thousands) with the hero screen under a target latency, recompute of a changed old month with early cutoff, rule backtest over full history, matcher assignment on hundreds of open items.

### 9.9 Security tests

Auth bypass attempts, token scope enforcement, secret redaction in logs and responses, CSRF, rate limits, SQL injection and XSS in descriptions and notes (bank strings are untrusted input).

### 9.10 ML evaluation gates

An offline harness replays history prequentially and reports teach-once accuracy, first-pass accuracy by day, calibration curves and auto-tentative precision. The targets in the spec (95% teach-once, 90% by day 30, 95% by day 90, 99% auto-tentative precision) are release gates for the learning features.

---

### 9.11 Gamification tests

- **Isolation:** running the budget engine with and without `internal/play` yields identical `MonthState` hashes (a differential test in CI). `internal/play` has no write access to budget tables (enforced by a repository interface and a test).
- **Determinism and stickiness:** replaying events reproduces the same growth. A recompute never revokes an award.
- **Integrity:** rubber-stamp confirms earn near zero, daily caps and diminishing returns hold, undo before settle earns nothing, hiding transactions withholds "clean books".
- **Streaks:** weekly tended logic, freezes, rest mode and decay, in home time across DST and travel.
- **Copy lint:** every gamification string passes the forbidden-word and no-exclamation check.
- **UI:** off, quiet and full levels, celebration intensity 0, reduced motion, screen-reader description of the grove.

---

## 10. Data-discovery probe (run first)

Full instructions live in `docs/discovery/PROBE.md`. In short: a small read-only tool (`tools/probe`) run against the owner's real SimpleFIN account. Its output decides several design details, and it must respect the daily request cap (use per-account requests and `balances-only` where possible; a public demo access URL is also available for development without touching real data).

Measure per account and institution:

1. Which fields are present on transactions: payee, memo, MCC, `transacted_at`, `extra`.
2. Whether `pending=1` returns pending items, and how pending rows look (`posted = 0`?).
3. How far back history goes, and the working window size per request (watch for window-size warnings).
4. Whether transaction ids are stable across pulls (repeat a pull and diff), and whether any are re-issued.
5. The meaning of `available-balance` vs `balance` for each account type (checking, savings, card).
6. Whether holdings appear for investment accounts and what fields they carry.
7. Typical lag between a card purchase and its appearance and posting.
8. How P2P payments (Zelle, Venmo) appear: description, payee and memo content.
9. The `errlist` codes returned in normal operation.
10. When the Bridge refreshes each account during the day.

The probe writes a redacted JSON report (descriptions hashed or truncated, amounts bucketed) that can be shared or committed as fixtures.

---

## 11. Build order (dependency order, nothing cut)

Each milestone ends with its tests green and a working vertical slice.

| # | Milestone | Depends on | Exit criteria |
|---|---|---|---|
| M0 | Repo, CI, Docker Compose, migrations, config, logging, auth plumbing (reuse existing) | | `docker compose up` yields a login page; CI runs lint and tests |
| M1 | `money`, `civil`, event store, ledger tables, overlays, CSV/OFX import | M0 | import a CSV, undo and redo events, unit tests pass |
| M2 | SimpleFIN client v2, `pending=1`, window discovery, dedupe, id-rotation heal, coverage, sync jobs, probe | M1 | fixtures green, probe report produced, scheduled pulls idempotent |
| M3 | `budget.ComputeMonth`, recompute with early cutoff, time and month assignment | M1 | golden scenarios A to D and invariants 1 to 6, 13 pass |
| M4 | Income, bills, subscriptions, sinking, recurrence engine, mark-as-recurring, forecasts | M3 | schedule tests for any interval and calendar rules; detection tests |
| M5 | Savings, goals, emergency fund, discretionary, covers and overspend, rollover jobs | M3, M4 | scenarios E and the overspend flow; rollover idempotent |
| M6 | Normalization, rules DSL, learned rules, case memory, model, calibration, triage, evaluation recorder | M2 | rules tests; simulation reports teach-once and first-pass accuracy |
| M7 | Transfers, refunds, links, receivables, held payments, matcher and pattern learning | M2, M6 | every 12a and 12b acceptance test; invariants 7 and 14 |
| M8 | PWA: snapshots cache, offline queue, sync protocol, push, review UI, hero screens, onboarding wizard | M3 to M7 | offline e2e passes; review at swipe speed |
| M9 | Reports, alerts, cash timeline and drift detection, search and bulk edit, splits and tags | M5, M7 | alert catalog tests; drift detection on simulated gaps |
| M9a | Gamification: the grove (growth, streaks, quests, awards, season recap, intensity dial) | M6, M7, M8, M9 | invariants for play (A9.11) pass, copy lint passes, with and without play yields identical budget state |
| M10 | Net worth and investments, multi-currency, debt planner, receipts and OCR, trips, tax module, soft targets, pending polish | M9 | feature tests plus invariants 8 and 9 |
| M11 | API tokens, webhooks, MCP server, AI assistant, observability, passkeys and OIDC, exports and encrypted backups | M8 | API contract tests; restore-from-backup test |
| M12 | Hardening: performance, security review, chaos tests, docs, upgrade path | all | scale and security suites green |

**Walking skeleton first:** a thin path from a SimpleFIN pull to a rendered safe-to-spend number (M0 to M3 with minimal UI) so every later piece plugs into a working system.

---

## 12. Conventions for the coding agent

- **Spec traceability:** each function and test cites its spec section (`// spec: S12a`). A CI check fails if a spec section has no tagged test.
- **No budget math outside `internal/budget`.** No I/O inside it.
- **ADRs:** every non-obvious technical decision is a short markdown record in `docs/adr/`.
- **Definition of done:** tests (unit, scenario, property where relevant), migration included, OpenAPI updated, explainability entries emitted for any new budget movement, UI copy in the lowercase vocabulary, accessibility checked.
- **Bank strings are untrusted input** everywhere (rendering, rules, regexes, logs).
- **Feature flags** only for unfinished work, never for permanent behavior forks.

---

## 13. Open technical items

None blocking. The probe in section 10 may change details of sync and pending handling. Everything else is a normal engineering choice recorded as an ADR when made.
