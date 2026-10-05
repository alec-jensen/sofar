-- +goose Up
CREATE TABLE month_snapshots (
  month date PRIMARY KEY,
  engine_version integer NOT NULL,
  input_hash text NOT NULL,
  state jsonb NOT NULL,
  computed_at timestamptz NOT NULL DEFAULT now()
);

-- Months whose inputs may have changed. Triggers fill it in the same
-- transaction as the change (architecture A1).
CREATE TABLE dirty_months (
  month date PRIMARY KEY,
  reason text NOT NULL,
  marked_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION mark_month_dirty(d date, why text) RETURNS void AS $$
DECLARE m date;
BEGIN
  IF d IS NULL THEN RETURN; END IF;
  m := date_trunc('month', d)::date;
  -- the month itself, the one it feeds (plan = last month's income), and the
  -- previous one when the date is near a boundary (zone conversion of an instant)
  INSERT INTO dirty_months (month, reason) VALUES (m, why) ON CONFLICT DO NOTHING;
  INSERT INTO dirty_months (month, reason) VALUES ((m + interval '1 month')::date, why) ON CONFLICT DO NOTHING;
  IF extract(day FROM d) <= 2 THEN
    INSERT INTO dirty_months (month, reason) VALUES ((m - interval '1 month')::date, why) ON CONFLICT DO NOTHING;
  END IF;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION raw_txn_dirty() RETURNS trigger AS $$
BEGIN
  PERFORM mark_month_dirty(NEW.posted_on, 'transaction');
  PERFORM mark_month_dirty(NEW.transacted_on, 'transaction');
  IF TG_OP = 'UPDATE' THEN
    PERFORM mark_month_dirty(OLD.posted_on, 'transaction');
    PERFORM mark_month_dirty(OLD.transacted_on, 'transaction');
    IF OLD.import_batch_id IS DISTINCT FROM NEW.import_batch_id THEN
      PERFORM mark_month_dirty(NEW.posted_on, 'import');
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER raw_transactions_dirty AFTER INSERT OR UPDATE ON raw_transactions
  FOR EACH ROW EXECUTE FUNCTION raw_txn_dirty();

-- +goose StatementBegin
CREATE FUNCTION overlay_dirty() RETURNS trigger AS $$
DECLARE r raw_transactions%ROWTYPE; tid uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN tid := OLD.raw_txn_id; ELSE tid := NEW.raw_txn_id; END IF;
  SELECT * INTO r FROM raw_transactions WHERE raw_transactions.id = tid;
  IF FOUND THEN
    PERFORM mark_month_dirty(r.posted_on, 'overlay');
    PERFORM mark_month_dirty(r.transacted_on, 'overlay');
  END IF;
  IF TG_OP <> 'INSERT' THEN PERFORM mark_month_dirty(OLD.month_override, 'overlay'); END IF;
  IF TG_OP <> 'DELETE' THEN PERFORM mark_month_dirty(NEW.month_override, 'overlay'); END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER overlays_dirty AFTER INSERT OR UPDATE OR DELETE ON transaction_overlays
  FOR EACH ROW EXECUTE FUNCTION overlay_dirty();

-- +goose StatementBegin
CREATE FUNCTION split_dirty() RETURNS trigger AS $$
DECLARE r raw_transactions%ROWTYPE; tid uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN tid := OLD.raw_txn_id; ELSE tid := NEW.raw_txn_id; END IF;
  SELECT * INTO r FROM raw_transactions WHERE raw_transactions.id = tid;
  IF FOUND THEN
    PERFORM mark_month_dirty(r.posted_on, 'split');
    PERFORM mark_month_dirty(r.transacted_on, 'split');
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER splits_dirty AFTER INSERT OR UPDATE OR DELETE ON txn_splits
  FOR EACH ROW EXECUTE FUNCTION split_dirty();

-- +goose StatementBegin
CREATE FUNCTION import_batch_dirty() RETURNS trigger AS $$
BEGIN
  IF OLD.status IS DISTINCT FROM NEW.status THEN
    PERFORM mark_month_dirty(d, 'import') FROM (SELECT DISTINCT date_trunc('month', posted_on)::date AS d FROM raw_transactions WHERE import_batch_id = NEW.id) x;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER import_batches_dirty AFTER UPDATE ON import_batches
  FOR EACH ROW EXECUTE FUNCTION import_batch_dirty();

-- Budget events and plan overrides name their month in subject_id ("2026-10").
-- +goose StatementBegin
CREATE FUNCTION budget_event_dirty() RETURNS trigger AS $$
BEGIN
  IF NEW.subject_type = 'month' AND NEW.subject_id ~ '^[0-9]{4}-[0-9]{2}$' THEN
    PERFORM mark_month_dirty((NEW.subject_id || '-01')::date, 'event');
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER events_budget_dirty AFTER INSERT ON events
  FOR EACH ROW EXECUTE FUNCTION budget_event_dirty();

-- Changing which accounts count changes every month.
-- +goose StatementBegin
CREATE FUNCTION account_dirty() RETURNS trigger AS $$
BEGIN
  IF OLD.kind IS DISTINCT FROM NEW.kind OR OLD.active IS DISTINCT FROM NEW.active THEN
    PERFORM mark_month_dirty(d, 'account') FROM (SELECT DISTINCT date_trunc('month', posted_on)::date AS d FROM raw_transactions WHERE account_id = NEW.id) x;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER accounts_dirty AFTER UPDATE ON accounts
  FOR EACH ROW EXECUTE FUNCTION account_dirty();

-- +goose Down
DROP TRIGGER accounts_dirty ON accounts;
DROP TRIGGER events_budget_dirty ON events;
DROP TRIGGER import_batches_dirty ON import_batches;
DROP TRIGGER splits_dirty ON txn_splits;
DROP TRIGGER overlays_dirty ON transaction_overlays;
DROP TRIGGER raw_transactions_dirty ON raw_transactions;
DROP FUNCTION account_dirty(), budget_event_dirty(), import_batch_dirty(), split_dirty(), overlay_dirty(), raw_txn_dirty(), mark_month_dirty(date, text);
DROP TABLE dirty_months, month_snapshots;
