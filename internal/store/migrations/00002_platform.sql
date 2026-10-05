-- +goose Up
CREATE TABLE users (
  id integer PRIMARY KEY CHECK (id = 1),
  username text UNIQUE NOT NULL,
  password_hash text NOT NULL,
  totp_secret text,
  totp_pending text,
  totp_last_step bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash text PRIMARY KEY,
  user_id integer NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  user_agent text NOT NULL DEFAULT '',
  ip_hint text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE recovery_codes (
  id bigserial PRIMARY KEY,
  user_id integer NOT NULL REFERENCES users(id),
  hash text NOT NULL,
  used_at timestamptz
);

CREATE TABLE push_subscriptions (
  endpoint text PRIMARY KEY,
  user_id integer NOT NULL REFERENCES users(id),
  keys jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE settings (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE events (
  id bigserial PRIMARY KEY,
  at timestamptz NOT NULL DEFAULT now(),
  actor text NOT NULL,
  kind text NOT NULL,
  subject_type text NOT NULL DEFAULT '',
  subject_id text NOT NULL DEFAULT '',
  payload jsonb NOT NULL DEFAULT '{}',
  reverses bigint REFERENCES events(id),
  idempotency_key text UNIQUE
);
CREATE INDEX events_subject_idx ON events (subject_type, subject_id);

CREATE TABLE jobs (
  id bigserial PRIMARY KEY,
  kind text NOT NULL,
  key text NOT NULL UNIQUE,
  run_at timestamptz NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  locked_until timestamptz,
  payload jsonb NOT NULL DEFAULT '{}',
  last_error text,
  done_at timestamptz
);
CREATE INDEX jobs_due_idx ON jobs (run_at) WHERE done_at IS NULL;

CREATE TABLE provider_connections (
  id text PRIMARY KEY,
  provider text NOT NULL,
  access_url_enc text NOT NULL,
  status text NOT NULL DEFAULT 'ok',
  last_ok_at timestamptz,
  error_json jsonb
);

CREATE TABLE audit_log (
  id bigserial PRIMARY KEY,
  at timestamptz NOT NULL DEFAULT now(),
  actor text NOT NULL,
  action text NOT NULL,
  detail jsonb NOT NULL DEFAULT '{}'
);
-- +goose StatementBegin
CREATE FUNCTION audit_log_append_only() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'audit_log is append-only';
END $$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER audit_log_no_change BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();

-- +goose Down
DROP TABLE audit_log, provider_connections, jobs, events, settings, push_subscriptions, recovery_codes, sessions, users;
DROP FUNCTION audit_log_append_only();
