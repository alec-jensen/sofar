-- +goose Up
-- Transfers (S12, S13): what kind of account it is, and every pairing or
-- one-sided decision so a pair the user dissolved is never made again.
ALTER TABLE accounts ADD COLUMN subtype text NOT NULL DEFAULT ''
  CHECK (subtype IN ('', 'checking', 'savings', 'credit_card', 'investment', 'loan'));

CREATE TABLE transfer_links (
  id bigserial PRIMARY KEY,
  txn_a uuid NOT NULL REFERENCES raw_transactions(id),
  txn_b uuid REFERENCES raw_transactions(id),      -- null for a one-sided decision
  kind text NOT NULL CHECK (kind IN ('pair', 'one_sided')),
  choice text NOT NULL DEFAULT '' CHECK (choice IN ('', 'neutral', 'savings', 'spending')),
  state text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'dissolved')),
  created_by text NOT NULL DEFAULT 'auto' CHECK (created_by IN ('auto', 'user')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX transfer_links_a ON transfer_links (txn_a);
CREATE UNIQUE INDEX transfer_links_b ON transfer_links (txn_b) WHERE txn_b IS NOT NULL;

-- +goose Down
DROP TABLE transfer_links;
ALTER TABLE accounts DROP COLUMN subtype;
