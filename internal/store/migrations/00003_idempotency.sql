-- +goose Up
-- Responses to mutating requests that carried an Idempotency-Key header, so a
-- retry (for example after an offline-queue replay) returns the first result.
CREATE TABLE idempotency_keys (
  key text PRIMARY KEY,
  request_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  status integer,
  content_type text,
  body bytea
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
