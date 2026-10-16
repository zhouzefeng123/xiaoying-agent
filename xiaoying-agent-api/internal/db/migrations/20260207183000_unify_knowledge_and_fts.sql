-- +goose Up
-- 1. Create Knowledge Tree Table (Migrated from IKTS)
-- The SQLite build used an `fts5` virtual table plus sync triggers; MySQL
-- indexes the base table directly with an ngram FULLTEXT index so Chinese
-- text is tokenised sensibly.
CREATE TABLE IF NOT EXISTS knowledge_tree (
    id VARCHAR(64) NOT NULL,
    parent_id VARCHAR(64) NULL,
    layer BIGINT NOT NULL, -- 1=Core, 2=Domain, 3=Knowledge, 4=Episodic
    category VARCHAR(64) NOT NULL,
    content LONGTEXT NOT NULL,
    summary TEXT NULL,
    keywords TEXT NULL,
    confidence DOUBLE DEFAULT 1.0,
    source VARCHAR(128) NULL,
    embedding TEXT NULL,
    access_count BIGINT DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY idx_knowledge_tree_layer (layer),
    KEY idx_knowledge_tree_category (category),
    KEY idx_knowledge_tree_parent (parent_id),
    FULLTEXT KEY ft_knowledge_tree (content, summary, keywords) WITH PARSER ngram
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 2. Create Cognitive State Table (Migrated from IKTS)
CREATE TABLE IF NOT EXISTS cognitive_state (
    `key` VARCHAR(64) NOT NULL,
    value LONGTEXT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Initialize default cognitive state if not exists
INSERT IGNORE INTO cognitive_state (`key`, value, updated_at) VALUES ('emotion', '{"happiness": 0.8, "energy": 1.0}', UNIX_TIMESTAMP());
INSERT IGNORE INTO cognitive_state (`key`, value, updated_at) VALUES ('daily_token_usage', '0', UNIX_TIMESTAMP());
INSERT IGNORE INTO cognitive_state (`key`, value, updated_at) VALUES ('daily_token_limit', '50000', UNIX_TIMESTAMP());

-- 3. Data Migration (Move memories to knowledge_tree)
INSERT INTO knowledge_tree (id, layer, category, content, source, embedding, created_at, updated_at)
SELECT
    id,
    CASE WHEN category IN ('habit', 'preference', 'identity') THEN 1 ELSE 2 END, -- 1=Core, 2=Domain
    category,
    content,
    source,
    embedding,
    created_at,
    updated_at
FROM memories;

-- +goose Down
DROP TABLE IF EXISTS cognitive_state;
DROP TABLE IF EXISTS knowledge_tree;
