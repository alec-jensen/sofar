# ADR-0019: ofx, qfx and qif parsing is in-repo

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
milestone M1 adds csv, ofx, qfx and qif import.

## decision
write the parsers in this repository with fuzz tests. do not depend on gpl parsers.

## consequences
licence, and bank files are untrusted input that needs careful handling.

## alternatives considered
ofxgo (rejected: licence).
