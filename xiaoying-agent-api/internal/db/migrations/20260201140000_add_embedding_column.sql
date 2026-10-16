-- +goose Up
ALTER TABLE memories ADD COLUMN embedding TEXT NULL;

-- +goose Down
ALTER TABLE memories DROP COLUMN embedding;
