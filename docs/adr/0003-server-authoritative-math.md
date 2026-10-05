# ADR-0003: server is authoritative for budget math

- status: accepted
- date: 2026-10-02
- refs: docs/architecture.md A6

## context
the pwa is offline-first, so it is tempting to compute budget numbers on the client.

## decision
all budget math runs on the server. the client caches derived snapshots and queues offline actions with idempotency keys. optimistic local updates are limited to review actions. budget math is never reimplemented in typescript.

## consequences
no drift between two implementations. offline numbers can be slightly stale and are labeled with their computed time.

## alternatives considered
shared math compiled to wasm (rejected for now: complexity without need).
