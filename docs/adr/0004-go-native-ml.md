# ADR-0004: go-native ml core

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S18a, S12b; docs/architecture.md A5

## context
the categorization and reimbursement matching engines need rules, nearest-neighbor case memory, logistic regression, calibration and assignment.

## decision
implement these natively in go so sofar runs with nothing extra installed. an optional sidecar may provide ocr, embeddings or local models, but nothing in the core depends on it.

## consequences
no python runtime for self-hosters. gradient-boosted trees and embeddings are deliberately out of the core.

## alternatives considered
python sidecar for everything (rejected: operational burden), external ml services (rejected: privacy).
