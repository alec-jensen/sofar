# config defaults

Every tunable default lives here and is exposed in settings. Add a row whenever the spec or an implementation decision introduces a default.

| setting | default | spec ref | notes |
|---|---|---|---|
| scheduled sync times | about 06:00 and 18:00 home time | S15 | two pulls per day |
| manual sync cooldown / daily cap | 1 hour / about 6 per day | S15 | stays well under the provider cap |
| stale data threshold | 48 hours | S10, S15 | |
| recurrence detection threshold | 3 occurrences | S14 | |
| missing occurrence grace | 25% of interval, capped at 3 days | S14 | |
| boundary attribution tolerance | min(5 days, interval/4) | S3 | |
| unmatched reimbursement hold | 30 days | S12 | |
| held-payment grace window | 5 days | S12a | learned per account over time |
| pot default destination | general savings, at month end | S2 | |
| emergency fund target | 3 months of spending | S8 | trailing 3-month actual bills plus spending |
| emergency fund rebuild suggestion | 6 months | S8 | chosen at withdrawal time |
| merchant rule induction | 2 consistent labels | S18a | |
| conditional rule induction | 3 consistent labels | S18a | |
| learned rule probation | 5 matches | S18a | |
| reimbursement pattern graduation | 3 consecutive confirmations, no rejections | S12b | |
| materiality gate (no auto-tentative above) | $100 | S12b | |
| auto-tentative precision target | 99% | S12b | release gate |
| teach-once accuracy target | 95% | S18a | release gate |
| first-pass accuracy targets | 90% by day 30, 95% by day 90 | S18a | release gate |
| audit mode | off | S18a | |
| AI assistant | off, aggregates only | S19.17 | |
| session length | 30 days sliding | S19.19 | `last_seen_at` is written at most once an hour per session |
| recovery codes | 8, one use each | S19.19 | shown once, stored bcrypt-hashed; replaced on regeneration |
| login attempts | 10 per address per 15 minutes | S19.19 | a success clears them; after 20 failures overall, failures are delayed up to 3 s |
| job retry backoff | 30 s doubling, capped at 6 h | A7 | |
| idempotency key retention | until purged | A6 | a purge job comes later |
| gamification level | full | S19.25 | off, quiet, full |
| celebration intensity | 2 | S19.25 | 0 to 3 |
| celebration taper | on | S19.25 | quieter over time |
| tended day threshold | queue cleared or 5 deliberate decisions | S19.25 | days with nothing new count as tended |
| tended week threshold | 4 tended days | S19.25 | |
| freeze accrual | 1 per 4 tended weeks, max 3 | S19.25 | earned, never bought |
| streak decay on a missed week | minus 25%, rounded down | S19.25 | longest streak preserved |
| quick confirm dwell threshold | 1.5 seconds | S19.25 | below this earns 0 growth |
| growth soft daily cap | 60 (50% value after 40) | S19.25 | |
| growth level thresholds | 0, 100, 300, 700, 1500, 3000, 6000, 12000, 24000, 48000 | S19.25 | |
| clean-books hidden-share limit | unusual share withholds the award | S19.25 | threshold tunable |
| gamification notifications | off | S19.25 | optional weekly recap |
| transfer pairing window | 3 days (5 for an account pair paired 3 or more times) | S12 | transaction dates |
| transfer ask delay | 3 days | S12 | a lone transfer-looking row waits this long for a partner |
| match suggestion threshold | 0.6 calibrated probability | S12a | raised by the hidden-pending hint, never lowered |
| match ambiguity margin | 0.15 | S12a, S12b | best must beat the runner-up by this much or it asks |
| match auto-tentative confidence | 0.8 | S12b | only for a proven pattern |
| held payment grace | 5 days from the payment | S12a | then asks "what was it for"; later learned per account |
| held payment hold | 30 days after the grace | S12 | held outside income, then needs a decision |
| match lookback | 30 days (payer's own lag plus 2 once learned) | S12b | |
| posting delay | 3 days (the account's median once measured) | S12a | not penalized for an expense dated after the payment |
| materiality gate | $100 | S12b | above this a link is never auto-tentative |
| pattern graduation | 3 consecutive confirmations, no strike | S12b | one strike or rejection returns it to suggest-only |
| pattern share tolerance | 4 points, widened to twice the payer's own spread, at most 10 | S12b | tips and rounding |
| pattern precision floor | 80% over the last 10, at least 5 | S12b | below this the pattern is demoted and the owner told |
| pattern decay | 120 days without a confirmation | S12b | demoted to suggest-only |
| receivable aging alerts | 14, 30 and 60 days (the payer's own lag when known) | S12a | |
| receivable overdue | past the expected-by date, else 30 days | S12a | derived, never stored |
| matching job | after every import or manual entry, and nightly at 03:45 home time | A7 | keyed per minute and per day |
