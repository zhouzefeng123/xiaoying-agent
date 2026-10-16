-- +goose Up
CREATE TABLE IF NOT EXISTS saved_news (
    id VARCHAR(64) NOT NULL,
    title VARCHAR(512) NOT NULL,
    summary LONGTEXT NOT NULL,
    category VARCHAR(64) NOT NULL,
    url TEXT NOT NULL,
    published_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS saved_news;
