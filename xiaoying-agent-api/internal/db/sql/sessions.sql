-- NOTE: CreateSession / UpdateSession are hand written in queries_manual.go
-- because MySQL has no RETURNING clause.

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = ? LIMIT 1;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
ORDER BY created_at DESC;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: GetTokenStats :one
SELECT
    CAST(COALESCE(SUM(prompt_tokens), 0) AS SIGNED) as input_tokens,
    CAST(COALESCE(SUM(completion_tokens), 0) AS SIGNED) as output_tokens
FROM sessions;
