# legacy data migration and cutover

The owner's deployed sofar holds real decisions: reviews, categories, rules, links, a goal and the encrypted SimpleFIN access URL. This document is the plan for carrying them into the new schema **without destroying anything**. CLAUDE.md "ask first" applies: the owner must approve the dry run before `--apply` runs on their server.

## 1. the trap: table names collide

The legacy schema (`internal/db/schema.sql`) already uses `users`, `sessions`, `accounts`, `categories`, `rules`, `transactions`, `settings` and `push_subscriptions`. The new schema (architecture A3) reuses several of those names with different columns.

**Rule:** the first new migration moves every legacy table into a Postgres schema named `legacy`, and only then creates the new tables in `public`.

```sql
-- migrations/00001_legacy_quarantine.sql  (goose)
-- Runs only on a database that has the legacy layout (detected by transactions.review_status).
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = 'public' AND table_name = 'transactions' AND column_name = 'review_status') THEN
    CREATE SCHEMA IF NOT EXISTS legacy;
    -- move each legacy table with ALTER TABLE public.<name> SET SCHEMA legacy;
    -- (list them explicitly: users sessions simplefin connections accounts categories subcategories
    --  recurring_groups transactions rules reimbursement_links savings_goals budget_periods
    --  push_subscriptions settings applied_actions transaction_splits ignore_rules)
  END IF;
END $$;
```

Nothing is dropped. After the owner has used the new build for a while, they may drop `legacy` by hand. sofar never drops it.

## 2. the importer

`sofar import-legacy --dry-run` and `sofar import-legacy --apply` (in `internal/legacyimport`).

- **The dry run** reads `legacy.*`, builds everything in memory, and prints a report: counts per entity, every legacy category and how it maps, unmapped or ambiguous items, and transactions it would skip and why. It writes nothing.
- **Apply** runs in **one database transaction**, writes events with `actor = "legacy-import"`, and is idempotent: it is keyed by the legacy row id, so a second apply changes nothing.
- It runs before the first sync on the new build, so imported rows dedupe against later pulls by `(account, provider_txn_id)`.

## 3. mapping

### accounts
| legacy | new |
|---|---|
| `accounts.connection_id`, `external_id` | `provider_account_id = external_id`; the connection becomes a `provider_connections` row |
| `account_type = depository, subtype = checking` | kind `budget` |
| `subtype = savings` | kind `tracked` |
| `account_type = credit` | kind `budget` (card spending counts at swipe, S13) |
| `account_type = investment` | kind `balance_only` |
| `excluded_from_safe = true` | kind `tracked` (excluded from the plan) |
| `removed = true` | `active = false` |
| `nickname` | account display name override |

The importer prints the proposed kind per account. **The onboarding wizard asks the owner to confirm kinds** (`docs/discovery/known-facts.md`: two brokerage accounts were mis-guessed as checking).

### provider connection
`legacy.simplefin.access_url` is AES-GCM encrypted with `SOFAR_ENCRYPTION_KEY`. Keep the same key; copy the ciphertext as-is into `provider_connections.access_url_enc`. Never decrypt it in the importer.

### transactions
- Legacy ids are `<accountKey>:<simplefin id>`. `provider_txn_id` is the part after the first `:`. Manual rows (`manual = true`) become manual-provider transactions.
- Bank fields go to `raw_transactions`: date, amount and sign from `direction`, `description` (the raw bank text), and `raw_merchant` as the payee. `payload` is `{"imported_from":"legacy"}` (the original JSON was never stored). `bank_pending` maps to `pending`.
- User fields go to `transaction_overlays`: category (below), `note`, `hidden` (from `ignored`), `month_override` (none existed), and `reviewed_at` (set when `review_status = 'confirmed'`).
- **Each confirmed legacy review becomes a `label_event`** with `source = "user"` and `scope = "this_time"`. These are real user decisions, so they may train the engine (S18a principle 1). Give them weight 1, the quick-confirm weight, because the legacy app pre-filled them.
- `transaction_splits` become `txn_splits` with mapped categories.
- `reimbursement_links` become `links` with `state = confirmed` and `created_by = user`.

### categories (legacy id to S1a stable id)
Seed these S1a ids first (M1). The stable ids are fixed here so every session uses the same ones.

| legacy | new stable id | new bucket |
|---|---|---|
| housing | `rent_mortgage` | bills |
| utilities | `utilities` | bills |
| phone | `internet_phone` | bills |
| insurance | `insurance` | bills |
| loans | `loan_payments` | bills |
| subscriptions | `other_subscriptions` | subscriptions |
| fitness | `memberships` | subscriptions |
| education | `education` | spending |
| groceries | `groceries` | spending |
| dining, coffee | `dining_out` | spending |
| gas | `fuel` | spending |
| transport | `auto_transit` | spending |
| shopping | `shopping` | spending |
| home | `home_household` | spending |
| entertainment | `entertainment` | spending |
| health | `health_medical` | spending |
| personal-care | `personal_care` | spending |
| travel | `travel` | spending |
| gifts | `gifts_donations` | spending |
| fees | `fees_interest` | spending |
| pets | **custom** category `pets` in group `sp_shopping_home` | spending |
| savings-account | `general_savings` | savings |
| investing | `investments` | savings |
| group `expenses` only, no subcategory | `other_bills` | bills |
| group `spending` only | `other_spending` | spending |
| group `savings` only | `general_savings` | savings |
| income `salary` | `salary` | income |
| income `self-employed` | `self_employment` | income |
| income `other` | `interest` if the merchant normalizes to dividend or interest, else `gift_other` | income |
| income `transfer` | `transfer` | neutral |
| ignored, reason `transfer` | `transfer` | neutral |
| ignored, reason `card payment` | `credit_card_payment` | neutral |
| ignored, any other reason | `excluded`, with `hidden = true` | neutral |

### rules
- `legacy.rules (merchant_pattern, category_id, subcategory_id, enabled)` become **user rules**: stage `default`, condition `merchant contains <pattern>` (normalized), action `set category <mapped id>`, `enabled` kept, and provenance `legacy-import`.
- `legacy.ignore_rules` become user rules with the action `hide`.

### recurring
- Confirmed, not dismissed `recurring_groups` become schedules (kind `interval`; `cadence` maps to 7, 14, ~30.44 or ~365.25 days, or a monthly calendar rule on the day of `next_date`) plus a bill, subscription (category `memberships` or `*_subscriptions`) or income stream, with `amount_tolerance_pct` and the expected amount.
- Dismissed groups become a "not recurring" decision for that merchant, so detection does not ask again.
- Unconfirmed candidates are dropped; detection will find them again.

### goal, settings, people
- The active `savings_goals` row becomes a goal (name, target, monthly). Its legacy "saved" figure becomes an **opening balance** event for the goal envelope (S8 allows an opening balance for the EF; use the same mechanism for goals).
- `settings.recurring_occurrence_threshold` becomes the config default. `last_sync` is not carried.
- `users` (username, password hash, TOTP secret) carry over, so the owner logs in with the same credentials. `sessions` do not: everyone signs in again.
- `push_subscriptions` carry over.

## 4. cutover runbook (owner runs it, with the agent's help)

1. On `main`, tag the last legacy build: `git tag legacy-final`.
2. On the server, back up: `docker compose exec -T db pg_dump -U sofar sofar | gzip > sofar-pre-rebuild.sql.gz`, and copy the file off the server.
3. Restore that dump into a **scratch** database on a dev machine, start the rebuild build against it, and run `sofar import-legacy --dry-run`. Review the report with the owner.
4. Run `--apply` on the scratch copy and click through the app. Fix the importer until the owner is happy.
5. On the server: deploy the rebuild image (migration `00001` quarantines legacy tables), run `import-legacy --dry-run`, then `--apply`.
6. **Rollback:** redeploy the `legacy-final` image and restore the dump from step 2.

## 5. tests

- `TestLegacyImport_DryRunWritesNothing`
- `TestLegacyImport_ApplyIsIdempotent`
- `TestLegacyImport_CategoryMappingCoversEveryLegacyCategory`
- `TestLegacyImport_QuarantineMovesTablesWithoutDataLoss`, using a fixture database built from the real legacy `schema.sql` plus seeded rows
- `TestLegacyImport_ReviewedRowsBecomeUserLabels`
- `TestLegacyImport_AccessURLCopiedNotDecrypted`
