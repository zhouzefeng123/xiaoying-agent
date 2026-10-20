-- +goose Up
ALTER TABLE knowledge_tree
    ADD COLUMN owner_email VARCHAR(320) NOT NULL DEFAULT '';

CREATE INDEX idx_knowledge_tree_owner_layer
    ON knowledge_tree (owner_email, layer, confidence);

ALTER TABLE scheduled_tasks
    ADD COLUMN owner_email VARCHAR(320) NOT NULL DEFAULT '';

CREATE INDEX idx_scheduled_tasks_owner_active
    ON scheduled_tasks (owner_email, is_active, next_run_at);

ALTER TABLE saved_news
    ADD COLUMN owner_email VARCHAR(320) NOT NULL DEFAULT '';

CREATE INDEX idx_saved_news_owner_created
    ON saved_news (owner_email, created_at);

ALTER TABLE memories
    ADD COLUMN owner_email VARCHAR(320) NOT NULL DEFAULT '';

CREATE INDEX idx_memories_owner_category
    ON memories (owner_email, category);

-- Cognitive state keys are namespaced by owner at the application layer.
ALTER TABLE cognitive_state
    MODIFY COLUMN `key` VARCHAR(512) NOT NULL;

-- +goose Down
ALTER TABLE cognitive_state
    MODIFY COLUMN `key` VARCHAR(64) NOT NULL;

DROP INDEX idx_memories_owner_category ON memories;
ALTER TABLE memories DROP COLUMN owner_email;

DROP INDEX idx_saved_news_owner_created ON saved_news;
ALTER TABLE saved_news DROP COLUMN owner_email;

DROP INDEX idx_scheduled_tasks_owner_active ON scheduled_tasks;
ALTER TABLE scheduled_tasks DROP COLUMN owner_email;

DROP INDEX idx_knowledge_tree_owner_layer ON knowledge_tree;
ALTER TABLE knowledge_tree DROP COLUMN owner_email;
