-- +goose Up
ALTER TABLE sessions
    ADD COLUMN owner_email VARCHAR(320) NOT NULL DEFAULT '';

CREATE INDEX idx_sessions_owner
    ON sessions (owner_email, parent_session_id, created_at);

-- +goose Down
DROP INDEX idx_sessions_owner ON sessions;
ALTER TABLE sessions DROP COLUMN owner_email;
