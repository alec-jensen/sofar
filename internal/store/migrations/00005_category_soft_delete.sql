-- +goose Up
-- Deleting a category or group is soft: ids stay valid for history, undo and foreign keys.
ALTER TABLE category_groups ADD COLUMN deleted_at timestamptz;
ALTER TABLE categories ADD COLUMN deleted_at timestamptz;

-- +goose Down
ALTER TABLE categories DROP COLUMN deleted_at;
ALTER TABLE category_groups DROP COLUMN deleted_at;
