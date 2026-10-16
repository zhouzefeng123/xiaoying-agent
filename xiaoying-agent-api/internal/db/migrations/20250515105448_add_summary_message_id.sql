-- +goose Up
ALTER TABLE sessions ADD COLUMN summary_message_id VARCHAR(64) NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN summary_message_id;
