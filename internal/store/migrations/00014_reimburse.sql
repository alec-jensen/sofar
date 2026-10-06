-- +goose Up
-- Reimbursement matching (S12a, S12b): held payments, link suggestions, learned
-- patterns, payer labels and the decision log that makes every match reproducible.
CREATE TABLE held_payments (
  txn_id uuid PRIMARY KEY REFERENCES raw_transactions(id),
  state text NOT NULL CHECK (state IN ('awaiting_expense', 'unlinked_reimbursement', 'needs_decision', 'resolved')),
  since date NOT NULL,
  grace_until date NOT NULL,
  hold_until date NOT NULL,
  declared boolean NOT NULL DEFAULT false,      -- "this was for something not posted yet"
  hint_merchant text NOT NULL DEFAULT '',
  hint_amount bigint,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE link_suggestions (
  payment_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  expense_txn_id uuid REFERENCES raw_transactions(id),
  receivable_id uuid REFERENCES receivables(id),
  amount bigint NOT NULL,
  confidence double precision NOT NULL,
  reasons jsonb NOT NULL DEFAULT '[]',
  rank integer NOT NULL,
  grp integer NOT NULL DEFAULT 0,          -- above 0: these rows together cover one payment (several expenses)
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (payment_txn_id, rank)
);

CREATE TABLE reimbursement_patterns (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  counterparty_id uuid NOT NULL REFERENCES counterparties(id),
  category_id text NOT NULL DEFAULT '',
  merchant_key text NOT NULL DEFAULT '',
  share_ratio double precision NOT NULL,
  lag_min integer NOT NULL DEFAULT 0,
  lag_max integer NOT NULL DEFAULT 7,
  support integer NOT NULL DEFAULT 1,
  streak integer NOT NULL DEFAULT 1,
  strikes integer NOT NULL DEFAULT 0,
  state text NOT NULL DEFAULT 'probation' CHECK (state IN ('probation', 'active', 'demoted', 'disabled')),
  user_authored boolean NOT NULL DEFAULT false,
  last_confirmed_on date,
  note text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reimbursement_patterns_cp ON reimbursement_patterns (counterparty_id);

CREATE TABLE payer_labels (
  txn_id uuid PRIMARY KEY REFERENCES raw_transactions(id),
  counterparty_id uuid REFERENCES counterparties(id),
  label text NOT NULL CHECK (label IN ('reimbursement', 'other')),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE link_events (
  id bigserial PRIMARY KEY,
  payment_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  expense_txn_id uuid REFERENCES raw_transactions(id),
  receivable_id uuid REFERENCES receivables(id),
  link_id bigint REFERENCES links(id),
  pattern_id uuid REFERENCES reimbursement_patterns(id),
  counterparty_id uuid REFERENCES counterparties(id),
  amount bigint,
  expense_total bigint,
  lag_days integer,
  category_id text NOT NULL DEFAULT '',
  source text NOT NULL,                 -- receivable, pattern, model, manual
  outcome text NOT NULL CHECK (outcome IN ('auto_tentative', 'suggested', 'abstained', 'confirmed', 'rejected', 'unlinked', 'manual', 'held', 'dissolved')),
  confidence double precision,
  features jsonb NOT NULL DEFAULT '{}',   -- the pair's features at decision time, for reproducibility
  model_version integer NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX link_events_payment ON link_events (payment_txn_id);

-- +goose Down
DROP TABLE link_events, payer_labels, reimbursement_patterns, link_suggestions, held_payments;
