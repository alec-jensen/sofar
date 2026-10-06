-- +goose Up
-- Counterparties (S12b) and receivables (S12a): who pays the user back, and what
-- they owe. Held payments come with the late-matching migration.
CREATE TABLE counterparties (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  policy text NOT NULL DEFAULT 'ask' CHECK (policy IN ('ask', 'always_reimbursement', 'never')),
  auto_net boolean NOT NULL DEFAULT false,
  merged_into uuid REFERENCES counterparties(id),
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- an alias is the normalized words of a payer string, in sorted order; an alias
-- belongs to exactly one counterparty
CREATE TABLE counterparty_aliases (
  id bigserial PRIMARY KEY,
  alias_key text NOT NULL UNIQUE,
  counterparty_id uuid NOT NULL REFERENCES counterparties(id),
  platform text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX counterparty_aliases_cp ON counterparty_aliases (counterparty_id);

CREATE TABLE receivables (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  counterparty_id uuid REFERENCES counterparties(id),
  expense_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  part_no integer,
  amount bigint NOT NULL CHECK (amount > 0),
  amount_max bigint CHECK (amount_max IS NULL OR amount_max >= amount),
  expected_by date,
  netted_now boolean NOT NULL DEFAULT false,
  written_off_at timestamptz,
  note text NOT NULL DEFAULT '',
  source text NOT NULL DEFAULT 'tap' CHECK (source IN ('tap', 'split', 'suggestion', 'rule')),
  opened_on date NOT NULL,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX receivables_expense ON receivables (expense_txn_id);
ALTER TABLE links ADD CONSTRAINT links_receivable_fk FOREIGN KEY (receivable_id) REFERENCES receivables(id);

-- +goose StatementBegin
CREATE FUNCTION receivable_dirty() RETURNS trigger AS $$
DECLARE r raw_transactions%ROWTYPE; tid uuid;
BEGIN
  tid := coalesce(NEW.expense_txn_id, OLD.expense_txn_id);
  SELECT * INTO r FROM raw_transactions WHERE raw_transactions.id = tid;
  IF FOUND THEN
    PERFORM mark_month_dirty(r.posted_on, 'receivable');
    PERFORM mark_month_dirty(r.transacted_on, 'receivable');
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER receivables_dirty AFTER INSERT OR UPDATE OR DELETE ON receivables
  FOR EACH ROW EXECUTE FUNCTION receivable_dirty();

-- +goose Down
DROP TRIGGER receivables_dirty ON receivables;
DROP FUNCTION receivable_dirty();
ALTER TABLE links DROP CONSTRAINT links_receivable_fk;
DROP TABLE receivables;
DROP TABLE counterparty_aliases;
DROP TABLE counterparties;
