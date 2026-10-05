# sofar: design brief

Direction for UI work. Behavior is defined in `docs/spec.md`. This document defines look, feel, screens and flows. If the owner provides `docs/design-language.md`, it overrides the palette and type choices here.

## Feel

- **Calm, minimal, quietly playful.** Fewer elements per screen than Rocket Money. One hero number, then quiet supporting content. A real game layer (the grove) sits alongside, never in front of the numbers.
- **Mobile-first**, installable PWA. Desktop is a comfortable widening of the same layout, with keyboard shortcuts.
- **Soft and rounded:** generous radii, soft shadows or none, lots of whitespace.
- **Green color scheme, muted:** sage, moss, muted mint. Never neon or emerald. Neutral warm greys and off-whites. Dark mode follows the same hues, lower contrast surfaces.
- **Typography:** warm humanist sans, readable at small sizes. Tabular figures for money.
- **Copy:** all lowercase, casual, calm. No exclamation points, no hype, no guilt. The product name is always `sofar`.
- **Tone for hard states:** matter-of-fact. "you're over by $42. where should it come from?" not "warning!".

Use the vocabulary in spec section 0a (safe to spend, uncategorized, owed to you, waiting for the charge, and so on). Never show internal names like pot or spend_base.

Suggested starting tokens (adjust freely, but keep the feel): background off-white warm, surface white, text deep green-grey, primary moss, accent sage, positive mint, caution muted amber, negative muted terracotta (never harsh red). Define colors as tokens with light and dark values.

## Principles

1. **One question per screen.** Each flow asks the user one thing at a time.
2. **Never block, always explain.** Show the consequence of an action inline before confirming ("this takes spending from $2000 to $1500"). Confirmations are only for deleting data.
3. **Always undoable.** Undo toasts everywhere.
4. **Everything has an explanation.** Any number can be tapped to see why ("why is my number $X").
5. **Review is fast.** A daily review should take under a minute for routine items.
6. **Honest about data freshness.** Show "data as of" when stale, and pending items clearly as pending.
7. **Offline-first.** The app opens instantly from cache and states clearly when it is offline.

## Core screens

**1. home (the hero).**
- Safe to spend (total, large) and per day (smaller).
- Quiet strip beneath: discretionary balance, "uncategorized" (tappable, only when non-zero), "owed to you" total (only when non-zero).
- Next few things: upcoming bills and expected income (cash timeline preview).
- A single prompt area for what needs attention (review count, an unresolved overspend, an overdue receivable). One at a time, not a pile.
- States: normal, overspent (negative total, per day 0, gentle cover prompt), learning (no plan yet), stale data, offline, pending-aware ("after pending: $X").

**2. review.**
- Card stack. Swipe right to confirm the suggested category, swipe left to recategorize.
- Each card: merchant, amount, date, account, suggested category with a short reason ("usually fuel for you"), top alternatives as chips, flags (unusual amount, new merchant, possible duplicate, possible reimbursement).
- Recategorize opens a picker, then **scope chips** with the engine's best guess pre-selected: this time only, this merchant, this merchant in this amount range, this account. Shows how many past transactions it would change.
- Review by merchant (batch mode).
- Done state: calm, short ("all caught up").
- Keyboard shortcuts on desktop.

**3. uncategorized (the pot).**
- Shows the amount and why it appeared ("last month's income was $600 higher", broken down by source when schedules exist).
- Allocate with sliders or amounts across: spending, savings, a goal, emergency fund, discretionary. One-time or permanent toggle per allocation with plain consequences.
- If untouched, says what will happen at month end (goes to savings).

**4. overspent (cover sheet).**
- "you're over by $42. where should it come from?"
- Source list with balances and the consequence of each (next month's spending, savings, goals, emergency fund, bills reserve, leave it). Splits allowed. Suggested order shown, never forced.

**5. owed to you.**
- List of receivables with counterparty, amount, age, and state (expected, tentative match, received). Actions: mark received, write off, link payment.
- Waiting-for-the-charge cards for held payments ("dad paid $11, waiting for the matching charge"), with "this was for something not posted yet".
- Counterparty profile cards.

**6. money (envelopes).**
- Savings, goals (progress, projected date), emergency fund (percent of target, rebuild plan), discretionary, sinking funds. Each tappable for history and explanation.
- Rollover transfer checklist ("move $X to savings account Y", tap to confirm).

**7. bills and subscriptions.**
- Upcoming calendar, reserve per bill, price change flags, missing bill alerts, annualized subscription cost, trials, payment plans, "mark as recurring".

**8. accounts and data health.**
- Accounts with kind, last sync, coverage, gaps, pending support per account, drift warnings, "assigned vs in your accounts", connection errors with clear next steps.

**9. rules and learning.**
- Rules list (user and learned), provenance, health, preview and backtest, the rules debugger (select a transaction, see every rule and why), reimbursement patterns, counterparties, accuracy dashboard.

**10. activity.**
- Search, saved views, bulk edit, transaction detail (splits, tags, notes, receipts, links, history of decisions), command palette.

**11. insights and reports.**
- Insights feed, month recap, reports, forecast and cash timeline, what-if sandbox, as-of view.

**12. settings.**
- Budget settings (savings, goals, emergency fund, standing lines), notification and alert catalog, privacy mode, security (passkeys, TOTP, sessions, tokens, panic lock), AI assistant (off by default), export and backup, tunable defaults.

**13. onboarding wizard.**
- Resumable steps: connect SimpleFIN, classify accounts, coverage check, detected recurring items, savings and goals and emergency fund setup, "teach me" queue, first plan reveal with a plain explanation, notification setup including the iOS Home Screen install guide.

## Play layer: the grove (spec section 19.25)

Gamified on purpose, calm in execution. The grove is a garden that grows from real finances and habits. It is a skin over the truth: it never changes a number and never gets in the way.

- **Where it lives:** a small grove card on home (optional, collapsible), a full-screen grove, the review "all caught up" moment, a quests board, an achievements shelf, and the season recap at month end.
- **Look:** soft, rounded, procedural plants and trees in the sage and moss palette. Seasons follow the real month. Light and dark variants. No heavy art assets: SVG generated from stable ids so everything is derived and reproducible.
- **Feel:** gentle growth animations, light haptics on supported devices, sound off by default. A celebration intensity slider (0 to 3), reduced motion respected, and a taper that quiets celebrations over time.
- **Voice:** lowercase, warm, understated. No exclamation points, no guilt, no countdowns, never the words failed, lost, broken, missed or behind. Examples: "all caught up. something grew." "you taught sofar 3 new merchants this week."
- **Never:** show a streak counter on home, rank the user against anyone, use random rewards, or imply that spending less is a win. On plan is a neutral state.
- **Screens and states:** grove (overview, tap a tree for its goal and history), quests board, achievements shelf with evidence, fluency view (the engine's real accuracy and coverage), streak as a growth ring with rest mode and freezes, season recap card (shareable, amounts hidden), play settings (off, quiet, full, per-component toggles, celebration slider, taper, reset, export).
- **Accessibility:** a text description of the grove for screen readers, keyboard navigation, no information conveyed by color alone, nothing time-limited.

## Components to design once

Hero number, per-day figure, grove card, tree and plant set, quest card, badge, fluency meter, season recap card, envelope card, allocation slider with consequence line, source picker, swipe review card, scope chips, merchant and category chips, receivable row, counterparty card, timeline strip, explanation sheet ("why is my number $X"), alert row, undo toast, data-freshness badge, empty and error states, skeletons.

## States every screen needs

Loading (skeleton), empty (calm copy, a single suggested next step), error (what happened and what to do), offline (cached with a visible badge, queued actions shown), stale data, partial coverage, privacy mode (amounts hidden until tapped).

## Accessibility and platform

- WCAG 2.2 AA, including contrast in both themes, focus order, and touch targets of at least 44px.
- Respect reduced motion. Swipe gestures always have a button equivalent.
- Keyboard-first on desktop: command palette, review shortcuts.
- iOS PWA constraints: push works only for the installed Home Screen app on iOS 16.4 or later, and there is no background sync, so the app refreshes on open. Provide an install guide in onboarding.
- Localization-ready: locale-aware currency and date formats, week start, right-to-left support.

## Deliverables expected from implementation

A component library with tokens and light and dark themes, each core screen with all states, and Playwright e2e for the main flows. The owner reviews the UI visually and on a phone at milestone M8 before further UI work.
