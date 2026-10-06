-- +goose Up
-- Links (S12, A3): a payment (reimbursement or refund) netted against an
-- expense, wholly or in part, in the expense's month. A tentative link nets
-- immediately; rejecting it reverts the math exactly.
CREATE TABLE links (
  id bigserial PRIMARY KEY,
  payment_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  expense_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  part_no integer,                       -- the split part of the expense, if any
  receivable_id uuid,                    -- set when the link satisfies a receivable (M7.3)
  amount bigint NOT NULL CHECK (amount > 0),
  state text NOT NULL CHECK (state IN ('tentative', 'confirmed', 'rejected', 'removed')),
  created_by text NOT NULL CHECK (created_by IN ('user', 'auto_receivable', 'auto_pattern')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (payment_txn_id <> expense_txn_id)
);
CREATE INDEX links_payment ON links (payment_txn_id);
CREATE INDEX links_expense ON links (expense_txn_id);

-- +goose StatementBegin
CREATE FUNCTION link_dirty() RETURNS trigger AS $$
DECLARE r raw_transactions%ROWTYPE; tid uuid;
BEGIN
  FOREACH tid IN ARRAY ARRAY[coalesce(NEW.payment_txn_id, OLD.payment_txn_id), coalesce(NEW.expense_txn_id, OLD.expense_txn_id)] LOOP
    SELECT * INTO r FROM raw_transactions WHERE raw_transactions.id = tid;
    IF FOUND THEN
      PERFORM mark_month_dirty(r.posted_on, 'link');
      PERFORM mark_month_dirty(r.transacted_on, 'link');
    END IF;
  END LOOP;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER links_dirty AFTER INSERT OR UPDATE OR DELETE ON links
  FOR EACH ROW EXECUTE FUNCTION link_dirty();

-- +goose Down
DROP TRIGGER links_dirty ON links;
DROP FUNCTION link_dirty();
DROP TABLE links;
