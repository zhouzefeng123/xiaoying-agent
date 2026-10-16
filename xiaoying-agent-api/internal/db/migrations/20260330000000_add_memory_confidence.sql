-- +goose Up
ALTER TABLE memories ADD COLUMN confidence DOUBLE DEFAULT 1.0;
ALTER TABLE memories ADD COLUMN last_accessed_at BIGINT DEFAULT 0;

ALTER TABLE knowledge_tree ADD COLUMN last_accessed_at BIGINT DEFAULT 0;

-- +goose Down
ALTER TABLE knowledge_tree DROP COLUMN last_accessed_at;

ALTER TABLE memories DROP COLUMN last_accessed_at;
ALTER TABLE memories DROP COLUMN confidence;
