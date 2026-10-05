# sofar implementation plan

This is the plan an implementing model (Claude Sonnet 5.5) follows to build sofar from `docs/spec.md`. It turns `docs/plan/milestones.md` into concrete, ordered tasks for **this** repository. Every task names its spec sections, the files it touches, the tests to write first, and how to know it is done.

Read in this order at the start of the project (one time):
1. `CLAUDE.md`: rules, non-negotiables, autonomy.
2. This file.
3. `docs/audit.md`: what exists today, keep / adapt / rewrite.
4. `docs/plan/engine-semantics.md`: the exact budget algorithm (read before M3).
5. `docs/plan/legacy-migration.md`: how the owner's data moves over (read before M1 and M8).

Then work one milestone file at a time: `m0.md`, `m1.md`, and so on.

---

## 1. strategy

- **Rebuild on a branch.** All work happens on the long-lived branch `rebuild`. `main` keeps the current app, which the owner uses daily and deploys from. Do not change `main` except for owner-requested fixes.
- **Cut over once** the new PWA reaches parity (end of M8), using the legacy importer and the runbook in `docs/plan/legacy-migration.md`. Until then the owner keeps using the legacy app.
- **Walking skeleton by the end of M3:** a SimpleFIN pull (or a fixture) produces a `MonthState`, and a minimal web screen shows the safe-to-spend number from `GET /api/v1/home`.
- **Tests first** for every spec worked example, acceptance test and invariant. Never weaken a test to pass it.
- **Nothing is cut** (ADR-0008). Later milestones are as real as early ones, but they are planned at slightly coarser grain. Refine a later milestone's tasks at the start of its first session, write the refinement into its file, and commit it.

## 2. milestone order

| order | milestone | file | depends on | owner gate |
|---|---|---|---|---|
| 0 | M-1 audit | `docs/audit.md` (done) | | review keep / adapt / rewrite and the migration plan |
| 1 | M0 foundations | `m0.md` | | |
| 2 | M1 core types, events, ledger, import | `m1.md` | M0 | |
| 3 | M2 SimpleFIN, ingest, probe | `m2.md` | M1 | `SIMPLEFIN_ACCESS_URL` for the probe |
| 4 | M3 budget core (walking skeleton) | `m3.md` | M1 | answer amendments A-001 to A-008 when convenient |
| 5 | M4 income, bills, subscriptions, recurrence | `m4.md` | M3 | |
| 6 | M5 savings, overspend, rollover | `m5.md` | M3, M4 | |
| 7 | M6 categorization and rules | `m6.md` | M2 | |
| 8 | M7 reimbursements | `m7.md` | M2, M6 | |
| 9 | M8 PWA and cutover rehearsal | `m8.md` | M3 to M7 | visual review on a phone, then cutover |
| 10 | M9 reports, alerts, forecasting, search | `m9.md` | M5, M7 | |
| 11 | M9a gamification (the grove) | `m9a.md` | M6 to M9 | play with it for a few days |
| 12 | M10 extended systems | `m10.md` | M9 | |
| 13 | M11 platform | `m11.md` | M8 | |
| 14 | M12 hardening | `m12.md` | all | release checklist |

If the owner has not supplied the probe credential when M1 finishes, do M3 before M2 (M3 needs only M1 and fixtures).

## 3. decisions already made for the implementer

Record each as an ADR in M0 (task M0.1). Do not re-decide them.

| ADR | decision | why |
|---|---|---|
| 0010 | **Migrations:** `github.com/pressly/goose/v3` with SQL files embedded from `internal/store/migrations`, run at startup and by `sofar migrate`. The legacy `schema.sql` is retired; migration `00001` quarantines legacy tables (`legacy-migration.md`). | versioned, MIT, simple |
| 0011 | **Job queue:** a small in-repo Postgres queue (`jobs` table, unique `key`, `FOR UPDATE SKIP LOCKED`, enqueue in the same transaction) plus a scheduler guarded by a Postgres advisory lock. No River. | single user, few jobs; architecture A1 already says sofar owns scheduling and catch-up |
| 0012 | **Layout:** Go code in `cmd/sofar` and `internal/*` per architecture A4. The frontend moves to `web/` as its own npm project. | A4 |
| 0013 | **Property tests:** `pgregory.net/rapid` (MPL-2.0, test-only). | shrinking, seeded reproducibility |
| 0014 | **API contract:** spec-first `api/openapi.yaml`. Go handlers are hand-written; tests validate requests and responses against the spec with `github.com/getkin/kin-openapi` (MIT). TypeScript types are generated with `openapi-typescript` (MIT). | one source of truth for the client |
| 0015 | **Database access:** keep `database/sql` with the `pgx/v5/stdlib` driver and hand-written SQL in repository packages under `internal/store`. No ORM, no code generation. | matches the existing code, easy to review |
| 0016 | **Config names:** keep the deployed names (`SOFAR_ENCRYPTION_KEY`, `SOFAR_ORIGIN`, `TZ`, `SOFAR_BIND`, `TRUST_PROXY`, `TRUSTED_PROXIES`, `SOFAR_SETUP_KEY`, VAPID keys). Add `*_FILE` variants for every secret, and accept `SOFAR_HOME_TZ` and `SOFAR_BASE_URL` as aliases. | the owner's server depends on the existing names |
| 0017 | **End-to-end tests:** Playwright (Apache-2.0) with `@axe-core/playwright` for accessibility. | design brief |
| 0018 | **Passkeys** (M11): `github.com/go-webauthn/webauthn` (BSD-3). **OIDC:** `github.com/coreos/go-oidc/v3` (Apache-2.0). | maintained, permissive |
| 0019 | **OFX, QFX and QIF parsing:** written in-repo (no GPL parsers such as `ofxgo`). | license |
| 0020 | **Sync request budget:** a token bucket of 20 requests per rolling 24 hours per access URL (the provider allows about 24), shared by scheduled, manual and backfill requests. The probe uses a **separate** access URL. | headroom; no shared-quota surprises |

When a later choice is not covered here, make it, note it in `docs/progress.md`, and write an ADR if it is not obvious. Prefer permissively licensed, well-maintained dependencies.

## 4. how to work a session

1. Read `docs/progress.md` (the last entry) and the current milestone file. Read only the spec sections the next task cites.
2. Take the **first unchecked task**. If it is too large for one session, split it into numbered subtasks in the milestone file first and commit that.
3. Write the tests the task lists. Run them and see them fail for the right reason.
4. Implement in small steps. Run the narrowest relevant tests after each step (`go test ./internal/budget/...`, not the whole suite).
5. When green: `gofmt`, `go vet ./...`, the package tests, and `npm test` in `web/` if the frontend changed. Commit with a conventional message (`feat(budget): ...`, `test(budget): ...`). One logical change per commit. If `git commit` hangs, GPG signing is waiting for a passphrase: stop and tell the owner (see the owner's global instructions).
6. Tick the task box in the milestone file. Add a short entry to `docs/progress.md`.
7. Before closing a milestone: run the full suite through a subagent, run `/spec-check`, ask the `spec-auditor` agent to review the milestone's diff, and stop at any owner gate.

**Context discipline:** do not paste whole spec or plan files into context. Use Grep to find the section, then Read with an offset. Delegate full-suite runs and broad searches to subagents.

**When stuck:** if the spec seems wrong or two sections conflict, open an amendment (`/amend`), implement the conservative reading, and continue. If a task depends on something missing, note it in `docs/progress.md` and take the next independent task.

**Platform notes:** the owner develops on Windows (PowerShell plus Git Bash) with Docker Desktop. Postgres for tests: either `SOFAR_TEST_DATABASE_URL` pointing at a throwaway database in the `sofar-db` container, or embedded Postgres (downloads once into `.cache/`). Port 8080 is taken on the owner's PC, so run the API locally on `127.0.0.1:8081` and point the vite proxy at it with `SOFAR_DEV_API`.

## 5. owner decisions and inputs

| when | what | default if no answer |
|---|---|---|
| before M0 | Commit (and push, if wanted) the uncommitted mobile fixes on `main`, then let the agent create the `rebuild` branch. | the agent stops and asks |
| before M2 | Claim a **separate** SimpleFIN setup token for the probe. Export it in the shell that launches Claude Code (PowerShell: `$env:SIMPLEFIN_ACCESS_URL = '...'`). Never put it in a file. | build against the public demo access URL and report what is blocked |
| any time | Decide amendments A-001 to A-010 (`docs/spec-amendments.md`). | the interim behavior in each entry |
| M8 | Visual review on a phone, then approve the cutover dry run. | no cutover |
| M9a | Play with the grove and adjust the dial and defaults. | defaults from `docs/config-defaults.md` |

## 6. definition of done (every task)

- Tests named with spec tags (`TestS2_ExampleC_PermanentRaiseThenLoss`, `TestS12a_T1_ReportedBug_NoLinkToUnrelatedExpense`).
- Unit, scenario and property tests where relevant, all passing.
- A migration if the schema changed. `api/openapi.yaml` updated if the API changed.
- Explainability entries for every new budget movement.
- No budget math outside `internal/budget`. Money in integer cents.
- UI copy lowercase, using the S0a vocabulary. New screens checked for accessibility.
- `docs/config-defaults.md` updated for any new tunable. `docs/progress.md` updated.
