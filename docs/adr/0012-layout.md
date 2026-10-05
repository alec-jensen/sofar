# ADR-0012: layout: cmd/sofar, internal/*, web/

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the legacy repo has `server/` as package main and the frontend at the root.

## decision
go code in cmd/sofar and internal/* per architecture A4; the frontend moves to web/ as its own npm project.

## consequences
matches the architecture document and keeps budget packages isolated.

## alternatives considered
keeping `server/` as package main (rejected: blocks the package dependency rules).
