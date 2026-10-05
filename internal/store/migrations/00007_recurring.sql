-- +goose Up
-- Recurring things: income streams, bills, subscriptions and payment plans.
-- Rows are configuration; every decision that changes them is also an event.
CREATE TABLE recurring_items (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL CHECK (kind IN ('income','bill','subscription','payment_plan')),
  name text NOT NULL,
  match_key text NOT NULL,
  category_id text REFERENCES categories(id),
  amount bigint NOT NULL CHECK (amount >= 0),
  variable boolean NOT NULL DEFAULT false,
  tolerance_bp integer NOT NULL DEFAULT 1000 CHECK (tolerance_bp BETWEEN 0 AND 10000),
  schedules jsonb NOT NULL,
  active_from date NOT NULL,
  ended_on date,
  deleted_at timestamptz,
  installments_total integer,
  account_id uuid REFERENCES accounts(id),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recurring_items_key_idx ON recurring_items (match_key) WHERE deleted_at IS NULL;

-- "not recurring": detection must not ask again about this merchant.
CREATE TABLE recurring_suppressions (
  match_key text NOT NULL,
  direction text NOT NULL CHECK (direction IN ('in','out')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (match_key, direction)
);

-- Transactions the user attached to an item by hand (mark as recurring).
CREATE TABLE recurring_links (
  item_id uuid NOT NULL REFERENCES recurring_items(id),
  raw_txn_id uuid NOT NULL REFERENCES raw_transactions(id),
  PRIMARY KEY (item_id, raw_txn_id)
);

-- Item changes move the fixed lines, so the months they touch are dirty.
-- +goose StatementBegin
CREATE FUNCTION recurring_item_dirty() RETURNS trigger AS $$
DECLARE d date;
BEGIN
  d := coalesce(NEW.active_from, OLD.active_from);
  PERFORM mark_month_dirty(d, 'recurring');
  IF NEW.ended_on IS NOT NULL THEN PERFORM mark_month_dirty(NEW.ended_on, 'recurring'); END IF;
  IF TG_OP = 'UPDATE' THEN
    PERFORM mark_month_dirty(OLD.active_from, 'recurring');
    IF OLD.ended_on IS NOT NULL THEN PERFORM mark_month_dirty(OLD.ended_on, 'recurring'); END IF;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER recurring_items_dirty AFTER INSERT OR UPDATE ON recurring_items
  FOR EACH ROW EXECUTE FUNCTION recurring_item_dirty();

-- +goose Down
DROP TRIGGER recurring_items_dirty ON recurring_items;
DROP FUNCTION recurring_item_dirty();
DROP TABLE recurring_links, recurring_suppressions, recurring_items;
