-- +goose Up
-- The rules engine (S18a): rules with layers and stages, versions, and health stats.
CREATE TABLE rules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL DEFAULT '',
  layer text NOT NULL CHECK (layer IN ('user','learned','seed')),
  stage text NOT NULL DEFAULT 'default' CHECK (stage IN ('pre','default','post')),
  conditions jsonb NOT NULL,
  actions jsonb NOT NULL,
  priority integer NOT NULL DEFAULT 100,
  enabled boolean NOT NULL DEFAULT true,
  provenance text NOT NULL DEFAULT 'user',
  valid_from date,
  valid_to date,
  pack text,
  version integer NOT NULL DEFAULT 1,
  probation_left integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);
CREATE INDEX rules_active_idx ON rules (layer, stage, priority) WHERE archived_at IS NULL AND enabled;

CREATE TABLE rule_versions (
  id bigserial PRIMARY KEY,
  rule_id uuid NOT NULL REFERENCES rules(id),
  version integer NOT NULL,
  snapshot jsonb NOT NULL,
  at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (rule_id, version)
);

CREATE TABLE rule_stats (
  rule_id uuid PRIMARY KEY REFERENCES rules(id),
  hits integer NOT NULL DEFAULT 0,
  accepted integer NOT NULL DEFAULT 0,
  corrected integer NOT NULL DEFAULT 0,
  last_hit timestamptz
);

-- +goose Down
DROP TABLE rule_stats, rule_versions, rules;
