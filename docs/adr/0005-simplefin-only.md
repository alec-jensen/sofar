# ADR-0005: simplefin only, provider seam kept

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S19.15, S19.16

## context
the owner chose simplefin and does not want a second provider.

## decision
simplefin (protocol v2) is the only live provider. csv, ofx and manual accounts are providers too. a provider interface exists internally for clean code and testing only.

## consequences
simple operations and cost. coverage gaps are handled by import and manual accounts.

## alternatives considered
adding a second live provider (rejected by the owner).
