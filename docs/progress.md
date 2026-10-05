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
