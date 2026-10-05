CREATE TABLE IF NOT EXISTS users (
 id integer PRIMARY KEY CHECK(id=1), username text UNIQUE NOT NULL, password_hash text NOT NULL,
 totp_secret text, totp_pending text, totp_last_step bigint NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (token_hash text PRIMARY KEY, user_id integer NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL);
-- One SimpleFIN access URL (encrypted) covers every bank linked in SimpleFIN Bridge.
CREATE TABLE IF NOT EXISTS simplefin (
 id integer PRIMARY KEY CHECK(id=1), access_url text NOT NULL, bridge_url text NOT NULL DEFAULT '',
 backfilled boolean NOT NULL DEFAULT false, last_fetch timestamptz, last_error text
);
CREATE TABLE IF NOT EXISTS connections (id text PRIMARY KEY, name text NOT NULL, org_url text NOT NULL DEFAULT '', last_error text);
CREATE TABLE IF NOT EXISTS accounts (
 id text PRIMARY KEY, connection_id text NOT NULL DEFAULT '', external_id text NOT NULL DEFAULT '',
 institution_name text NOT NULL, account_type text NOT NULL, account_subtype text NOT NULL DEFAULT '', display_name text NOT NULL,
 mask text NOT NULL DEFAULT '', balance bigint NOT NULL DEFAULT 0, last_synced_at timestamptz
);
CREATE TABLE IF NOT EXISTS categories (id text PRIMARY KEY CHECK(id IN ('expenses','spending','savings')), name text NOT NULL, is_savings_category boolean NOT NULL DEFAULT false);
INSERT INTO categories VALUES ('expenses','expenses',false),('spending','spending',false),('savings','savings',true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS subcategories (id text PRIMARY KEY, name text NOT NULL, group_id text NOT NULL REFERENCES categories(id), monthly_plan bigint NOT NULL DEFAULT 0 CHECK(monthly_plan>=0));
CREATE TABLE IF NOT EXISTS recurring_groups (
 id text PRIMARY KEY, merchant_pattern text NOT NULL, expected_amount bigint NOT NULL CHECK(expected_amount>=0), amount_tolerance_pct numeric NOT NULL DEFAULT 10 CHECK(amount_tolerance_pct BETWEEN 0 AND 100),
 cadence text NOT NULL CHECK(cadence IN ('weekly','biweekly','monthly','annual')), type text NOT NULL CHECK(type IN ('bill','income')),
 income_stream text CHECK(income_stream IN ('salary','self-employed')), occurrence_threshold_at_creation integer NOT NULL,
 category_id text REFERENCES categories(id), confirmed boolean NOT NULL DEFAULT false, next_date date NOT NULL, mismatch boolean NOT NULL DEFAULT false, dismissed boolean NOT NULL DEFAULT false,
 UNIQUE(merchant_pattern,type)
);
ALTER TABLE recurring_groups ADD COLUMN IF NOT EXISTS dismissed boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS transactions (
 id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id), date date NOT NULL, amount bigint NOT NULL CHECK(amount>=0),
 raw_merchant text NOT NULL, clean_merchant text NOT NULL, category_id text REFERENCES categories(id),
 is_recurring boolean NOT NULL DEFAULT false, recurring_group_id text REFERENCES recurring_groups(id),
 review_status text NOT NULL DEFAULT 'pending' CHECK(review_status IN ('pending','confirmed')), direction text NOT NULL CHECK(direction IN ('in','out')),
 income_stream text CHECK(income_stream IN ('salary','self-employed','transfer')), bank_pending boolean NOT NULL DEFAULT false
);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS subcategory_id text REFERENCES subcategories(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS transactions_date_idx ON transactions(date DESC);
CREATE TABLE IF NOT EXISTS rules (merchant_pattern text PRIMARY KEY, category_id text NOT NULL REFERENCES categories(id));
ALTER TABLE rules ADD COLUMN IF NOT EXISTS subcategory_id text REFERENCES subcategories(id) ON DELETE SET NULL;
ALTER TABLE rules ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
CREATE TABLE IF NOT EXISTS reimbursement_links (
 id text PRIMARY KEY, expense_transaction_id text NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
 reimbursement_transaction_id text NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
 amount_netted bigint NOT NULL CHECK(amount_netted>0), created_at timestamptz NOT NULL DEFAULT now(), CHECK(expense_transaction_id<>reimbursement_transaction_id)
);
CREATE TABLE IF NOT EXISTS savings_goals (id text PRIMARY KEY, name text NOT NULL, target_amount bigint CHECK(target_amount>=0), target_monthly_contribution bigint NOT NULL CHECK(target_monthly_contribution>=0), is_active boolean NOT NULL DEFAULT true);
INSERT INTO savings_goals VALUES ('first','a little breathing room',1000000,0,true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS budget_periods (id text PRIMARY KEY, period_start date NOT NULL, period_end date NOT NULL, salary_baseline_amount bigint NOT NULL, self_employed_baseline_amount bigint NOT NULL, computed_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS push_subscriptions (endpoint text PRIMARY KEY, user_id integer NOT NULL REFERENCES users(id), keys jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS settings (key text PRIMARY KEY, value text NOT NULL);
INSERT INTO settings VALUES ('recurring_occurrence_threshold','3'),('last_sync','1970-01-01T00:00:00Z') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS applied_actions (id text PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS ignored boolean NOT NULL DEFAULT false;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS ignore_reason text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS transaction_splits (
 seq bigserial PRIMARY KEY, transaction_id text NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
 category_id text NOT NULL REFERENCES categories(id), subcategory_id text REFERENCES subcategories(id) ON DELETE SET NULL,
 amount bigint NOT NULL CHECK(amount>0)
);
CREATE INDEX IF NOT EXISTS transaction_splits_tx_idx ON transaction_splits(transaction_id);
CREATE TABLE IF NOT EXISTS ignore_rules (merchant_pattern text PRIMARY KEY);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS excluded_from_safe boolean NOT NULL DEFAULT false;
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS needs_reauth boolean NOT NULL DEFAULT false;
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS nickname text;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS manual boolean NOT NULL DEFAULT false;
UPDATE transactions SET manual=true WHERE manual=false AND id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND category_id='savings' AND review_status='confirmed' AND direction='out';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS connection_id text NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS external_id text NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS removed boolean NOT NULL DEFAULT false;
-- Plaid was replaced by SimpleFIN: drop its connections and the data they imported.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='accounts' AND column_name='plaid_item_id') THEN
  UPDATE transactions SET review_status='pending',category_id=NULL,subcategory_id=NULL,income_stream=NULL WHERE id IN (SELECT l.reimbursement_transaction_id FROM reimbursement_links l JOIN transactions t ON t.id=l.expense_transaction_id JOIN accounts a ON a.id=t.account_id WHERE a.plaid_item_id IS NOT NULL);
  DELETE FROM transactions WHERE account_id IN (SELECT id FROM accounts WHERE plaid_item_id IS NOT NULL);
  DELETE FROM accounts WHERE plaid_item_id IS NOT NULL;
  ALTER TABLE accounts DROP COLUMN plaid_item_id;
  ALTER TABLE accounts DROP COLUMN IF EXISTS plaid_account_id;
 END IF;
END $$;
DROP TABLE IF EXISTS plaid_items;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_income_stream_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_income_stream_check CHECK(income_stream IN ('salary','self-employed','transfer','other'));
