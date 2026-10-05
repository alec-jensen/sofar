-- +goose Up
-- Moves a pre-rebuild database's tables into the "legacy" schema so the new
-- tables can reuse their names. Nothing is dropped; sofar never drops "legacy".
-- A fresh database has no legacy layout (detected by transactions.review_status) and is untouched.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = 'public' AND table_name = 'transactions' AND column_name = 'review_status') THEN
    CREATE SCHEMA IF NOT EXISTS legacy;
    FOREACH t IN ARRAY ARRAY['users','sessions','simplefin','connections','accounts','categories','subcategories',
      'recurring_groups','transactions','rules','reimbursement_links','savings_goals','budget_periods',
      'push_subscriptions','settings','applied_actions','transaction_splits','ignore_rules']
    LOOP
      EXECUTE format('ALTER TABLE IF EXISTS public.%I SET SCHEMA legacy', t);
    END LOOP;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Quarantine is one-way by design.
SELECT 1;
