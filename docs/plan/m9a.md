# M9a gamification: the grove

Spec: S19.25 (all of it), S0 principle 10, S0a vocabulary. Architecture: A9.11. ADR-0009. Design: `docs/design-brief.md` "play layer". Defaults: `docs/config-defaults.md`.

**Goal:** a calm garden derived from real state, with growth, forgiving weekly streaks, quests, sticky awards, fluency, a season recap and an intensity dial. It never touches budget math.

**Exit criteria:**
- All 10 S19.25 acceptance tests pass.
- The differential test proves budget state is identical with and without play.
- The copy lint passes in CI.
- **Owner gate:** the owner plays with it for a few days and adjusts the dial and defaults.

---

### [ ] M9a.1 isolation first
- `internal/play` gets a **read-only repository interface** (events, snapshots, labels, receivables). It has no import of `internal/store` write paths and no SQL writes outside `play_*` tables.
- Enforce it with `internal/archtest` (an import check) plus a test that runs `play-settle` with a database role that has only SELECT on budget tables.
- Tests: `TestS19_25_T2_BudgetStateIdenticalWithAndWithoutPlay` (run recompute with play off and on, compare every `MonthState` hash), `TestS19_25_T1_OffComputesNothing`.

### [ ] M9a.2 settings and the dial
- `play_settings`: level off, quiet or full (default full); per-component toggles; celebration intensity 0 to 3 (default 2); taper on; notifications off.
- Turning play off and on loses nothing. A reset deletes all play data. Export includes play data.

### [ ] M9a.3 growth with integrity
- `play_xp_events` are computed **only** in the nightly `play-settle` job (idempotent, keyed by home-time date) from settled events: label events with dwell times, allocations, goal events, resolved overspends, confirmed links, drift fixes, received receivables, marked recurring items, verified cancellations.
- Amounts come from the S19.25 table.
- Integrity rules: a soft daily cap of 60 (50% value after 40); diminishing returns; a confirm under 1.5 seconds or in a rapid streak earns 0 (never accused); hidden, undone, duplicate and imported items earn nothing.
- Levels from the thresholds in `config-defaults.md`.
- Tests: `TestS19_25_T3_HundredFastConfirmsNearZero`, `TestS19_25_T4_CorrectionBeatsConfirm`, `TestS19_25_UndoBeforeSettleEarnsNothing`, `TestS19_25_ImportedEarnNothing`.

### [ ] M9a.4 tended days, weeks and streaks
- A tended day means the day's new transactions are all reviewed, or at least 5 deliberate decisions were made, or there was nothing new.
- A tended week is at least 4 tended days. The streak counts consecutive tended weeks.
- **Rest mode** pauses without cost. **Freezes:** 1 earned per 4 tended weeks, at most 3, applied automatically to a missed week.
- **Decay:** a missed week without a freeze reduces the streak by a quarter (rounded down). The longest streak is kept forever. Travel mode pauses.
- Days are home-time calendar days, DST-safe.
- Tests: `TestS19_25_T5_FreezeKeepsStreak_NoFreezeDecaysQuarter`, `TestS19_25_RestModePauses`, `TestS19_25_DSTWeekBoundaries`, `TestS19_25_FreezesStopAtCap`.

### [ ] M9a.5 awards, quests, fluency
- **Awards** (`play_awards`) are sticky, each with an evidence snapshot, and never revoked by a recompute. The families come from S19.25, including secret awards.
- **Clean books** is withheld silently when the hidden share is unusual.
- **Quests:** up to 3 a week, generated from real opportunities (the insights feed and queue state). Never restriction challenges from the app; user-defined challenges are allowed. An unfinished quest rotates away silently.
- **Fluency** comes from the real S18a metrics (M6.8): first-pass accuracy tiers, known-merchant coverage, teach-once rate, "caught" count, and average review time (informational only).
- Tests: `TestS19_25_T6_AwardSurvivesRecompute`, `TestS19_25_T7_HiddenShareWithholdsCleanBooks`, `TestS19_25_T8_GoalCompleteMatureTreeAndAward`, `TestS19_25_QuestsNeverSuggestRestriction`.

### [ ] M9a.6 grove rendering (web)
- `grove_state` is a derived cache. Procedural SVG from stable ids and a seed:
  - a goal is a tree, mature with a plaque when complete, pruned (neutral) when borrowed from
  - the EF is a greenhouse or oak
  - discretionary is a flower patch
  - sinking funds are seed beds
  - subscriptions are a hedge
  - the pot is seeds
  - "owed to you" is sprouts
  - debt payoff is a clearing
  - fluency is overall health, the season follows the calendar month, and the streak is a growth ring
- Light and dark versions. A text description for screen readers, keyboard navigation, and reduced motion. Privacy mode hides amounts.
- The "all caught up. something grew." moment on review completion. Celebrations scale with intensity and taper. Intensity 0 with reduced motion means no animation.
- Tests: Playwright `grove.spec.ts` (levels off, quiet and full; intensity 0; reduced motion; the screen-reader description), plus `TestS19_25_T10_NoAnimationAtIntensityZero`.

### [ ] M9a.7 season recap and notifications
- At rollover: a recap card derived from real data (weeks tended, accuracy change, goals advanced, receivables collected, pot planted, wins, one gentle "next"). An image export with amounts hidden by default. Skippable.
- Play notifications are off by default: an optional weekly recap and a "your grove grew" digest. Never more than one a day, quiet hours apply, and never a loss or guilt message.

### [ ] M9a.8 copy lint in CI
- All play strings live in one module (`web/src/play/copy.ts` and `internal/play/copy.go`). A test fails on the forbidden words (failed, lost, broken, missed, behind), on exclamation points, and on uppercase starts.
- Tests: `TestS19_25_T9_CopyLint` (Go) and the equivalent vitest.
