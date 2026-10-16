-- +goose Up
CREATE INDEX idx_scheduled_tasks_next_run ON scheduled_tasks (is_active, next_run_at);

-- +goose Down
DROP INDEX idx_scheduled_tasks_next_run ON scheduled_tasks;
