-- NOTE: CreateMemory / UpdateMemory are hand written in queries_manual.go
-- because MySQL has no RETURNING clause.

-- name: GetMemory :one
SELECT * FROM memories
WHERE id = ? LIMIT 1;

-- name: ListMemories :many
SELECT * FROM memories
ORDER BY created_at DESC;

-- name: ListMemoriesByCategory :many
SELECT * FROM memories
WHERE category = ?
ORDER BY created_at DESC;

-- name: UpdateMemoryEmbedding :exec
UPDATE memories
SET embedding = ?
WHERE id = ?;

-- name: DeleteMemory :exec
DELETE FROM memories
WHERE id = ?;

-- name: UpdateMemoryConfidence :exec
UPDATE memories
SET confidence = ?,
    last_accessed_at = ?
WHERE id = ?;

-- name: UpdateMemoryCategory :exec
UPDATE memories
SET category = ?
WHERE id = ?;
