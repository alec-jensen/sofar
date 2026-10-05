---
description: Work a milestone from docs/plan/milestones.md to its exit criteria
argument-hint: <milestone id, e.g. M3>
---

Work milestone $ARGUMENTS.

1. Read `docs/progress.md` and the $ARGUMENTS section of `docs/plan/milestones.md`. Read only the spec and architecture sections it references.
2. Write the tests the milestone requires first (spec worked examples, acceptance tests, property invariants), tagged with spec sections.
3. Implement in small steps. Run the relevant tests after each step. Commit when green with a conventional commit message.
4. Use the `test-writer` subagent for edge-case and property tests, and the `spec-auditor` subagent to review your diff against the spec before you finish.
5. When all exit criteria are met, run `/spec-check`, run the full test suite, tick the boxes in `docs/plan/milestones.md`, and add an entry to `docs/progress.md`.
6. Stop at any human gate listed for the milestone and ask the owner. Stop and ask before anything on the "ask first" list in `CLAUDE.md`.

If the spec seems wrong or ambiguous, open an entry in `docs/spec-amendments.md`, keep the conservative reading, and ask.
