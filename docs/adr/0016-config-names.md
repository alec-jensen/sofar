# ADR-0016: config names: keep the deployed names

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the handoff's .env.example uses different names than the owner's running deployment.

## decision
keep SOFAR_ENCRYPTION_KEY, SOFAR_ORIGIN, TZ, SOFAR_BIND, TRUST_PROXY, TRUSTED_PROXIES, SOFAR_SETUP_KEY and the VAPID variables. every secret also accepts a NAME_FILE variant; SOFAR_HOME_TZ and SOFAR_BASE_URL are accepted as aliases.

## consequences
the owner's deployed server depends on the existing names.

## alternatives considered
adopting the handoff's names (rejected: would break the deployment).
