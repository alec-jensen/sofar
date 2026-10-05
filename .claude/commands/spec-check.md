---
description: Check that every spec section has tagged tests and that the code follows the architecture rules
---

1. List every numbered section of `docs/spec.md` (including 12a, 12b, 18a and the 19.x subsections).
2. For each, find tests tagged with that section (for example names starting with `TestS12a_`). Report sections with no tests, ordered by importance (budget math and reimbursement matching first).
3. Check architecture rules: no budget math outside `internal/budget`, no I/O inside `internal/budget`, `internal/money` and `internal/civil`, money only as integer cents, every scheduled job idempotent and keyed.
4. Check that every acceptance test written in the spec has a corresponding test, and that the 14 property invariants in `docs/architecture.md` section 9.3 each have a test.
5. Print a short report: covered, missing, violations. Do not fix anything unless asked, except trivial tagging fixes.
