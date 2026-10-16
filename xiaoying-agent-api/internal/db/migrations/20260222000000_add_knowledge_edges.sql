-- +goose Up
CREATE TABLE IF NOT EXISTS knowledge_edges (
    source_id VARCHAR(64) NOT NULL,
    target_id VARCHAR(64) NOT NULL,
    relation VARCHAR(64) NOT NULL, -- e.g., 'related_to', 'supports', 'contradicts', 'part_of'
    weight DOUBLE DEFAULT 1.0,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (source_id, target_id, relation),
    KEY idx_knowledge_edges_source (source_id),
    KEY idx_knowledge_edges_target (target_id),
    CONSTRAINT fk_knowledge_edges_source FOREIGN KEY (source_id) REFERENCES knowledge_tree (id) ON DELETE CASCADE,
    CONSTRAINT fk_knowledge_edges_target FOREIGN KEY (target_id) REFERENCES knowledge_tree (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS knowledge_edges;
