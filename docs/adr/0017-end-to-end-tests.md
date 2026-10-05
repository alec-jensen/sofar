# ADR-0017: end-to-end tests: playwright with axe

- status: accepted
- date: 2026-10-05
- refs: docs/plan/README.md section 3

## context
the pwa needs end-to-end coverage of offline, swipe and accessibility behavior.

## decision
playwright for flows (including offline and a virtual webauthn authenticator) with @axe-core/playwright for accessibility.

## consequences
design brief requires wcag 2.2 aa checks on every screen.

## alternatives considered
cypress (rejected: weaker offline and multi-context support).
