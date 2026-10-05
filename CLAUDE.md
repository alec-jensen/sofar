# sofar

sofar (always lowercase) is a **single-user, self-hosted personal budgeting app** for one independent person. It is not a product: no households, no multi-tenancy, no billing, no telemetry. Stack: Go backend, React installable PWA (offline-first), Postgres. Bank data comes from SimpleFIN only.

The owner's goal is the best possible version at full scope. There is **no MVP or v1 cutoff**. Build everything in the spec, in dependency order.

## Where things live

Read these on demand. Do not paste whole files into context.

- `docs/spec.md`: the logic spec. **Source of truth for behavior.** Sections are tagged by number (for example S12a for section 12a).
- `docs/architecture.md`: how to build it (modules, data model, jobs, API, tests, build order). Source of truth for technical structure.
- `docs/plan/README.md`: **the implementation plan. Start here.** Strategy (rebuild on the `rebuild` branch, cut over at M8), decisions already made (ADRs 0010 to 0020), the session loop, and owner gates.
- `docs/plan/m0.md` to `docs/plan/m12.md`: task-level steps per milestone (files, tests to write first, done criteria). Work the first unchecked task.
- `docs/plan/engine-semantics.md`: the exact order of operations for `ComputeMonth`. Read it before any work in `internal/budget`.
- `docs/plan/legacy-migration.md`: how the owner's existing data moves to the new schema (legacy tables are quarantined, never dropped).
- `docs/audit.md`: what exists today and keep / adapt / rewrite (milestone M-1, done).
- `docs/discovery/known-facts.md`: what the legacy app already learned about the owner's real SimpleFIN data.
- `docs/plan/milestones.md`: the original milestone overview with exit criteria (the `m*.md` files refine it).
- `docs/design-brief.md`: UI direction, screens and flows.
- `docs/discovery/PROBE.md`: the SimpleFIN data-discovery probe. **Do this early** (milestone M2, but start the probe as soon as the repo audit is done).
- `docs/adr/`: decisions already made, plus the template for new ones.
- `docs/spec-amendments.md`: the only way behavior in the spec changes.
- `docs/progress.md`: running log. Update it at the end of every work session.

## Non-negotiables (from the spec)

1. **Users spend last month's money.** `plan(M) = I(M-1)`. Nothing becomes spendable on its own: freed-up money lands in the pot (UI: "uncategorized") until the user allocates it.
2. **Allow everything, record everything, block nothing.** The app warns and shows consequences. It never refuses a user action because it would be unwise. Only deleting data asks for confirmation.
3. **Everything is derived.** Raw bank data is immutable. User decisions are append-only events. Budget numbers come from the pure function `ComputeMonth` in `internal/budget`.
4. **Every transaction is reviewed by the user daily.** Nothing auto-confirms. Suggestions are pre-selected.
5. **Server is authoritative for all budget math.** Never reimplement budget math in TypeScript.
6. **Learn only from user labels,** never from the system's own guesses.
7. **Never auto-link a reimbursement on a guess.** Exactly two cases may link tentatively (exact receivable match, proven pattern), and they still wait for review.
8. **Always request `pending=1`** on every SimpleFIN pull, and design for banks that never return pending items.
9. **Bank strings are untrusted input** everywhere (rendering, rules, regexes, logs).
10. **Gamification is a skin over truth** (spec section 19.25). It reads events and snapshots, never writes budget tables, never changes `ComputeMonth` output, never gates a feature, never rewards spending less or speed, never shames. A CI test proves budget state is identical with and without it, and a lint checks its copy.

## Architecture rules

- All budget math lives in `internal/budget`. That package is pure (no I/O) and depends only on `internal/money` and `internal/civil`.
- Money is integer cents everywhere. Never floats.
- Calendar dates from banks are dates, not instants. Calculations use the owner's home time zone.
- Every job is idempotent and keyed (by date or month) so catch-up after downtime is safe.
- Jobs are enqueued in the same transaction as the change that caused them.
- Every budget movement emits explainability entries (double-entry envelope movements).
- UI copy is lowercase, calm, casual, no exclamation points. Use the vocabulary table in `docs/spec.md` section 0a (for example "safe to spend", "uncategorized", "owed to you").

## How to work

- **Start every session** by reading `docs/progress.md` and the current milestone in `docs/plan/milestones.md`.
- **Work in small, verified steps.** Write the tests from the spec first (or alongside), run them, and commit when green. Conventional commit messages. One logical change per commit.
- **Tag tests with the spec section** in the test name (for example `TestS12a_ReportedBug_NoLinkToUnrelatedExpense`). Run `/spec-check` before closing a milestone.
- **Keep CLAUDE.md short.** If you learn a command or convention worth keeping, add it to the Commands section below, not as prose elsewhere.
- **Use subagents** for verbose work (running the full test suite, broad searches, spec audits) so the main context stays clean. The `spec-auditor` and `test-writer` agents are in `.claude/agents/`.

## Autonomy

**Do freely:** write code, tests, migrations and docs; run tests, linters and builds; run the probe in read-only mode within its request budget; make routine engineering decisions and record non-obvious ones as ADRs; refactor your own work.

**Do, then record in `docs/progress.md`:** add dependencies (prefer well-maintained, permissively licensed ones; note why); choose between equivalent designs; add settings or defaults the spec leaves unspecified (make them configurable and list them in `docs/config-defaults.md`).

**Ask the owner first:**
- Anything that changes budget math semantics or contradicts the spec. Write an entry in `docs/spec-amendments.md` with evidence, propose the change, and **continue with the more conservative reading** while waiting.
- Deleting or rewriting the owner's existing data. Any migration of existing data needs a dry run and a backup first.
- Anything that touches real credentials beyond the read-only probe or sync client.
- Network calls to anything other than SimpleFIN and the FX rates source (and package registries).
- Adding paid services, telemetry of any kind, or force pushes.
- UI visual direction changes (palette, typography) beyond `docs/design-brief.md`.

## Secrets and safety

- The app itself never reads `SIMPLEFIN_ACCESS_URL`: users claim a setup token in the UI, and the resulting access URL is stored encrypted in Postgres. The environment variable exists **only** for `tools/probe`, and the owner should use a separate token for it (`docs/plan/README.md` section 5).
- The SimpleFIN access URL is a credential. For the probe it is provided **only** through the environment variable `SIMPLEFIN_ACCESS_URL` exported in the shell before launching Claude Code (PowerShell: `$env:SIMPLEFIN_ACCESS_URL = '...'`). **Never** write it to a file, never print it, never commit it, never read `.env` files. `.claude/settings.json` denies reading them.
- SimpleFIN is accessed **only** through the probe and the sync client. No ad hoc curl requests.
- Respect the provider's daily request cap (about 24 requests per day per access URL, plus per-account limits). Use a token bucket. Prefer `balances-only` and per-account requests. Never loop on failures.
- Raw probe output stays **outside the repo** in `~/.sofar-probe/` (gitignored by location). Only redacted fixtures may be committed.
- Logs redact amounts, descriptions and account numbers by default.

## Commands

Current layout (until M0 moves the frontend to `web/` and Go to `cmd/sofar`; M0.13 updates this list). The owner develops on Windows with Git Bash and Docker Desktop.

- Install web deps: `npm ci`
- Web dev server: `npm run dev` (port 5173; proxies `/api` to `SOFAR_DEV_API`, default `http://127.0.0.1:8080`)
- Web tests and build: `npm test`, `npm run build`
- Go build, vet, test: `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .` must print nothing
- Integration tests: `go test -tags integration ./server -count=1` with `SOFAR_TEST_DATABASE_URL` set to an empty database in the local `sofar-db` Postgres container (or unset to download an embedded Postgres into `.cache/`)
- Run the API locally: load the env file into the shell without reading it with a tool (`set -a; . ./.env.local; set +a` in Git Bash), override `SOFAR_ADDR=127.0.0.1:8081` (port 8080 is taken on this PC), then `go run ./server`. Start vite with `SOFAR_DEV_API=http://127.0.0.1:8081`.
- Migrations: none yet (legacy idempotent `schema.sql`). M0.6 introduces goose: `go run ./cmd/sofar migrate up`.

## Definition of done (every task)

Tests (unit, scenario, and property where relevant) pass. Migration included if the schema changed. OpenAPI updated if the API changed. Explainability entries emitted for any new budget movement. UI copy uses the lowercase vocabulary. Accessibility checked for new screens. Spec tag present on tests. `docs/progress.md` updated.
