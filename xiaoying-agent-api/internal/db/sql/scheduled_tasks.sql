-- NOTE: CreateScheduledTask / UpdateScheduledTask are hand written in
-- queries_manual.go because MySQL has no RETURNING clause.

-- name: GetScheduledTask :one
SELECT * FROM scheduled_tasks
WHERE id = ? LIMIT 1;

-- name: ListScheduledTasks :many
SELECT * FROM scheduled_tasks
ORDER BY created_at DESC;

-- name: ListActiveScheduledTasks :many
SELECT * FROM scheduled_tasks
WHERE is_active = 1
ORDER BY created_at DESC;

-- name: DeleteScheduledTask :exec
DELETE FROM scheduled_tasks
WHERE id = ?;
