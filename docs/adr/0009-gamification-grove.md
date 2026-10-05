# ADR-0009: gamification as a skin over truth (the grove)

- status: accepted
- date: 2026-10-02
- refs: docs/spec.md S19.25, docs/architecture.md A9.11

## context
the owner wants sofar to be gamified, almost or fully. gamification in finance apps tends toward streak anxiety, rewarding restraint, and chasing metrics instead of the real goal. sofar also depends on clean daily review labels to train its categorization and matching engines, so a reward that encourages speed would damage its core.

## decision
a garden metaphor (the grove) derived from real state, with growth and levels, weekly forgiving streaks with earned freezes, quests drawn from real opportunities, sticky badges with evidence, and cosmetic-only rewards. an off, quiet or full dial with full as the default. the play module reads events and snapshots only. rewards never depend on speed, never reward spending less, never gate features, never shame. everything is local.

## consequences
a differential test must prove budget state is identical with and without the module. rewards need integrity rules (caps, dwell thresholds, nightly settle). copy needs a lint. progress meters recompute but awards are sticky.

## alternatives considered
leaderboards and social features (rejected: single-user, privacy), daily hard-reset streaks (rejected: anxiety), points for spending less (rejected: reinforces restriction), random rewards and loot boxes (rejected: manipulation).
