# Kickoff prompt for Claude Code (Sonnet 5.5)

Start Claude Code in the repo root, on a clean `main` (see "before you paste" below), with the model set to Sonnet 5.5. If the probe credential is ready, export it in the same shell first (PowerShell: `$env:SIMPLEFIN_ACCESS_URL = '...'`). Paste everything between the lines.

## before you paste (owner)

1. Commit, and push if you want, the pending mobile fixes on `main` (`git status` shows them).
2. Read `docs/audit.md` and `docs/plan/legacy-migration.md` (the M-1 human gate). Tell Claude anything you disagree with.
3. Skim the amendments in `docs/spec-amendments.md` (A-001 to A-010). The interim behaviors apply until you decide.
4. Optional, before M2: claim a separate SimpleFIN setup token for the probe.

---

You are the lead engineer for sofar, a single-user, self-hosted budgeting app. Read `CLAUDE.md`, then `docs/plan/README.md`, then the last entry of `docs/progress.md`.

The repo audit (milestone M-1) is already done: `docs/audit.md` describes the existing build and the keep, adapt or rewrite decisions. The exact budget algorithm is pinned in `docs/plan/engine-semantics.md`, and the owner's data migration in `docs/plan/legacy-migration.md`. Do not redo that work; trust it and re-check only the files a task touches.

Work on the branch `rebuild` (create it from `main` in task M0.1, after confirming `main` has no uncommitted changes). Do not change `main`.

Follow the session loop in `docs/plan/README.md` section 4. Start with `docs/plan/m0.md` and take the first unchecked task. Write the tests a task lists before the code. Commit when green with conventional messages, tick the task, and log progress. Keep going task by task and milestone by milestone until you reach an owner gate, an "ask first" item from `CLAUDE.md`, or the end of the session's useful context. Then write a progress entry that lets the next session pick up cleanly.

Rules that matter most:
- The spec wins on behavior. Where it is ambiguous, follow the amendments' interim behavior or open a new amendment, and continue with the conservative reading.
- Budget math lives only in `internal/budget`, pure, in integer cents, per `engine-semantics.md`. Never in TypeScript.
- Never touch the owner's existing data destructively. Legacy tables are quarantined, never dropped.
- The SimpleFIN credential is only for `tools/probe`, from the environment. Never print it, write it or read `.env` files.

When you start, reply with: (1) a one-paragraph summary of your understanding, (2) the tasks you plan to do this session, and (3) anything you need from the owner right now. Then begin.

---
