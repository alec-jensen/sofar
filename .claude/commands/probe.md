---
description: Run the SimpleFIN data-discovery probe safely and write the report
argument-hint: <snapshot | observe | report>
---

Run the probe in mode $ARGUMENTS following `docs/discovery/PROBE.md` exactly.

- Confirm `SIMPLEFIN_ACCESS_URL` is set in the environment without printing it. If it is not set, say so and stop (or use the public demo URL if the document allows it for this mode).
- Enforce the request budget: at most 12 requests for a snapshot, 2 per day for observe. Stop on any quota warning.
- Raw output goes only to `~/.sofar-probe/`, never into the repo.
- Produce redacted fixtures and update `docs/discovery/report.md`.
- File `docs/spec-amendments.md` entries for anything that contradicts the spec, and summarize the implications to the owner.
