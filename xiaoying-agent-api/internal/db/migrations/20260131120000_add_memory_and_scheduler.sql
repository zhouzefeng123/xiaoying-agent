-- +goose Up
CREATE TABLE IF NOT EXISTS memories (
    id VARCHAR(64) NOT NULL,
    category VARCHAR(64) NOT NULL,
    content LONGTEXT NOT NULL,
    source VARCHAR(128) NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY idx_memories_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id VARCHAR(64) NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    parameters LONGTEXT NOT NULL,
    schedule_expression VARCHAR(255) NULL,
    last_run_at BIGINT NULL,
    next_run_at BIGINT NULL,
    is_active TINYINT(1) DEFAULT 1,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS scheduled_tasks;
DROP TABLE IF EXISTS memories;
