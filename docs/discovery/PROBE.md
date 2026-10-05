# SimpleFIN data-discovery probe

The spec makes several assumptions about SimpleFIN behavior that only real data can confirm. This probe measures them. Do it early (as soon as the repo audit is done), because the answers shape sync, pending handling and matching.

## Safety rules (read first)

- **Read-only.** The probe only reads. Never write, never trigger anything beyond the documented SimpleFIN GET endpoints.
- **Credential:** the access URL comes only from the environment variable `SIMPLEFIN_ACCESS_URL`. Never write it to a file, print it, commit it, or read `.env` files. If it is missing, use the public demo access URL from the SimpleFIN documentation and say so in the report, then stop and tell the owner what could not be measured.
- **Request budget:** the provider allows about 24 requests per day per access URL (plus per-account limits). The probe must enforce a **hard cap of 12 requests per run** and a token bucket across runs, and must stop on any quota warning. Prefer `balances-only` and per-account requests. Never retry in a loop.
- **Raw output stays outside the repo:** write raw responses to `~/.sofar-probe/raw/` (outside the repo, owner-only permissions). Only **redacted** output may be committed.
- **Redaction for committed fixtures:** replace names, account numbers and ids with stable pseudonyms; keep description structure (token shapes, prefixes, processors) but replace personal names; jitter amounts by a small random percentage while preserving sign, relative size and fraction relationships (for example a half-split stays a half-split); shift dates by a constant offset per fixture file while keeping weekdays and intervals. Never commit real descriptions containing names.
- Log nothing sensitive. Print only aggregate counts and field-presence flags.

## What to build

A small Go program at `tools/probe` with two modes.

**`probe snapshot`** (one-time, at most 12 requests):
1. List accounts and connections (`balances-only`), record institution, account type, currency, `balance`, `available-balance`, `balance-date`, and any `errlist` entries.
2. For each account, one request with `pending=1`, as wide a window as the provider allows. If the provider returns a window-size warning, record the exact warning and the size it asks for, then re-request within budget.
3. One repeat request for one or two accounts over the same window **without** `pending=1`, to compare.
4. Write raw responses to `~/.sofar-probe/raw/`, then produce the redacted report and fixtures.

**`probe observe`** (scheduled, 2 requests per day, for 7 to 14 days):
- Run twice a day. Each run records, per account, a redacted delta against the previous run: new transactions, transactions that changed (pending to posted, amount changes, description changes), transactions that disappeared, id changes, balance and available balance changes, and the observed time the data changed.
- Append to `~/.sofar-probe/observe.jsonl` (outside the repo). Provide a `probe report` command that summarizes it.

## What to measure

For each account and institution, report:

1. **Fields present** on transactions: payee, memo, MCC, `transacted_at`, `extra`. For each, the share of transactions where it is non-empty.
2. **Pending behavior:** does `pending=1` return pending items? What do they look like (`pending: true`, `posted = 0`)? Do pending items change amount or id when they post (tips)?
3. **History depth** per account, and the working window size per request. Capture exact warning text.
4. **Id stability:** are ids identical across repeat pulls? Any re-issued ids (same posted, amount, description, payee with a new id)?
5. **Balance semantics:** how `available-balance` relates to `balance` for checking, savings and card accounts, and whether the gap matches visible pending items (this decides whether the hidden-pending hint is usable per account).
6. **Holdings:** do investment accounts return holdings, and which fields? Does the account balance already include holdings value?
7. **Posting lag:** for card purchases, the time between `transacted_at` (or first sighting) and posting. For P2P, how fast do Zelle or Venmo items appear?
8. **P2P descriptors:** how Zelle, Venmo and Cash App transactions appear (description, payee, memo), including whether counterparty names are present, masked or generic. Record shapes only, with names pseudonymized.
9. **Error codes:** every `errlist` code seen in normal operation.
10. **Refresh timing:** when during the day each account's data changes.

## Deliverables

- `docs/discovery/report.md`: findings per item above, per account (pseudonymized account names), with a short "implications for sofar" section.
- `testdata/simplefin/*.json`: redacted fixtures covering normal pull, pending present, pending absent, id rotation (if seen), window warning, `errlist` entries, missing optional fields, P2P shapes.
- `docs/spec-amendments.md` entries for any finding that contradicts the spec.
- Tests that load the fixtures (these become the sync client test suite).

## How findings change the design

| Finding | Action |
|---|---|
| A bank never returns pending items even with `pending=1` | Held payments, late matching and receivables are the primary path for that account. Disable "link to pending" for it. |
| Pending items change id when they post | Pending to posted reconcile must use account, merchant, date window and amount tolerance, not id. |
| Ids are re-issued | Id-rotation heal is mandatory, not optional. Add the content fingerprint as a first-class key. |
| `transacted_at` is missing | Date by `posted`, and widen the late-match window by the account's observed posting lag. |
| `payee` or `memo` is missing or generic | Lean on description parsing for counterparty and merchant normalization. Raise the importance of receivables for paybacks. |
| `available-balance` does not track pending holds | Disable the hidden-pending hint for that account. |
| History depth is short (for example 30 or 45 days) | The learning phase is longer. Make the onboarding wizard recommend CSV/OFX import for older history. |
| Window cap is smaller than 90 days | Set the chunk size from the observed cap, per provider and account. |
| Holdings are absent or inconsistent | Mark the account balance-only, do not show holdings. |

## Done when

The report exists, fixtures are committed (redacted), amendments are filed, and the sync client test suite runs against the fixtures. Then tell the owner what you found and what changed in the design.
