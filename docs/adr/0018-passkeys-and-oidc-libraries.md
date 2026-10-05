# ADR-0018: passkeys and oidc libraries

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
milestone M11 adds passkeys and optional oidc.

## decision
github.com/go-webauthn/webauthn for passkeys and github.com/coreos/go-oidc/v3 for oidc.

## consequences
maintained, permissively licensed.

## alternatives considered
hand-rolled webauthn (rejected: security risk).
