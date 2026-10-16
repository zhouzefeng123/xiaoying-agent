-- NOTE: CreateFile / UpdateFile are hand written in queries_manual.go because
-- MySQL has no RETURNING clause. The dropped `ListNewFiles` query referenced a
-- column (`is_new`) that no migration ever created and had no callers.

-- name: GetFile :one
SELECT *
FROM files
WHERE id = ? LIMIT 1;

-- name: GetFileByPathAndSession :one
SELECT *
FROM files
WHERE path = ? AND session_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: ListFilesBySession :many
SELECT *
FROM files
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: ListFilesByPath :many
SELECT *
FROM files
WHERE path = ?
ORDER BY created_at DESC;

-- name: DeleteFile :exec
DELETE FROM files
WHERE id = ?;

-- name: DeleteSessionFiles :exec
DELETE FROM files
WHERE session_id = ?;

-- name: ListLatestSessionFiles :many
SELECT f.*
FROM files f
INNER JOIN (
    SELECT path, MAX(created_at) as max_created_at
    FROM files
    GROUP BY path
) latest ON f.path = latest.path AND f.created_at = latest.max_created_at
WHERE f.session_id = ?
ORDER BY f.path;
