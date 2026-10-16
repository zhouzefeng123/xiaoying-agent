-- +goose Up
ALTER TABLE messages ADD COLUMN embedding TEXT NULL;

-- +goose Down
ALTER TABLE messages DROP COLUMN embedding;
