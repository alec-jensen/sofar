# audit of the existing build (M-1)

Written 2026-10-05 against commit `24abd9e` plus the uncommitted mobile fixes in the working tree. This is the M-1 deliverable from `docs/plan/milestones.md`. It replaces "inventory the repo" for the implementer: trust this document, and re-check only the files a task touches.

**Human gate (still open):** the owner reviews the keep / adapt / rewrite table (section 9) and the migration plan (`docs/plan/legacy-migration.md`).

---

## 1. summary

The current app works and is deployed by the owner, but its budget model is not the spec's. It uses three buckets (expenses, spending, savings), a three-month income average and a "safe to spend" figure derived from it. There are no events, no overlays, no envelopes, no pot and no plan from last month's income. Its client also duplicates the budget math in TypeScript, which ADR-0003 forbids.

What is worth keeping is solid and recently hardened by a security audit:
- auth (bcrypt, server-side sessions, TOTP, rate limiting by caller IP, trusted-proxy handling)
- the SimpleFIN v2 client (token claim, encrypted access URL, `pending=1`, 45-day windows, connection errors)
- outbound-request hardening (SSRF guard)
- web push
- the PWA shell, UI components, gestures and motion
- a 424-merchant directory

The plan is: keep those, rebuild everything else from the spec on a `rebuild` branch, and cut over with a one-time importer once the new PWA reaches parity (M8).

## 2. stack and versions

| part | today |
|---|---|
| go | 1.26 (module `github.com/alec-jensen/sofar`) |
| go deps | `jackc/pgx/v5` 5.11.0 (via `database/sql` + `pgx/stdlib`), `rs/zerolog`, `pquerna/otp`, `x/crypto/bcrypt`, `SherClockHolmes/webpush-go`, `fergusstrange/embedded-postgres` (tests only) |
| postgres | 17 (`postgres:17-alpine` in compose) |
| frontend | react 19, typescript 5.8, vite 6, vitest 4, `idb` 8, `lucide-react`, `qrcode` |
| deploy | `Dockerfile` (node 24 build stage, go 1.26 build stage, alpine runtime, non-root), `compose.yaml` (`db`, `app`), `Caddyfile.example`; the owner actually runs nginx on another machine over NetBird |
| ci | none |

## 3. layout today

```
server/            package main: http routes, auth, simplefin sync, suggestions, push, netguard, state + actions
internal/budget/   old three-bucket Calculate(), merchant directory (merchants.json + merchants.go)
internal/db/       Open() + embedded idempotent schema.sql (no versioned migrations)
src/               react app (single 3,000-line App.tsx plus page components and hooks)
public/            sw.js, manifest, icons
```

Target layout is `docs/architecture.md` A4 (`cmd/sofar`, `internal/*`, `web/`). M0 moves things there.

## 4. database (legacy schema, `internal/db/schema.sql`)

The schema is idempotent SQL run at every startup, with `ALTER ... IF NOT EXISTS` patches appended over time. There is no migration history.

| table | purpose | spec fit |
|---|---|---|
| `users` | single user, bcrypt hash, TOTP secret/pending/last step | keep the idea; add recovery codes, passkeys later |
| `sessions` | hashed token, 7-day expiry | spec wants 30-day sliding (S17, S19.19) |
| `simplefin`, `connections` | encrypted access URL, bridge URL, per-connection errors, backfill flag | becomes `provider_connections` |
| `accounts` | SimpleFIN accounts, type/subtype guessed from names, nickname, excluded flag, removed flag | needs `kind` (budget, tracked, balance_only, liability) per S13 |
| `categories` | three fixed groups (expenses, spending, savings) | replaced by buckets, groups, categories (S1a) |
| `subcategories` | 24 starter categories | mapped to S1a defaults by the importer |
| `transactions` | bank fields **and** user decisions in one mutable row | split into immutable `raw_transactions` plus `transaction_overlays` (S11) |
| `transaction_splits` | split parts (group + subcategory + amount) | becomes `txn_splits` |
| `rules`, `ignore_rules` | substring merchant rules, always-ignore patterns | become user rules in the rules DSL (S18a) |
| `recurring_groups` | detected or manual bills and income | become schedules plus bills, subscriptions and income streams (S14) |
| `reimbursement_links` | one-to-one credit to expense link with amount | becomes `links` (many-to-many, tentative or confirmed) (S12a) |
| `savings_goals` | one active goal | becomes goals (S7) |
| `budget_periods` | stored baseline numbers | dropped (everything is derived) |
| `applied_actions` | idempotency keys for client actions | replaced by `events.idempotency_key` |
| `push_subscriptions`, `settings` | push devices, threshold, last sync, seed flag | keep, migrate |

## 5. api surface

`GET /api/state` returns the whole workspace, including computed budget numbers. `POST /api/actions` takes typed actions such as review, ignore, split, link and goal. The rest are auth, sync, SimpleFIN connect and disconnect, push, and health endpoints. There is no OpenAPI, no versioning and no cursor sync. The spec wants `/api/v1`, OpenAPI, idempotency keys, `GET /sync/changes` and SSE (A6). **Rewrite**, but reuse the auth and middleware code.

## 6. auth and security (keep, extend)

- `server/auth.go`: setup with a setup key, login with bcrypt (cost 12) and TOTP, per-caller rate limiting, and failed-login slowdown that never locks out the owner. Password change revokes other sessions and push. Enabling and disabling TOTP require the password.
- `server/netguard.go`: an outbound client that refuses non-public addresses at dial time and never follows redirects, an allowlist of SimpleFIN hosts, and `TRUST_PROXY` plus `TRUSTED_PROXIES` for client IP and HTTPS detection.
- `server/main.go` middleware: exact-origin check on mutations, JSON-only mutations, CSP, HSTS in production, no-store on the API.
- Missing versus the spec: recovery codes, 30-day sliding sessions, session list and revoke, login history, panic lock, passkeys, OIDC, step-up auth, audit log (S17, S19.19). These are M11 work, apart from sliding sessions and recovery codes, which are cheap and land in M0.

## 7. simplefin sync (adapt)

- `server/simplefin.go`: token claim, AES-GCM-encrypted access URL, `GET /accounts` with `version=2` and `pending=1` on the recent window, a backfill walking back in 45-day chunks until empty, `errlist` and `connections` handling, and connection errors shown per account. It runs every 4 hours, with manual sync throttled to once an hour.
- It writes straight into the mutable `transactions` table and prettifies the merchant on ingest (so the bank string is lost, apart from a `description` column).
- Missing versus the spec:
  - `raw_transactions` immutability
  - id-rotation heal
  - fingerprint dedupe
  - "missing from bank" flags (today a vanished pending row is deleted)
  - balance snapshots
  - coverage and gap tracking
  - a token bucket and daily cap
  - the twice-daily schedule
  - a quota pause
  - `balances-only` refreshes
  - per-account retry
  - a provider interface

## 8. categorization today (reuse as seed data)

- `internal/budget/merchants.json` has 24 categories, 424 merchants with match phrases, about 300 keyword phrases, income hints, transfer and card-payment phrases. `merchants.go` handles lookup (longest whole-word match) and `Prettify()` (strips processor prefixes, store numbers and addresses).
- `server/suggest.go` has the suggestion order: user rule, transfer pairing within 4 days, past choices for the same payer, then the directory, plus `seedDefaults` and `autoCategorize`.
- In the spec this becomes the **starter prior and seed rules** (S18a "starter prior") and the processor-prefix list for **normalization**. Move the JSON, re-map its category ids to the S1a defaults (`docs/plan/legacy-migration.md` has the mapping), and keep its tests.

## 9. keep / adapt / rewrite

| component | decision | notes |
|---|---|---|
| auth (`server/auth.go`) | **keep, move** to `internal/auth` | add sliding 30-day sessions and 8 recovery codes in M0; passkeys and OIDC in M11 |
| security middleware, CSP, origin check | **keep, move** to `internal/api` | |
| `netguard.go` (SSRF guard, trusted proxies) | **keep, move** to `internal/netguard` | the provider client must use it |
| simplefin client (`server/simplefin.go`) | **adapt** into `internal/provider/simplefin` | keep claim, encryption, request shape and backfill walk; replace storage with `internal/ingest` |
| ingest and storage of transactions | **rewrite** | immutable raw rows, overlays, id heal, coverage |
| `internal/db` schema.sql | **rewrite** as versioned migrations | the legacy schema stays only as the source for the importer |
| old `internal/budget.Calculate` | **delete** | replaced by the pure `ComputeMonth` (M3) |
| merchant directory + `Prettify` | **keep as data**, move to `internal/normalize` | re-map category ids to S1a |
| `server/suggest.go` | **rewrite** as `internal/categorize` + `internal/rules` + `internal/learn` | transfer pairing logic is a useful starting point for S12 |
| recurrence detection in `simplefin.go` (`detect`) | **rewrite** as `internal/recurrence` | the current median-gap idea matches S14 |
| push (`server/push.go`) | **keep, move** to `internal/notify` | add alert routing in M9 |
| state + actions API (`server/state.go`) | **rewrite** | events, `/api/v1`, OpenAPI |
| integration test harness (embedded or env postgres, fake SimpleFIN TLS server) | **keep the pattern** | becomes `internal/testkit` |
| react shell, routing by hash, tab bar, sheets, swipe card with momentum, motion and haptics, design tokens in CSS | **keep** | move to `web/`; split `App.tsx` into screens in M8 |
| `src/model.ts` `budget()` and `applyAction()` (client-side math and optimistic mirror) | **delete** | violates ADR-0003; the client renders server snapshots only |
| demo mode (client-side sample data and math) | **owner decision** (see `docs/spec-amendments.md` A-009) | interim: removed in M8, replaced by a server-side seeded demo |
| IndexedDB cache and offline action queue (`src/store.ts`) | **adapt** | keep the queue and idempotency idea; cache `MonthState` snapshots per A6 |
| calculators, "should i buy this" | **keep** | buy check switches to `spend_left` plus discretionary from the server (S10) |
| `Dockerfile`, `compose.yaml` | **adapt** | add a `worker` role and migrations; keep `SOFAR_BIND` and `TRUSTED_PROXIES` |

## 10. conflicts between what exists and the spec

1. **Budget model.** The current safe-to-spend uses a three-month income average and three buckets. The spec uses `plan(M) = I(M-1)`, four buckets plus neutral, pot, spend_base, envelopes and absorption (S2). **Rebuild.**
2. **Mutable transactions.** User decisions overwrite bank rows today. The spec requires immutable bank fields plus an overlay (S11, S18a). **Rebuild.**
3. **Auto-sorting.** `autoCategorize` and the "sort them" button categorize confirmed spending automatically. The spec says suggestions are pre-selected and nothing auto-confirms (S0.5, S11). The current feature only touches already-confirmed rows, but it changes reviewed data without a "apply to past" choice (S18a retroactive scope). **Drop**, replaced by rule backtest with explicit apply.
4. **Reimbursement links.** Today a link is one credit to one expense, made by hand. The spec wants many-to-many, receivables, held payments, abstain and tentative links (S12a). **Rebuild.**
5. **Sync schedule.** Every 4 hours today; the spec says about 06:00 and 18:00 home time, plus manual sync with a 1-hour cooldown and about 6 a day (S15).
6. **Sessions.** 7 days fixed today; the spec says 30 days sliding (S17).
7. **Client-side math.** `src/model.ts` recomputes budgets in the browser (ADR-0003). **Delete.**
8. **Pending handling.** A pending row that vanishes is deleted today; the spec says keep it and flag it "missing from bank", and reconcile pending to posted by content, not only by id (S11, S19.24).
9. **Ignore vs hide.** "Ignore with always" exists today; in the spec this is "hide" (a tombstone keyed by bank id) plus the neutral categories Transfer and Credit card payment and Excluded (S1a, S11).
10. **Env and config names.** The handoff's `.env.example` uses `SOFAR_SECRET_KEY_FILE`, `SOFAR_HOME_TZ` and `SOFAR_BASE_URL`. The deployed server uses `SOFAR_ENCRYPTION_KEY`, `TZ`, `SOFAR_ORIGIN`, `SOFAR_BIND`, `TRUST_PROXY` and `TRUSTED_PROXIES`. **Keep the deployed names** (the owner's server depends on them), and add the new ones as aliases (`*_FILE` variants for secrets). This is decided in `docs/plan/m0.md`.
11. **SimpleFIN credential source.** The handoff assumes `SIMPLEFIN_ACCESS_URL` in the environment. The app claims a setup token in the UI and stores the access URL encrypted in Postgres, which S15 ("re-claim flow with a new setup token") also expects. **Both stay:** the environment variable is used only by `tools/probe`. Recommend that the owner claims a **separate** setup token for the probe, so the probe has its own request allowance and can be revoked independently.

## 11. existing owner data

- **Local dev database** (`sofar-db` container on this PC): 5 accounts, 37 transactions (14 reviewed), 5 rules, 24 categories, 2 recurring items, 1 manual entry. This is test data.
- **The owner's deployed database** (Debian server) is the one that matters. Its contents are not visible from here.
- The plan imports legacy data with a dry run and a backup first (`docs/plan/legacy-migration.md`). It never modifies the legacy tables; the new schema lives alongside until cutover.

## 12. commands (current layout; M0 updates them)

- Install: `npm ci`
- Frontend dev: `npm run dev` (vite on 5173; proxies `/api` to `SOFAR_DEV_API`, default `http://127.0.0.1:8080`)
- Frontend test: `npm test` (vitest). Build: `npm run build`.
- Go build, vet and test: `go build ./...`, `go vet ./...`, `go test ./...`
- Integration test: `go test -tags integration ./server -run TestFullFlow -count=1`, with `SOFAR_TEST_DATABASE_URL` pointing at an empty Postgres database (or unset, to download an embedded Postgres)
- Local Postgres: `docker run -d --name sofar-db -p 127.0.0.1:5432:5432 -e POSTGRES_USER=sofar -e POSTGRES_DB=sofar -e POSTGRES_PASSWORD=... postgres:17-alpine`
- Run the API locally: load `.env.local` into the shell (never read it with a tool), set `SOFAR_ADDR=127.0.0.1:8081` (port 8080 is taken on the owner's PC), then `go run ./server`
- Deploy: `docker compose up -d --build` on the server
