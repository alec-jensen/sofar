# ADR-0001: stack: go, postgres, react pwa

- status: accepted
- date: 2026-10-02
- refs: docs/architecture.md A1

## context
the owner already runs a go backend, a react frontend and postgres, and wants an installable offline-first pwa that is easy to self-host.

## decision
go backend (api and worker roles in one binary), postgres as the only required service, react pwa with indexeddb and a service worker, docker compose for deployment, a postgres-backed job queue.

## consequences
one language for all budget math and ml, minimal moving parts for a self-hoster, no redis or external queue.

## alternatives considered
node backend (rejected: owner stack is go), sqlite (rejected: concurrent jobs and the event and snapshot workload fit postgres better).
