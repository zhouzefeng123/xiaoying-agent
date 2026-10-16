-- +goose Up
-- Sessions
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(64) NOT NULL,
    parent_session_id VARCHAR(64) NULL,
    title VARCHAR(512) NOT NULL,
    message_count BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cost DOUBLE NOT NULL DEFAULT 0.0,
    updated_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    created_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Files
CREATE TABLE IF NOT EXISTS files (
    id VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL,
    path VARCHAR(400) NOT NULL,
    content LONGTEXT NOT NULL,
    version VARCHAR(64) NOT NULL,
    created_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    updated_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    PRIMARY KEY (id),
    UNIQUE KEY uk_files_path_session_version (path, session_id, version),
    KEY idx_files_session_id (session_id),
    KEY idx_files_path (path),
    CONSTRAINT fk_files_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Messages
CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL,
    role VARCHAR(32) NOT NULL,
    parts LONGTEXT NOT NULL,
    model VARCHAR(128) NULL,
    created_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    updated_at BIGINT NOT NULL,  -- Unix timestamp in milliseconds
    finished_at BIGINT NULL,     -- Unix timestamp in milliseconds
    PRIMARY KEY (id),
    KEY idx_messages_session_id (session_id),
    CONSTRAINT fk_messages_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- MySQL refuses to update the subject table from its own trigger, so the
-- per-row `updated_at` maintenance that SQLite did with triggers is now done by
-- the application queries (see UpdateSession / UpdateFile / UpdateMessage).
-- The `message_count` triggers are created at startup by db.Connect (see
-- ensureTriggers) because sqlc cannot parse CREATE TRIGGER in the schema files.

-- +goose Down
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
