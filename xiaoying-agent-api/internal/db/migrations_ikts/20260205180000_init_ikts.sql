-- +goose Up
CREATE TABLE knowledge_tree (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    layer INTEGER NOT NULL, -- 1=Core, 2=Domain, 3=Knowledge, 4=Episodic
    category TEXT NOT NULL, -- 'identity', 'profile', 'tech', 'experience', 'episodic', 'failure_log'
    content TEXT NOT NULL,
    summary TEXT,           -- For L3 indexing
    keywords TEXT,          -- For search
    confidence REAL DEFAULT 1.0,
    source TEXT,            -- 'user', 'reflection', 'exploration', 'system'
    embedding TEXT,         -- Vector embedding (JSON string)
    access_count INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_knowledge_tree_layer ON knowledge_tree(layer);
CREATE INDEX idx_knowledge_tree_category ON knowledge_tree(category);
CREATE INDEX idx_knowledge_tree_parent ON knowledge_tree(parent_id);

CREATE TABLE cognitive_state (
    key TEXT PRIMARY KEY,   -- 'emotion', 'daily_token_usage', 'daily_token_limit', 'current_task'
    value TEXT,             -- JSON value
    updated_at INTEGER NOT NULL
);

-- Initialize default cognitive state
INSERT INTO cognitive_state (key, value, updated_at) VALUES ('emotion', '{"happiness": 0.8, "energy": 1.0}', STRFTIME('%s', 'now'));
INSERT INTO cognitive_state (key, value, updated_at) VALUES ('daily_token_usage', '0', STRFTIME('%s', 'now'));
INSERT INTO cognitive_state (key, value, updated_at) VALUES ('daily_token_limit', '50000', STRFTIME('%s', 'now'));

-- +goose Down
DROP TABLE knowledge_tree;
DROP TABLE cognitive_state;
