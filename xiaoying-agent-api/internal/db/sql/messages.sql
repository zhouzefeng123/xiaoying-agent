-- NOTE: CreateMessage is hand written in queries_manual.go because MySQL has
-- no RETURNING clause.

-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: ListRecentMessagesBySession :many
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?;

-- name: GetMessageCreatedAt :one
SELECT created_at
FROM messages
WHERE id = ? LIMIT 1;

-- NOTE: UpdateMessage is hand written in queries_manual.go so the timestamp is
-- produced in Go and the statement stays portable across MySQL and SQLite.

-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesWithEmbedding :many
SELECT *
FROM messages
WHERE role IN ('user', 'assistant') AND embedding IS NOT NULL;

-- name: ListSessionMessagesWithEmbedding :many
SELECT *
FROM messages
WHERE session_id = ?
  AND role IN ('user', 'assistant')
  AND embedding IS NOT NULL;
