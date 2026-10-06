-- +goose Up
-- Categorization: live suggestions, every label the user gives, rule exceptions,
-- model snapshots and the prequential evaluation log (S18a).
CREATE TABLE suggestions (
  txn_id uuid PRIMARY KEY REFERENCES raw_transactions(id),
  category_id text NOT NULL REFERENCES categories(id),
  source text NOT NULL,
  confidence double precision NOT NULL,
  reason text NOT NULL DEFAULT '',
  alternatives jsonb NOT NULL DEFAULT '[]',
  rule_id uuid REFERENCES rules(id),
  flags jsonb NOT NULL DEFAULT '[]',
  model_version integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE label_events (
  id bigserial PRIMARY KEY,
  txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  suggested text,
  source text NOT NULL,           -- what produced the suggestion: rule, learned, model, memory, prior, none
  confidence double precision,
  final text NOT NULL,
  scope text NOT NULL DEFAULT 'this_time',
  weight double precision NOT NULL DEFAULT 1,
  session_id text NOT NULL DEFAULT '',
  dwell_ms integer,
  merchant_key text NOT NULL DEFAULT '',
  amount bigint,
  account_id uuid,
  rule_id uuid,
  label_source text NOT NULL DEFAULT 'user',   -- user or legacy-import: only these train (invariant 12)
  undone boolean NOT NULL DEFAULT false,
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX label_events_txn_idx ON label_events (txn_id, at);
CREATE INDEX label_events_at_idx ON label_events (at);
CREATE INDEX label_events_merchant_idx ON label_events (merchant_key) WHERE NOT undone;

CREATE TABLE rule_exceptions (
  txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  rule_id uuid NOT NULL REFERENCES rules(id),
  PRIMARY KEY (txn_id, rule_id)
);

CREATE TABLE model_snapshots (
  id bigserial PRIMARY KEY,
  kind text NOT NULL,
  version integer NOT NULL,
  weights bytea NOT NULL,
  trained_through timestamptz,
  metrics jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (kind, version)
);

-- Every suggestion is stored before its label arrives (prequential evaluation).
CREATE TABLE suggestion_log (
  id bigserial PRIMARY KEY,
  txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  category_id text NOT NULL,
  source text NOT NULL,
  confidence double precision NOT NULL,
  model_version integer NOT NULL DEFAULT 0,
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX suggestion_log_txn_idx ON suggestion_log (txn_id, at);

-- +goose Down
DROP TABLE suggestion_log, model_snapshots, rule_exceptions, label_events, suggestions;
