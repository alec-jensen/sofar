CREATE TABLE IF NOT EXISTS users (
 id integer PRIMARY KEY CHECK(id=1), username text UNIQUE NOT NULL, password_hash text NOT NULL,
 totp_secret text, totp_pending text, totp_last_step bigint NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (token_hash text PRIMARY KEY, user_id integer NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS plaid_items (id text PRIMARY KEY, access_token text NOT NULL, cursor text NOT NULL DEFAULT '', institution_name text NOT NULL, kind text NOT NULL, last_error text);
CREATE TABLE IF NOT EXISTS accounts (
 id text PRIMARY KEY, plaid_item_id text NOT NULL REFERENCES plaid_items(id), plaid_account_id text UNIQUE NOT NULL,
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
