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
