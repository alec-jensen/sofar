-- +goose Up
-- Effective-dated budget configuration (S17): general savings rule and the
-- emergency fund. A version applies from its month until a later one starts.
CREATE TABLE config_versions (
  id bigserial PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('savings_rule','ef')),
  effective_month date NOT NULL,
  payload jsonb NOT NULL,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX config_versions_kind_idx ON config_versions (kind, effective_month) WHERE deleted_at IS NULL;

CREATE TABLE goals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  category_id text NOT NULL REFERENCES categories(id),
  -- effective-dated: [{"from":"2026-10","target":cents,"monthly":cents,"paused":bool,"target_date":"2027-06"|null}, ...]
  versions jsonb NOT NULL,
  priority integer NOT NULL DEFAULT 100,
  created_month date NOT NULL,
  completed_on date,
  abandoned_on date,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION config_dirty() RETURNS trigger AS $$
BEGIN
  PERFORM mark_month_dirty(NEW.effective_month, 'config');
  IF TG_OP = 'UPDATE' THEN PERFORM mark_month_dirty(OLD.effective_month, 'config'); END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER config_versions_dirty AFTER INSERT OR UPDATE ON config_versions
  FOR EACH ROW EXECUTE FUNCTION config_dirty();

-- +goose StatementBegin
CREATE FUNCTION goal_dirty() RETURNS trigger AS $$
BEGIN
  PERFORM mark_month_dirty(NEW.created_month, 'goal');
  IF NEW.completed_on IS NOT NULL THEN PERFORM mark_month_dirty(NEW.completed_on, 'goal'); END IF;
  IF NEW.abandoned_on IS NOT NULL THEN PERFORM mark_month_dirty(NEW.abandoned_on, 'goal'); END IF;
  IF TG_OP = 'UPDATE' THEN
    PERFORM mark_month_dirty(OLD.created_month, 'goal');
    IF OLD.completed_on IS NOT NULL THEN PERFORM mark_month_dirty(OLD.completed_on, 'goal'); END IF;
    IF OLD.abandoned_on IS NOT NULL THEN PERFORM mark_month_dirty(OLD.abandoned_on, 'goal'); END IF;
    -- pausing, a new monthly amount or a new target change every month from the start
    PERFORM mark_month_dirty(NEW.created_month, 'goal');
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER goals_dirty AFTER INSERT OR UPDATE ON goals
  FOR EACH ROW EXECUTE FUNCTION goal_dirty();

-- +goose Down
DROP TRIGGER goals_dirty ON goals;
DROP TRIGGER config_versions_dirty ON config_versions;
DROP FUNCTION goal_dirty(), config_dirty();
DROP TABLE goals, config_versions;
