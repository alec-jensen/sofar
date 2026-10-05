-- +goose Up
-- The immutable ledger (raw bank data), user overlays, categories and import batches.
-- Bank columns on raw_transactions are never changed by user actions (S11).

CREATE TABLE accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL,
  connection_id text NOT NULL DEFAULT '',
  provider_account_id text NOT NULL,
  name text NOT NULL,
  display_name_override text,
  institution text NOT NULL DEFAULT '',
  mask text NOT NULL DEFAULT '',
  currency text NOT NULL DEFAULT 'USD',
  kind text NOT NULL CHECK (kind IN ('budget','tracked','balance_only','liability')),
  active boolean NOT NULL DEFAULT true,
  opened_at date,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, connection_id, provider_account_id)
);

CREATE TABLE import_batches (
  id bigserial PRIMARY KEY,
  source text NOT NULL,
  filename text NOT NULL DEFAULT '',
  account_id uuid REFERENCES accounts(id),
  status text NOT NULL DEFAULT 'dry_run' CHECK (status IN ('dry_run','applied','undone')),
  summary jsonb NOT NULL DEFAULT '{}',
  parsed jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  applied_event_id bigint REFERENCES events(id)
);

CREATE TABLE import_presets (
  id bigserial PRIMARY KEY,
  name text NOT NULL UNIQUE,
  institution text NOT NULL DEFAULT '',
  format text NOT NULL,
  mapping jsonb NOT NULL DEFAULT '{}'
);

-- Bank dates are calendar dates (S3). *_on holds the date; *_instant is set only
-- when the provider gave a real timestamp, which is then converted to the home
-- zone when deciding the month.
CREATE TABLE raw_transactions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES accounts(id),
  provider_txn_id text NOT NULL,
  posted_on date NOT NULL,
  posted_instant timestamptz,
  transacted_on date,
  transacted_instant timestamptz,
  amount bigint NOT NULL,
  description text NOT NULL DEFAULT '',
  payee text NOT NULL DEFAULT '',
  memo text NOT NULL DEFAULT '',
  mcc text NOT NULL DEFAULT '',
  pending boolean NOT NULL DEFAULT false,
  payload jsonb NOT NULL DEFAULT '{}',
  fingerprint text NOT NULL DEFAULT '',
  missing_from_bank_at timestamptz,
  import_batch_id bigint REFERENCES import_batches(id),
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, provider_txn_id)
);
CREATE INDEX raw_transactions_fingerprint_idx ON raw_transactions (account_id, fingerprint);
CREATE INDEX raw_transactions_posted_idx ON raw_transactions (account_id, posted_on DESC);
CREATE INDEX raw_transactions_batch_idx ON raw_transactions (import_batch_id) WHERE import_batch_id IS NOT NULL;

CREATE TABLE txn_id_aliases (
  account_id uuid NOT NULL REFERENCES accounts(id),
  old_id text NOT NULL,
  new_id text NOT NULL,
  PRIMARY KEY (account_id, old_id)
);

-- Survives deletion of the raw row, so sync never re-imports a hidden transaction.
CREATE TABLE hidden_tombstones (
  account_id uuid NOT NULL,
  provider_txn_id text NOT NULL,
  hidden_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, provider_txn_id)
);

-- Categories: bucket -> group -> category, stable text ids (S1a).
CREATE TABLE category_groups (
  id text PRIMARY KEY,
  name text NOT NULL,
  bucket text NOT NULL CHECK (bucket IN ('income','bills','subscriptions','savings','spending','neutral')),
  sort integer NOT NULL DEFAULT 0,
  is_default boolean NOT NULL DEFAULT false,
  soft_target jsonb
);

CREATE TABLE categories (
  id text PRIMARY KEY,
  group_id text NOT NULL REFERENCES category_groups(id),
  name text NOT NULL,
  sort integer NOT NULL DEFAULT 0,
  is_default boolean NOT NULL DEFAULT false,
  soft_target jsonb
);
CREATE INDEX categories_group_idx ON categories (group_id);

CREATE TABLE transaction_overlays (
  raw_txn_id uuid PRIMARY KEY REFERENCES raw_transactions(id),
  category_id text REFERENCES categories(id),
  month_override date,
  note text NOT NULL DEFAULT '',
  tags text[] NOT NULL DEFAULT '{}',
  hidden boolean NOT NULL DEFAULT false,
  from_discretionary boolean NOT NULL DEFAULT false,
  type text,
  reviewed_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE txn_splits (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  raw_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  part_no integer NOT NULL,
  amount bigint NOT NULL,
  category_id text NOT NULL REFERENCES categories(id),
  tags text[] NOT NULL DEFAULT '{}',
  note text NOT NULL DEFAULT '',
  UNIQUE (raw_txn_id, part_no)
);

CREATE TABLE merchants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  canonical_key text NOT NULL UNIQUE,
  ambiguous boolean NOT NULL DEFAULT false,
  default_category_id text REFERENCES categories(id)
);

CREATE TABLE merchant_aliases (
  alias text PRIMARY KEY,
  merchant_id uuid NOT NULL REFERENCES merchants(id)
);

-- Default groups. Ids are permanent; renaming never changes them.
INSERT INTO category_groups (id, name, bucket, sort, is_default) VALUES
  ('inc_earned',        'earned',               'income',        10, true),
  ('inc_passive',       'passive',              'income',        20, true),
  ('inc_other',         'other income',         'income',        30, true),
  ('bl_housing',        'housing & utilities',  'bills',         10, true),
  ('bl_insurance_debt', 'insurance & debt',     'bills',         20, true),
  ('bl_other',          'other',                'bills',         30, true),
  ('sub_subscriptions', 'subscriptions',        'subscriptions', 10, true),
  ('sv_core',           'core savings',         'savings',       10, true),
  ('sv_goals',          'goals',                'savings',       20, true),
  ('sp_food_dining',    'food & dining',        'spending',      10, true),
  ('sp_transportation', 'transportation',       'spending',      20, true),
  ('sp_shopping_home',  'shopping & home',      'spending',      30, true),
  ('sp_lifestyle',      'lifestyle',            'spending',      40, true),
  ('sp_health_edu',     'health & education',   'spending',      50, true),
  ('sp_business_fin',   'business & financial', 'spending',      60, true),
  ('sp_other',          'other',                'spending',      70, true),
  ('nt_transfers',      'transfers',            'neutral',       10, true),
  ('nt_links',          'links',                'neutral',       20, true),
  ('nt_excluded',       'excluded',             'neutral',       30, true);

INSERT INTO categories (id, group_id, name, sort, is_default) VALUES
  ('salary',              'inc_earned',        'salary',                 10, true),
  ('self_employment',     'inc_earned',        'self-employment',        20, true),
  ('interest',            'inc_passive',       'interest',               10, true),
  ('rewards_cashback',    'inc_passive',       'rewards / cashback',     20, true),
  ('one_time_income',     'inc_other',         'one-time income',        10, true),
  ('gift_other',          'inc_other',         'gift / other',           20, true),
  ('asset_sale',          'inc_other',         'asset sale',             30, true),
  ('rent_mortgage',       'bl_housing',        'rent / mortgage',        10, true),
  ('utilities',           'bl_housing',        'utilities',              20, true),
  ('internet_phone',      'bl_housing',        'internet & phone',       30, true),
  ('insurance',           'bl_insurance_debt', 'insurance',              10, true),
  ('loan_payments',       'bl_insurance_debt', 'loan payments',          20, true),
  ('other_bills',         'bl_other',          'other bills',            10, true),
  ('streaming_media',     'sub_subscriptions', 'streaming & media',      10, true),
  ('software_cloud',      'sub_subscriptions', 'software & cloud',       20, true),
  ('memberships',         'sub_subscriptions', 'memberships',            30, true),
  ('other_subscriptions', 'sub_subscriptions', 'other subscriptions',    40, true),
  ('general_savings',     'sv_core',           'general savings',        10, true),
  ('emergency_fund',      'sv_core',           'emergency fund',         20, true),
  ('investments',         'sv_core',           'investments',            30, true),
  ('debt_payoff',         'sv_goals',          'debt payoff',            10, true),
  ('groceries',           'sp_food_dining',    'groceries',              10, true),
  ('dining_out',          'sp_food_dining',    'dining out',             20, true),
  ('fuel',                'sp_transportation', 'fuel',                   10, true),
  ('auto_transit',        'sp_transportation', 'auto & transit',         20, true),
  ('shopping',            'sp_shopping_home',  'shopping',               10, true),
  ('home_household',      'sp_shopping_home',  'home & household',       20, true),
  ('entertainment',       'sp_lifestyle',      'entertainment',          10, true),
  ('travel',              'sp_lifestyle',      'travel',                 20, true),
  ('personal_care',       'sp_lifestyle',      'personal care',          30, true),
  ('gifts_donations',     'sp_lifestyle',      'gifts & donations',      40, true),
  ('health_medical',      'sp_health_edu',     'health & medical',       10, true),
  ('education',           'sp_health_edu',     'education',              20, true),
  ('business_expenses',   'sp_business_fin',   'business expenses',      10, true),
  ('fees_interest',       'sp_business_fin',   'fees & interest',        20, true),
  ('cash_atm',            'sp_business_fin',   'cash & atm',             30, true),
  ('other_spending',      'sp_other',          'other',                  10, true),
  ('transfer',            'nt_transfers',      'transfer',               10, true),
  ('credit_card_payment', 'nt_transfers',      'credit card payment',    20, true),
  ('loan_proceeds',       'nt_transfers',      'loan proceeds',          30, true),
  ('reimbursement',       'nt_links',          'reimbursement',          10, true),
  ('refund',              'nt_links',          'refund',                 20, true),
  ('excluded',            'nt_excluded',       'excluded',               10, true);

-- +goose Down
DROP TABLE merchant_aliases, merchants, txn_splits, transaction_overlays, categories, category_groups,
  hidden_tombstones, txn_id_aliases, raw_transactions, import_presets, import_batches, accounts;
