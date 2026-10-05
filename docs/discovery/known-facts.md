# known facts from the legacy app (before the probe)

The legacy app has synced the owner's real SimpleFIN data since 2026-09-30. These observations came out of that work. They are **not** a substitute for `docs/discovery/PROBE.md`; the probe must still measure everything properly. They are recorded so the first sessions do not design around assumptions that are already known to be wrong. Names, employers and card digits are pseudonymized.

## connection

- The access URL host is `beta-bridge.simplefin.org`. Allowlisting `simplefin.org` and its subdomains (as `server/netguard.go` does) covers it.
- Two institutions: a credit union (checking and savings) and a brokerage (three accounts).

## window size

- Requesting more than 45 days returns data **and** an `errlist` entry with the message: `Requested date range exceeds recommended range of 45 days. In the future, this may be capped.` The legacy client stored this as a connection error. It is a warning, not a failure (S15: treat date-range cap warnings as non-fatal). The error code was not recorded; the probe should capture it.
- The legacy client therefore requests 44-day recent windows and 45-day backfill windows.

## history depth (short, and different per account)

- Credit union checking: earliest transaction about 12 days before the first sync.
- Credit union savings and the three brokerage accounts: about 3 months.
- Implication (PROBE.md table): the learning phase will be long and coverage differs by account, so S18 coverage logic and S13 "partial" months matter from day one. The onboarding wizard should recommend CSV/OFX import for older history.

## pending

- No pending rows were seen in any account across several pulls with `pending=1`. That does not prove the institutions never send them, but plan for the "pending never provided" path (ADR-0007) to be the **primary** path for these accounts.

## transaction fields

- `payee` is often present and already cleaned (for example a merchant's proper name). `description` is long and raw, for example:
  `Withdrawal Master Money <PROCESSOR> *<MERCHANT> <STREET ADDRESS> XXXXXXX944 <ST> Date 09/28/26 <REF> 5732 Card 16 #<4 digits>`.
  It contains a date, a reference, what looks like an MCC (`5732`), partial card digits and the merchant's address. **Redact descriptions in logs and fixtures** (CLAUDE.md "bank strings are untrusted", architecture A8).
- Debit-card descriptions at the credit union carry a `Master Money` prefix. Add it to normalization's processor prefixes (it is already in `merchants.go`).
- `transacted_at`: not recorded by the legacy app. The probe must check it.

## transfer and p2p shapes

- Between the owner's own credit-union accounts: `Transfer from Share 30` and `Transfer to Share 01` (share numbers identify the accounts).
- Person to person, inbound: `Transfer from <LAST>,<FIRST> <MI>`. Outbound: `Transfer to <First> <MI> <Last>`. Counterparty names are present in the description.
- Brokerage cash activity appears as same-day paired in/out rows: `Electronic Funds Transfer Paid` / `Received`, `Investment Buy`, `Dividend`, and rows simply named after the brokerage (sweeps).

## accounts

- Two brokerage accounts have names (`College (NNNN)`, `Individual (NNNN)`) that do not reveal they are investment accounts. Name heuristics guessed "checking". **Account kind must be confirmed by the user** in onboarding (S19.7 step 2), with heuristics only as a suggestion.
- One retirement account reports a balance of `0.00`. Unknown whether that is real or a provider gap. The probe should check holdings and balance dates.

## income

- Payroll arrives as a deposit whose description is the employer's name (no "payroll" keyword). Keyword-based income detection does not work for it; the user labels it once, and the schedule engine learns it (S14).
- Small `Dividend` and interest credits occur monthly in savings and brokerage accounts.
