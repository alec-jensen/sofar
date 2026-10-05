# ADR-0014: api contract: spec-first openapi

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the pwa and the tests both need a stable api contract.

## decision
api/openapi.yaml is the source of truth. handlers are hand-written; tests validate responses with kin-openapi; typescript types are generated with openapi-typescript.

## consequences
one contract for the client and the tests, no codegen of server code.

## alternatives considered
code-first annotations (rejected: drifts), grpc (rejected: browser pwa).
